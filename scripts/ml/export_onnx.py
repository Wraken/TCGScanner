"""
Export the embedding model to ONNX (float32, frozen graph).

Freezes the TF graph to inline StatefulPartitionedCall ops from ConvNeXt's
XLA-compiled depthwise convolutions, then converts to ONNX.

Usage:
    python export_onnx.py
    python export_onnx.py --model models/tcg/embedding_model.keras --output models/tcg/embedding_model.onnx
"""

import argparse
import os
import numpy as np

parser = argparse.ArgumentParser()
parser.add_argument("--model",  default="models/tcg/embedding_model.keras")
parser.add_argument("--output", default="models/tcg/embedding_model.onnx")
parser.add_argument("--opset",  type=int, default=13)
args = parser.parse_args()

os.environ["TF_CPP_MIN_LOG_LEVEL"] = "2"
os.environ["TF_XLA_FLAGS"] = "--tf_xla_auto_jit=0"
import tensorflow as tf
import keras

# Force float32
keras.mixed_precision.set_global_policy("float32")

print(f"Loading model: {args.model}")
model = keras.models.load_model(args.model, compile=False)

img_size = model.input_shape[1]
dummy = np.random.rand(1, img_size, img_size, 3).astype(np.float32)
out = model(dummy, training=False)
print(f"  Input  : (1, {img_size}, {img_size}, 3)")
print(f"  Output : {out.shape}")

# Step 1: Get concrete function
print("\nTracing concrete function...")

@tf.function(input_signature=[tf.TensorSpec((1, img_size, img_size, 3), tf.float32)])
def serve(x):
    return model(x, training=False)

concrete_func = serve.get_concrete_function()

# Step 2: Freeze — inlines all function bodies (removes StatefulPartitionedCall)
print("Freezing graph (inlining all function calls)...")
from tensorflow.python.framework.convert_to_constants import convert_variables_to_constants_v2
frozen_func = convert_variables_to_constants_v2(concrete_func)
graph_def = frozen_func.graph.as_graph_def()

# Check that StatefulPartitionedCall is gone
ops = set(n.op for n in graph_def.node)
if "StatefulPartitionedCall" in ops:
    print(f"  ⚠ StatefulPartitionedCall still present!")
else:
    print(f"  ✓ Graph frozen: {len(graph_def.node)} nodes, no StatefulPartitionedCall")

# Step 3: Save frozen graph as .pb
frozen_pb = "/tmp/embedding_frozen.pb"
tf.io.write_graph(graph_def, os.path.dirname(frozen_pb), os.path.basename(frozen_pb), as_text=False)

# Get input/output tensor names from the concrete function
input_names = [t.name for t in concrete_func.inputs if t.dtype != tf.resource]
output_names = [t.name for t in concrete_func.outputs]
print(f"  Inputs  : {input_names}")
print(f"  Outputs : {output_names}")

# Step 4: Convert frozen graph to ONNX
print(f"\nConverting to ONNX (opset={args.opset})...")
import subprocess
result = subprocess.run([
    "python", "-m", "tf2onnx.convert",
    "--graphdef", frozen_pb,
    "--output", args.output,
    "--opset", str(args.opset),
    "--inputs", input_names[0],
    "--outputs", output_names[0],
], capture_output=True, text=True)

if result.returncode != 0:
    print("STDERR:")
    print(result.stderr[-2000:])
    raise RuntimeError("ONNX conversion failed")

# Show last few lines of tf2onnx output
stderr_lines = result.stderr.strip().split("\n")
for line in stderr_lines[-5:]:
    print(f"  {line}")

print(f"\n  ✓ Saved: {args.output} ({os.path.getsize(args.output) / 1e6:.1f} MB)")

# Step 5: Verify
import onnxruntime as ort
sess = ort.InferenceSession(args.output, providers=["CPUExecutionProvider"])
input_name = sess.get_inputs()[0].name
onnx_out = sess.run(None, {input_name: dummy})[0]
diff = np.abs(out.numpy() - onnx_out).max()
print(f"  ONNX vs Keras max diff: {diff:.6f}")
if diff < 1e-3:
    print("  ✓ Output matches!")
else:
    print("  ⚠ Large diff — check model integrity")
