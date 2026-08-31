"""
Verify existing embedding ONNX model, re-export with opset=17 if broken.

Usage:
    python verify_models.py
"""
import os, sys
os.environ["TF_CPP_MIN_LOG_LEVEL"] = "3"
os.environ["TF_ENABLE_ONEDNN_OPTS"] = "0"

import onnxruntime as ort
import numpy as np

def verify_embedding(path="models/tcg/embedding_model.onnx"):
    try:
        sess = ort.InferenceSession(path, providers=["CPUExecutionProvider"])
        inp = sess.get_inputs()[0]
        print(f"OK  input : {inp.name} {inp.shape}")  # expect (None,224,224,3)
        dummy = np.random.rand(1, 224, 224, 3).astype(np.float32)
        out = sess.run(None, {inp.name: dummy})
        print(f"OK  output: {out[0].shape}")           # expect (1, 512)
        return True
    except Exception as e:
        print(f"FAIL: {e}")
        return False

def reexport_embedding(keras_path="models/tcg/embedding_model.keras",
                       out_path="models/tcg/embedding_model.onnx"):
    import tensorflow as tf
    import tf2onnx
    import keras
    keras.mixed_precision.set_global_policy("float32")
    model = keras.saving.load_model(keras_path, compile=False)

    layer_names = [l.name for l in model.layers]
    print("Layers:", layer_names)
    embed_model = model.get_layer("embedding_model") if "embedding_model" in layer_names else model

    input_sig = [tf.TensorSpec([None, 224, 224, 3], tf.float32, name="input")]
    onnx_model, _ = tf2onnx.convert.from_keras(
        embed_model, input_signature=input_sig, opset=17)
    import onnx
    onnx.save(onnx_model, out_path)
    print(f"Saved → {out_path} ({os.path.getsize(out_path)/1e6:.1f} MB)")

print("=== Verifying embedding ONNX ===")
if verify_embedding():
    print("Model is valid, no re-export needed.")
else:
    print("\nRe-exporting with opset=17...")
    reexport_embedding()
    print("\n=== Re-verifying ===")
    verify_embedding()
