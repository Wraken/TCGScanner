"""
Re-export the embedding model to ONNX with a genuine float32 graph.

Problem: the model was trained with mixed_float16, and the per-layer dtype
policies are saved inside embedding_model.keras. Setting the global policy to
float32 at load time does NOT change them, so every export so far produced an
ONNX whose ops (and output tensor "normalize") are float16 — which the Go side
(pkg/ort) reads as float32 → garbage.

Fix: rewrite the dtype policies inside the model config (mixed_float16 →
float32), rebuild the model from that config, copy the weights (weights are
already stored fp32 by mixed precision), then export with tf2onnx.

Usage:
    python reexport_onnx_fp32.py
    python reexport_onnx_fp32.py --model models/tcg/embedding_model.keras --output models/tcg/embedding_model.onnx
"""

import argparse
import json
import os

parser = argparse.ArgumentParser()
parser.add_argument("--model",  default="models/tcg/embedding_model.keras")
parser.add_argument("--output", default="models/tcg/embedding_model.onnx")
parser.add_argument("--opset",  type=int, default=17)
args = parser.parse_args()

os.environ["TF_CPP_MIN_LOG_LEVEL"] = "2"
os.environ["TF_XLA_FLAGS"] = "--tf_xla_auto_jit=0"

import numpy as np
import tensorflow as tf
import keras

keras.mixed_precision.set_global_policy("float32")

print(f"Chargement : {args.model}")
model = keras.models.load_model(args.model, compile=False)
img_size = model.input_shape[1]

# ─── Réécriture des dtype policies dans la config ─────────────────────────────

def force_float32(node):
    """Remplace récursivement toute policy mixed_float16 par float32."""
    if isinstance(node, dict):
        for k, v in list(node.items()):
            if k == "dtype":
                if v == "mixed_float16" or v == "float16":
                    node[k] = "float32"
                elif isinstance(v, dict) and v.get("config", {}).get("name") in ("mixed_float16", "float16"):
                    v["config"]["name"] = "float32"
            else:
                force_float32(v)
    elif isinstance(node, list):
        for item in node:
            force_float32(item)

config = model.get_config()
n_before = json.dumps(config).count("mixed_float16")
force_float32(config)
n_after = json.dumps(config).count("mixed_float16")
print(f"Policies réécrites : {n_before} mixed_float16 → reste {n_after}")

fp32_model = keras.Model.from_config(config)
fp32_model.set_weights(model.get_weights())

# ─── Sanity : mêmes sorties (à la tolérance fp16 près) ────────────────────────

rng = np.random.default_rng(42)
dummy = rng.random((2, img_size, img_size, 3), dtype=np.float32) * 255.0
ref = np.asarray(model(dummy, training=False), dtype=np.float32)
new = np.asarray(fp32_model(dummy, training=False), dtype=np.float32)
cos = float((ref * new).sum(axis=1).mean())  # les deux sont L2-normalisés
print(f"Cosinus fp16-model vs fp32-model : {cos:.6f} (attendu > 0.999)")
assert cos > 0.999, "le clone fp32 diverge du modèle original !"

# ─── Export ONNX ──────────────────────────────────────────────────────────────

import tf2onnx

input_signature = [tf.TensorSpec((None, img_size, img_size, 3), tf.float32, name="input_image")]
print(f"\nExport ONNX (opset={args.opset}) → {args.output}")
tf2onnx.convert.from_keras(fp32_model, input_signature=input_signature,
                           opset=args.opset, output_path=args.output)
print(f"  ✓ {os.path.getsize(args.output) / 1e6:.1f} MB")

# ─── Vérification finale : dtype de sortie + parité ───────────────────────────

import onnxruntime as ort

sess = ort.InferenceSession(args.output, providers=["CPUExecutionProvider"])
inp = sess.get_inputs()[0]
out = sess.get_outputs()[0]
print(f"\nONNX  in : {inp.name} {inp.shape} {inp.type}")
print(f"ONNX out : {out.name} {out.shape} {out.type}")
assert out.type == "tensor(float)", f"la sortie est encore {out.type} !"

onnx_out = sess.run(None, {inp.name: dummy})[0].astype(np.float32)
cos = float((new * onnx_out).sum(axis=1).mean())
max_diff = float(np.abs(new - onnx_out).max())
print(f"Parité keras-fp32 vs ONNX : cosinus={cos:.6f}  max_diff={max_diff:.6f}")
assert cos > 0.9999, "ONNX diverge du modèle keras fp32 !"

print("\n✓ ONNX float32 prêt — lisible par pkg/ort côté Go.")
