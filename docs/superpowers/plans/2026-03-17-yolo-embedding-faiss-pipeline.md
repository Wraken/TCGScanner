# YOLO + Embedding + FAISS Pipeline Integration Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current TFLite classification pipeline in the Go app with a YOLO card-detection → ConvNeXt embedding → FAISS cosine-search pipeline, all CGO-free using purego.

**Architecture:** YOLO ONNX detects the card bbox in each webcam frame → crop is embedded by a ConvNeXt ONNX model → 512D vector is searched against a prebuilt FAISS index loaded via a purego wrapper. The existing TFLite path is kept untouched for backward compatibility. Both ONNX Runtime and FAISS are loaded via `purego.SyscallN` / `syscall.GetProcAddress` — identical pattern to the existing `pkg/tflite/`.

**Tech Stack:** Go 1.26 + purego v0.10, custom `pkg/ort/` (ORT C API via purego, no CGO), custom `pkg/faiss/` (FAISS C API via purego, no CGO), YOLOv8 ONNX (opset 12), Keras ConvNeXt ONNX (opset 17), Wails v2.

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `scripts/embeddingtest/verify_models.py` | Create | Verify/re-export ONNX models |
| `scripts/embeddingtest/export_embeddings_bin.py` | Create | Export flat float32 binary from FAISS index |
| `pkg/ort/ort.go` | Create | ONNX Runtime C API via purego (OrtApi struct traversal) |
| `pkg/ort/loader_windows.go` | Create | Load onnxruntime.dll via syscall |
| `pkg/ort/loader_linux.go` | Create | Load libonnxruntime.so via purego.Dlopen |
| `pkg/faiss/faiss.go` | Create | FAISS C API bindings via purego |
| `pkg/faiss/loader_windows.go` | Create | Load faiss.dll via syscall (not embedded — too large) |
| `pkg/faiss/loader_linux.go` | Create | Load libfaiss.so via purego.Dlopen |
| `pkg/inference/preprocess.go` | Create | YOLO (640×640 NCHW [0,1]) + ConvNeXt (224×224 NHWC ImageNet) preprocessing |
| `pkg/inference/yolo.go` | Create | Decode YOLO output [1,5,8400], NMS, return best bbox |
| `model_embedding.go` | Create | EmbeddingModel: YOLO → crop → embed → FAISS/cosine search |
| `app.go` | Modify | Add Predictor interface; change model field; update LoadModel |
| `model.go` | Modify | Extract `decodeBase64Image` to package-level helper |

**Not changing:** `pkg/tflite/`, `db.go`, `frontend/`.

---

## Task 1: Export YOLO to ONNX

- [ ] **Step 1: Export and copy YOLO**

```bash
cd scripts/embeddingtest
yolo export model=runs/detect/train2/weights/best.pt format=onnx imgsz=640 opset=12 simplify=True
cp runs/detect/train2/weights/best.onnx models/tcg/yolo.onnx
```

- [ ] **Step 2: Verify YOLO ONNX shapes**

```python
import onnxruntime as ort, numpy as np
sess = ort.InferenceSession("models/tcg/yolo.onnx", providers=["CPUExecutionProvider"])
print("inputs:", [(i.name, i.shape) for i in sess.get_inputs()])
print("outputs:", [(o.name, o.shape) for o in sess.get_outputs()])
# Expected: input 'images' [1,3,640,640], output 'output0' [1,5,8400]
# Note: raw cx,cy,w,h are in pixel space (0–640), not normalised 0–1
dummy = np.random.rand(1, 3, 640, 640).astype(np.float32)
out = sess.run(None, {"images": dummy})
print("output shape:", out[0].shape)  # (1, 5, 8400)
```

---

## Task 2: Verify & Re-export Embedding ONNX

**Files:**
- Create: `scripts/embeddingtest/verify_models.py`

The existing `models/tcg/embedding_model.onnx` (57 MB) may be broken. ConvNeXt GELU (`Erfc`) is supported natively in ONNX opset 17.

- [ ] **Step 1: Write verify/re-export script**

```python
# scripts/embeddingtest/verify_models.py
import os, sys
os.environ["TF_CPP_MIN_LOG_LEVEL"] = "3"
os.environ["TF_ENABLE_ONEDNN_OPTS"] = "0"

import onnxruntime as ort
import numpy as np

def verify_embedding(path="models/tcg/embedding_model.onnx"):
    try:
        sess = ort.InferenceSession(path, providers=["CPUExecutionProvider"])
        inp = sess.get_inputs()[0]
        print(f"OK  input: {inp.name} {inp.shape}")  # expect (None,224,224,3)
        dummy = np.random.rand(1, 224, 224, 3).astype(np.float32)
        out = sess.run(None, {inp.name: dummy})
        print(f"OK  output: {out[0].shape}")  # expect (1, 512)
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
    model = keras.saving.load_model(keras_path)
    # Find the embedding backbone (not the ArcFace training model)
    layer_names = [l.name for l in model.layers]
    print("Layers:", layer_names)
    embed_model = model.get_layer("embedding_model") if "embedding_model" in layer_names else model
    input_sig = [tf.TensorSpec([None, 224, 224, 3], tf.float32, name="input")]
    onnx_model, _ = tf2onnx.convert.from_keras(
        embed_model, input_signature=input_sig, opset=17)
    import onnx
    onnx.save(onnx_model, out_path)
    print(f"Saved → {out_path}")

if not verify_embedding():
    print("Re-exporting with opset=17...")
    reexport_embedding()
    verify_embedding()
```

- [ ] **Step 2: Run it**

```bash
cd scripts/embeddingtest
python verify_models.py
```

Expected:
```
OK  input: input (None, 224, 224, 3)
OK  output: (1, 512)
```

If re-export fails, inspect layer names (printed) and try `model` directly without `.get_layer(...)`.

---

## Task 3: Export Embeddings Binary (pure-Go fallback)

**Files:**
- Create: `scripts/embeddingtest/export_embeddings_bin.py`

- [ ] **Step 1: Write script**

```python
# scripts/embeddingtest/export_embeddings_bin.py
"""
Exports the FAISS index to flat binary files that Go can load without a FAISS dependency.
  models/<name>/embeddings.bin           — N×D float32, row-major, little-endian
  models/<name>/embeddings_labels.json   — ["card_id_0", "card_id_1", ...]
"""
import argparse, json
import numpy as np
import faiss

parser = argparse.ArgumentParser()
parser.add_argument("--name", default="tcg")
args = parser.parse_args()

index = faiss.read_index(f"models/{args.name}/index.faiss")
with open(f"models/{args.name}/index_labels.json") as f:
    labels = json.load(f)

n, d = index.ntotal, index.d
print(f"Index: {n} vectors × {d} dims")

vecs = np.zeros((n, d), dtype=np.float32)
for i in range(n):
    index.reconstruct(i, vecs[i])

norms = np.linalg.norm(vecs, axis=1)
print(f"Norm range: [{norms.min():.4f}, {norms.max():.4f}]  (should be ≈ 1.0)")

out_bin = f"models/{args.name}/embeddings.bin"
out_labels = f"models/{args.name}/embeddings_labels.json"
vecs.tofile(out_bin)
with open(out_labels, "w") as f:
    json.dump(labels, f)

print(f"Saved {out_bin}  ({n * d * 4 / 1024:.0f} KB)")
```

- [ ] **Step 2: Run it**

```bash
cd scripts/embeddingtest
python export_embeddings_bin.py --name tcg
# Expected: "Norm range: [0.99xx, 1.00xx]" + size line
```

---

## Task 4: `pkg/ort/` — ONNX Runtime purego wrapper

**Files:**
- Create: `pkg/ort/ort.go`
- Create: `pkg/ort/loader_windows.go`
- Create: `pkg/ort/loader_linux.go`

ONNX Runtime exports a single C function `OrtGetApiBase()` which returns a pointer to a struct of function pointers (`OrtApiBase`). Calling `OrtApiBase->GetApi(version)` returns `OrtApi*` — another struct of ~80 function pointers. We load all needed functions by their byte offset in this struct.

**Offsets are for ORT API version 16 (ORT >= 1.16.0, up to 1.19.x).** Each pointer is 8 bytes on 64-bit.

- [ ] **Step 1: Write `ort.go`**

```go
// pkg/ort/ort.go
// Package ort provides a CGo-free wrapper around the ONNX Runtime C API.
// It uses purego.SyscallN to call through the OrtApi struct function pointers.
// Requires onnxruntime.dll (Windows) or libonnxruntime.so (Linux) at runtime.
// These files are NOT embedded — place them next to the executable.
//
// Supported ORT versions: >= 1.16.0 (OrtApi version 16).
package ort

import (
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

// OrtApi struct offsets for API version 16 (ORT >= 1.16.0).
// Each function pointer is 8 bytes on 64-bit systems.
// Source: onnxruntime_c_api.h — entries listed in declaration order.
//
// IMPORTANT: verify all offsets for your ORT version by running:
//   python scripts/embeddingtest/dump_ort_offsets.py
// before first use. The indices below are believed correct for ORT 1.16–1.18
// but MUST be confirmed — wrong offsets call the wrong function silently.
const (
	offCreateStatus         = 0 * 8  // CreateStatus
	offGetErrorCode         = 1 * 8  // GetErrorCode
	offGetErrorMessage      = 2 * 8  // GetErrorMessage
	offReleaseStatus        = 3 * 8  // ReleaseStatus
	offCreateEnv            = 4 * 8  // CreateEnv
	// 5: CreateEnvWithCustomLogger
	// 6: EnableTelemetryEvents
	// 7: DisableTelemetryEvents
	offCreateSession        = 8 * 8  // CreateSession
	// 9: CreateSessionFromArray
	offRun                  = 10 * 8 // Run
	offCreateSessionOptions = 11 * 8 // CreateSessionOptions
	// 12: CloneSessionOptions  ← NOT ReleaseSessionOptions
	// 13–38: session option setters, custom ops, introspection functions
	offReleaseSession       = 39 * 8 // ReleaseSession  (ORT_CLASS_RELEASE(Session))
	offCreateTensorWithData = 40 * 8 // CreateTensorWithDataAsOrtValue
	// 41: IsTensor
	offGetTensorMutableData = 42 * 8 // GetTensorMutableData
	// 43–59: string tensors, type info, memory info
	offCreateCpuMemoryInfo  = 60 * 8 // CreateCpuMemoryInfo(device_type=0, mem_type=0, *out)
	// 61–82: memory info getters, allocators, kernel contexts
	offReleaseEnv           = 83 * 8 // ReleaseEnv        (ORT_CLASS_RELEASE(Env))
	// 84: ReleaseSessionOptions  — VERIFY with dump_ort_offsets.py
	// 85+: ReleaseValue, later additions
	//
	// These two are set at runtime after offset verification:
	offReleaseSessionOptions = 84 * 8 // TENTATIVE — run dump_ort_offsets.py to confirm
	offReleaseValue          = 85 * 8 // TENTATIVE — run dump_ort_offsets.py to confirm
)

const ortAPIVersion = 16

// raw function pointers loaded from OrtApi struct
var (
	fnCreateStatus          uintptr
	fnGetErrorMessage       uintptr
	fnReleaseStatus         uintptr
	fnCreateEnv             uintptr
	fnCreateSession         uintptr
	fnCreateSessionOptions  uintptr
	fnRun                   uintptr
	fnReleaseSession        uintptr
	fnReleaseSessionOptions uintptr
	fnReleaseEnv            uintptr
	fnCreateTensorWithData  uintptr
	fnGetTensorMutableData  uintptr
	fnReleaseValue          uintptr
	fnCreateCpuMemoryInfo   uintptr
	fnReleaseMemoryInfo     uintptr
)

func init() {
	lib := mustLoadLib()                      // platform-specific
	apiBase := resolveOrtGetApiBase(lib)       // *OrtApiBase via purego
	ortApi := callGetApi(apiBase)              // *OrtApi
	loadFunctions(ortApi)
}

// resolveOrtGetApiBase loads and calls OrtGetApiBase() from the shared library.
func resolveOrtGetApiBase(lib uintptr) uintptr {
	sym := mustSym(lib, "OrtGetApiBase")
	ret, _, _ := purego.SyscallN(sym)
	if ret == 0 {
		panic("ort: OrtGetApiBase returned nil")
	}
	return ret
}

// callGetApi calls OrtApiBase->GetApi(version) to obtain OrtApi*.
// OrtApiBase layout: [0] = GetApi fn ptr, [8] = GetVersionString fn ptr.
// GetApi signature: const OrtApi*(uint32_t version) — takes ONE argument.
func callGetApi(apiBase uintptr) uintptr {
	getApiFn := *(*uintptr)(unsafe.Pointer(apiBase)) // first field = GetApi fn ptr
	ret, _, _ := purego.SyscallN(getApiFn, ortAPIVersion)  // pass version only
	if ret == 0 {
		panic("ort: OrtApi->GetApi returned nil — check ORT version matches API version 16")
	}
	return ret
}

// loadFunctions reads function pointers from OrtApi struct by byte offset.
func loadFunctions(api uintptr) {
	load := func(off uintptr) uintptr {
		return *(*uintptr)(unsafe.Pointer(api + off))
	}
	fnCreateStatus = load(offCreateStatus)
	fnGetErrorMessage = load(offGetErrorMessage)
	fnReleaseStatus = load(offReleaseStatus)
	fnCreateEnv = load(offCreateEnv)
	fnCreateSession = load(offCreateSession)
	fnCreateSessionOptions = load(offCreateSessionOptions)
	fnRun = load(offRun)
	fnReleaseSession = load(offReleaseSession)
	fnReleaseSessionOptions = load(offReleaseSessionOptions)
	fnReleaseEnv = load(offReleaseEnv)
	fnCreateTensorWithData = load(offCreateTensorWithData)
	fnGetTensorMutableData = load(offGetTensorMutableData)
	fnReleaseValue = load(offReleaseValue)
	fnCreateCpuMemoryInfo = load(offCreateCpuMemoryInfo)
	// ReleaseMemoryInfo is typically just past ReleaseValue — verify with dump_ort_offsets.py
	fnReleaseMemoryInfo = load(offReleaseValue + 8) // TENTATIVE
}

// ── Public API ─────────────────────────────────────────────────────────────

// Env wraps OrtEnv*.
type Env struct{ ptr uintptr }

// NewEnv creates an ORT environment with WARNING log level.
func NewEnv(name string) (*Env, error) {
	nameBytes := cstring(name)
	var envPtr uintptr
	st, _, _ := purego.SyscallN(fnCreateEnv,
		2, // ORT_LOGGING_LEVEL_WARNING
		uintptr(unsafe.Pointer(&nameBytes[0])),
		uintptr(unsafe.Pointer(&envPtr)),
	)
	if err := ortStatus(st); err != nil {
		return nil, err
	}
	return &Env{ptr: envPtr}, nil
}

func (e *Env) Release() {
	if e.ptr != 0 {
		purego.SyscallN(fnReleaseEnv, e.ptr)
		e.ptr = 0
	}
}

// SessionOptions wraps OrtSessionOptions*.
type SessionOptions struct{ ptr uintptr }

func NewSessionOptions() (*SessionOptions, error) {
	var p uintptr
	st, _, _ := purego.SyscallN(fnCreateSessionOptions, uintptr(unsafe.Pointer(&p)))
	if err := ortStatus(st); err != nil {
		return nil, err
	}
	return &SessionOptions{ptr: p}, nil
}

func (o *SessionOptions) Release() {
	if o.ptr != 0 {
		purego.SyscallN(fnReleaseSessionOptions, o.ptr)
		o.ptr = 0
	}
}

// Session wraps OrtSession*.
type Session struct{ ptr uintptr }

// NewSession creates an ORT inference session from an ONNX file.
func NewSession(env *Env, modelPath string, opts *SessionOptions) (*Session, error) {
	pathBytes := cstring(modelPath)
	var sessPtr uintptr
	st, _, _ := purego.SyscallN(fnCreateSession,
		env.ptr,
		uintptr(unsafe.Pointer(&pathBytes[0])),
		opts.ptr,
		uintptr(unsafe.Pointer(&sessPtr)),
	)
	if err := ortStatus(st); err != nil {
		return nil, err
	}
	return &Session{ptr: sessPtr}, nil
}

func (s *Session) Release() {
	if s.ptr != 0 {
		purego.SyscallN(fnReleaseSession, s.ptr)
		s.ptr = 0
	}
}

// Value wraps OrtValue* (tensor).
type Value struct{ ptr uintptr }

// cpuMemInfo is the global CPU OrtMemoryInfo*, created once and reused.
// ORT requires a valid MemoryInfo — passing NULL causes a segfault.
var cpuMemInfo uintptr

func initCpuMemInfo() {
	if cpuMemInfo != 0 {
		return
	}
	// CreateCpuMemoryInfo(OrtDeviceAllocator=0, OrtMemTypeDefault=0, **out)
	st, _, _ := purego.SyscallN(fnCreateCpuMemoryInfo,
		0, 0, uintptr(unsafe.Pointer(&cpuMemInfo)))
	if st != 0 {
		panic("ort: CreateCpuMemoryInfo failed")
	}
}

// NewFloat32Tensor wraps an existing []float32 slice as an ORT CPU tensor.
// The slice must remain alive for the lifetime of the Value.
// shape is [dim0, dim1, ...].
func NewFloat32Tensor(data []float32, shape []int64) (*Value, error) {
	initCpuMemInfo() // ensures cpuMemInfo is valid before use
	var valPtr uintptr
	st, _, _ := purego.SyscallN(fnCreateTensorWithData,
		cpuMemInfo,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)*4), // byte count
		uintptr(unsafe.Pointer(&shape[0])),
		uintptr(len(shape)),
		1, // ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT = 1
		uintptr(unsafe.Pointer(&valPtr)),
	)
	if err := ortStatus(st); err != nil {
		return nil, err
	}
	return &Value{ptr: valPtr}, nil
}

// GetFloat32Data returns the tensor data as []float32 (zero-copy view into ORT memory).
func (v *Value) GetFloat32Data(n int) []float32 {
	var dataPtr uintptr
	purego.SyscallN(fnGetTensorMutableData, v.ptr, uintptr(unsafe.Pointer(&dataPtr)))
	return unsafe.Slice((*float32)(unsafe.Pointer(dataPtr)), n)
}

func (v *Value) Release() {
	if v.ptr != 0 {
		purego.SyscallN(fnReleaseValue, v.ptr)
		v.ptr = 0
	}
}

// Run executes inference.
// inputNames / outputNames are the model's I/O node names.
// inputs / outputs must be pre-allocated Value tensors of the correct shape.
func Run(sess *Session, env *Env,
	inputNames []string, inputs []*Value,
	outputNames []string, outputs []*Value) error {

	inPtrs := make([]uintptr, len(inputs))
	outPtrs := make([]uintptr, len(outputs))
	inNamePtrs := make([]uintptr, len(inputNames))
	outNamePtrs := make([]uintptr, len(outputNames))

	for i, v := range inputs {
		inPtrs[i] = v.ptr
	}
	for i, v := range outputs {
		outPtrs[i] = v.ptr
	}
	for i, s := range inputNames {
		b := cstring(s)
		inNamePtrs[i] = uintptr(unsafe.Pointer(&b[0]))
	}
	for i, s := range outputNames {
		b := cstring(s)
		outNamePtrs[i] = uintptr(unsafe.Pointer(&b[0]))
	}

	st, _, _ := purego.SyscallN(fnRun,
		sess.ptr,
		0, // run options — nil
		uintptr(unsafe.Pointer(&inNamePtrs[0])),
		uintptr(unsafe.Pointer(&inPtrs[0])),
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&outNamePtrs[0])),
		uintptr(len(outputs)),
		uintptr(unsafe.Pointer(&outPtrs[0])),
	)
	return ortStatus(st)
}

// ── internal helpers ────────────────────────────────────────────────────────

func cstring(s string) []byte {
	return append([]byte(s), 0)
}

func ortStatus(st uintptr) error {
	if st == 0 {
		return nil
	}
	// GetErrorMessage returns const char* as the direct return value (not via pointer arg).
	msgPtr, _, _ := purego.SyscallN(fnGetErrorMessage, st)
	msg := "ort error"
	if msgPtr != 0 {
		msg = goString(msgPtr)
	}
	purego.SyscallN(fnReleaseStatus, st)
	return fmt.Errorf("ort: %s", msg)
}

func goString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	var n int
	for *(*byte)(unsafe.Pointer(ptr + uintptr(n))) != 0 {
		n++
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = *(*byte)(unsafe.Pointer(ptr + uintptr(i)))
	}
	return string(b)
}

// mustSym is implemented per-platform (see loader_*.go).
// On Windows it uses syscall.GetProcAddress; on Linux purego.Dlsym.
```

- [ ] **Step 2: Write `loader_windows.go`**

```go
//go:build windows

package ort

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func mustLoadLib() uintptr {
	exe, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "onnxruntime.dll"),
		filepath.Join(".", "onnxruntime.dll"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			h, err := syscall.LoadLibrary(p)
			if err == nil {
				return uintptr(h)
			}
		}
	}
	panic(fmt.Sprintf("ort: onnxruntime.dll not found next to %q", exe))
}

func mustSym(lib uintptr, name string) uintptr {
	proc, err := syscall.GetProcAddress(syscall.Handle(lib), name)
	if err != nil {
		panic("ort: symbol not found: " + name)
	}
	return proc
}
```

- [ ] **Step 3: Write `loader_linux.go`**

```go
//go:build linux

package ort

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ebitengine/purego"
)

func mustLoadLib() uintptr {
	exe, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "libonnxruntime.so"),
		filepath.Join(".", "libonnxruntime.so"),
		"libonnxruntime.so",
	}
	for _, p := range candidates {
		lib, err := purego.Dlopen(p, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return lib
		}
	}
	panic(fmt.Sprintf("ort: libonnxruntime.so not found next to %q", exe))
}

func mustSym(lib uintptr, name string) uintptr {
	sym, err := purego.Dlsym(lib, name)
	if err != nil {
		panic("ort: symbol not found: " + name)
	}
	return sym
}
```

- [ ] **Step 4: Verify offset constants**

Before using, run this Python helper to confirm offsets match your ORT version:

```python
# scripts/embeddingtest/dump_ort_offsets.py
"""Print byte offsets of key OrtApi members for your installed ORT version."""
import onnxruntime as ort
import ctypes, os, sys

# Load the DLL
if sys.platform == "win32":
    dll_path = os.path.join(os.path.dirname(ort.__file__), "capi", "onnxruntime.dll")
    lib = ctypes.CDLL(dll_path)
else:
    lib = ctypes.CDLL("libonnxruntime.so")

OrtGetApiBase = lib.OrtGetApiBase
OrtGetApiBase.restype = ctypes.c_void_p

api_base = OrtGetApiBase()
# GetApi is at offset 0 in OrtApiBase (first function pointer)
GetApi = ctypes.cast(ctypes.c_void_p.from_address(api_base), ctypes.CFUNCTYPE(ctypes.c_void_p, ctypes.c_uint32))
api = GetApi(16)  # request API version 16

# Print the first 100 function pointer values at each offset
print(f"OrtApi* = {api:#x}")
for i in range(90):
    fp = ctypes.c_void_p.from_address(api + i * 8).value
    print(f"  [{i:2d}] offset={i*8:4d}  fp={fp:#018x}")
```

Compare the non-null addresses against the constants in `ort.go`. Indices to verify: 4=CreateEnv, 8=CreateSession, 10=Run, 11=CreateSessionOptions, 39=ReleaseSession, 40=CreateTensorWithData, 42=GetTensorMutableData, 83=ReleaseEnv.

- [ ] **Step 5: Compile check**

```bash
cd e:/TCGScanner
go build ./pkg/ort/
```

---

## Task 5: `pkg/faiss/` — FAISS purego wrapper

**Files:**
- Create: `pkg/faiss/faiss.go`
- Create: `pkg/faiss/loader_windows.go`
- Create: `pkg/faiss/loader_linux.go`

FAISS C API (`faiss/c_api/`) exports flat C functions — no struct traversal needed.

- [ ] **Step 1: Write `faiss.go`**

```go
// pkg/faiss/faiss.go
// Package faiss provides a CGo-free wrapper around the FAISS C API flat functions.
// Requires faiss.dll (Windows) or libfaiss.so (Linux) at runtime.
// These are NOT embedded — place them next to the executable.
package faiss

import (
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

// fnSearch / fnFree / fnReadIndex are loaded per-platform (see loader_*.go).
// mustSym is also platform-specific.
var (
	fnReadIndexFname uintptr // int faiss_read_index_fname(const char*, int, FaissIndex**)
	fnIndexSearch    uintptr // int faiss_Index_search(index, n, *x, k, *dist, *labels)
	fnIndexFree      uintptr // void faiss_Index_free(FaissIndex*)
	fnIndexD         uintptr // int faiss_Index_d(const FaissIndex*) — returns int directly
	fnIndexNtotal    uintptr // idx_t faiss_Index_ntotal(const FaissIndex*) — returns int64 directly
)

func init() {
	lib := mustLoadLib()
	fnReadIndexFname = mustSym(lib, "faiss_read_index_fname")
	fnIndexSearch = mustSym(lib, "faiss_Index_search")
	fnIndexFree = mustSym(lib, "faiss_Index_free")
	fnIndexD = mustSym(lib, "faiss_Index_d")
	fnIndexNtotal = mustSym(lib, "faiss_Index_ntotal")
}

// Index is a loaded FAISS index.
type Index struct {
	ptr uintptr
	D   int // embedding dimension
	N   int // number of indexed vectors
}

// Open loads a FAISS index from disk.
func Open(path string) (*Index, error) {
	pathBytes := append([]byte(path), 0)
	var ptr uintptr
	ret, _, _ := purego.SyscallN(fnReadIndexFname,
		uintptr(unsafe.Pointer(&pathBytes[0])),
		0, // io_flags = 0 (read-only)
		uintptr(unsafe.Pointer(&ptr)),
	)
	if int32(ret) != 0 || ptr == 0 {
		return nil, fmt.Errorf("faiss: failed to open %q (ret=%d)", path, ret)
	}

	// faiss_Index_d and faiss_Index_ntotal return their values directly (not via pointer)
	dRet, _, _ := purego.SyscallN(fnIndexD, ptr)
	nRet, _, _ := purego.SyscallN(fnIndexNtotal, ptr)

	return &Index{ptr: ptr, D: int(int32(dRet)), N: int(int64(nRet))}, nil
}

// Search finds the k nearest vectors to query.
// query must be L2-normalised, length == idx.D.
// Returns (distances, labels) each of length k.
func (idx *Index) Search(query []float32, k int) ([]float32, []int64, error) {
	if len(query) != idx.D {
		return nil, nil, fmt.Errorf("faiss: query dim %d != index dim %d", len(query), idx.D)
	}
	distances := make([]float32, k)
	labels := make([]int64, k)

	ret, _, _ := purego.SyscallN(fnIndexSearch,
		idx.ptr,
		uintptr(1),   // n = 1 query
		uintptr(unsafe.Pointer(&query[0])),
		uintptr(k),
		uintptr(unsafe.Pointer(&distances[0])),
		uintptr(unsafe.Pointer(&labels[0])),
	)
	if int32(ret) != 0 {
		return nil, nil, fmt.Errorf("faiss: search failed (ret=%d)", ret)
	}
	return distances, labels, nil
}

// Free releases the FAISS index memory.
func (idx *Index) Free() {
	if idx.ptr != 0 {
		purego.SyscallN(fnIndexFree, idx.ptr)
		idx.ptr = 0
	}
}
```

- [ ] **Step 2: Write `loader_windows.go`**

```go
//go:build windows

package faiss

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func mustLoadLib() uintptr {
	exe, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "faiss.dll"),
		filepath.Join(".", "faiss.dll"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			h, err := syscall.LoadLibrary(p)
			if err == nil {
				return uintptr(h)
			}
		}
	}
	panic(fmt.Sprintf("faiss: faiss.dll not found next to %q", exe))
}

func mustSym(lib uintptr, name string) uintptr {
	proc, err := syscall.GetProcAddress(syscall.Handle(lib), name)
	if err != nil {
		panic("faiss: symbol not found: " + name)
	}
	return proc
}
```

- [ ] **Step 3: Write `loader_linux.go`**

```go
//go:build linux

package faiss

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ebitengine/purego"
)

func mustLoadLib() uintptr {
	exe, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "libfaiss.so"),
		filepath.Join(".", "libfaiss.so"),
		"libfaiss.so",
	}
	for _, p := range candidates {
		lib, err := purego.Dlopen(p, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return lib
		}
	}
	panic(fmt.Sprintf("faiss: libfaiss.so not found next to %q", exe))
}

func mustSym(lib uintptr, name string) uintptr {
	sym, err := purego.Dlsym(lib, name)
	if err != nil {
		panic("faiss: symbol not found: " + name)
	}
	return sym
}
```

> **DLL acquisition:**
> - Windows: `conda install -c pytorch faiss-cpu` → `%CONDA_PREFIX%\Library\bin\faiss.dll`
> - Linux: `apt install libfaiss-dev` or conda.
> - Fallback: if FAISS DLL is unavailable, the pure-Go cosine search in `model_embedding.go` works for up to ~50K cards without any native library.

- [ ] **Step 4: Compile check**

```bash
go build ./pkg/faiss/
```

---

## Task 6: `pkg/inference/preprocess.go`

**Files:**
- Create: `pkg/inference/preprocess.go`

- [ ] **Step 1: Write preprocessing**

```go
// pkg/inference/preprocess.go
package inference

import (
	"image"

	"golang.org/x/image/draw"
)

// PreprocessYOLO resizes img to 640×640 and returns a flat float32 slice
// in NCHW format [3×640×640] normalised to [0, 1].
// Prepend batch dim [1,3,640,640] when creating the ORT tensor.
func PreprocessYOLO(img image.Image) []float32 {
	const size = 640
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

	out := make([]float32, 3*size*size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			base := y*dst.Stride + x*4
			idx := y*size + x
			out[0*size*size+idx] = float32(dst.Pix[base+0]) / 255.0 // R channel
			out[1*size*size+idx] = float32(dst.Pix[base+1]) / 255.0 // G channel
			out[2*size*size+idx] = float32(dst.Pix[base+2]) / 255.0 // B channel
		}
	}
	return out
}

// imagenetMean and imagenetStd are used by keras.applications.convnext.preprocess_input
// with mode='torch' (ImageNet statistics, input in [0,255]).
var (
	imagenetMean = [3]float32{0.485, 0.456, 0.406}
	imagenetStd  = [3]float32{0.229, 0.224, 0.225}
)

// PreprocessConvNeXt resizes img to 224×224 and returns a flat float32 slice
// in NHWC format [224×224×3] with ImageNet normalisation.
// This exactly matches keras.applications.convnext.preprocess_input (mode='torch').
func PreprocessConvNeXt(img image.Image) []float32 {
	const size = 224
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

	out := make([]float32, size*size*3)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			base := y*dst.Stride + x*4
			idx := (y*size + x) * 3
			out[idx+0] = (float32(dst.Pix[base+0])/255.0 - imagenetMean[0]) / imagenetStd[0]
			out[idx+1] = (float32(dst.Pix[base+1])/255.0 - imagenetMean[1]) / imagenetStd[1]
			out[idx+2] = (float32(dst.Pix[base+2])/255.0 - imagenetMean[2]) / imagenetStd[2]
		}
	}
	return out
}

// CropImage crops img to rectangle r (clamped to image bounds).
func CropImage(img image.Image, r image.Rectangle) image.Image {
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return img
	}
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := img.(subImager); ok {
		return si.SubImage(r)
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Src.Draw(dst, dst.Bounds(), img, r.Min)
	return dst
}
```

- [ ] **Step 2: Compile check**

```bash
go build ./pkg/inference/
```

---

## Task 7: `pkg/inference/yolo.go` — YOLO decode + NMS

**Files:**
- Create: `pkg/inference/yolo.go`

YOLOv8 ONNX (1 class) output: `[1, 5, 8400]`. Coordinates `cx, cy, w, h` are in pixel space relative to the 640×640 input — **not normalised 0–1**. Scale back to original image by dividing by 640.

- [ ] **Step 1: Write decoder**

```go
// pkg/inference/yolo.go
package inference

import "image"

const yoloInputSize = 640

// Detection is a detected card bounding box in the original image coordinate space.
type Detection struct {
	Box  image.Rectangle
	Conf float32
}

// DecodeYOLO decodes raw YOLO output tensor [1, 5, 8400] into detections.
// imgW, imgH are the original image dimensions (for scaling from 640-pixel space).
// confThresh filters predictions before NMS.
func DecodeYOLO(output []float32, imgW, imgH int, confThresh float32) []Detection {
	// output is stored as [5, 8400] after stripping the batch dim:
	//   row 0: cx (pixels in 640 space)
	//   row 1: cy
	//   row 2: w
	//   row 3: h
	//   row 4: confidence (for 1-class model)
	const numAnchors = 8400
	numChannels := len(output) / numAnchors

	scaleX := float32(imgW) / yoloInputSize
	scaleY := float32(imgH) / yoloInputSize

	type raw struct{ cx, cy, w, h, conf float32 }
	var candidates []raw
	for i := 0; i < numAnchors; i++ {
		conf := output[(numChannels-1)*numAnchors+i]
		if conf < confThresh {
			continue
		}
		candidates = append(candidates, raw{
			cx:   output[0*numAnchors+i],
			cy:   output[1*numAnchors+i],
			w:    output[2*numAnchors+i],
			h:    output[3*numAnchors+i],
			conf: conf,
		})
	}
	if len(candidates) == 0 {
		return nil
	}

	dets := make([]Detection, len(candidates))
	for i, c := range candidates {
		x1 := int((c.cx - c.w/2) * scaleX)
		y1 := int((c.cy - c.h/2) * scaleY)
		x2 := int((c.cx + c.w/2) * scaleX)
		y2 := int((c.cy + c.h/2) * scaleY)
		dets[i] = Detection{
			Box:  image.Rect(x1, y1, x2, y2),
			Conf: c.conf,
		}
	}
	return nms(dets, 0.5)
}

func nms(dets []Detection, iouThresh float32) []Detection {
	for i := 1; i < len(dets); i++ {
		for j := i; j > 0 && dets[j].Conf > dets[j-1].Conf; j-- {
			dets[j], dets[j-1] = dets[j-1], dets[j]
		}
	}
	kept := make([]Detection, 0, len(dets))
	suppressed := make([]bool, len(dets))
	for i := range dets {
		if suppressed[i] {
			continue
		}
		kept = append(kept, dets[i])
		for j := i + 1; j < len(dets); j++ {
			if iou(dets[i].Box, dets[j].Box) > iouThresh {
				suppressed[j] = true
			}
		}
	}
	return kept
}

func iou(a, b image.Rectangle) float32 {
	inter := a.Intersect(b)
	if inter.Empty() {
		return 0
	}
	areaI := float32(inter.Dx() * inter.Dy())
	return areaI / (float32(a.Dx()*a.Dy()) + float32(b.Dx()*b.Dy()) - areaI)
}
```

- [ ] **Step 2: Compile check**

```bash
go build ./pkg/inference/
```

---

## Task 8: Extract `decodeBase64Image` in `model.go`

**Files:**
- Modify: `model.go`

This must happen **before** writing `model_embedding.go` to avoid a duplicate symbol.

- [ ] **Step 1: Extract the helper in `model.go`**

In `model.go`, the `Predict` method currently inlines the base64+image decode. Extract it:

```go
// decodeBase64Image decodes a base64-encoded JPEG/PNG webcam frame.
// Shared between Model (TFLite) and EmbeddingModel.
func decodeBase64Image(base64Frame string) (image.Image, error) {
	imgData, err := base64.StdEncoding.DecodeString(base64Frame)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil, fmt.Errorf("image decode: %w", err)
	}
	return img, nil
}
```

Update `Model.Predict` to call `decodeBase64Image` instead of inlining.

- [ ] **Step 2: Compile check**

```bash
go build .
```

---

## Task 9: `model_embedding.go` — EmbeddingModel

**Files:**
- Create: `model_embedding.go`

- [ ] **Step 1: Write EmbeddingModel**

```go
// model_embedding.go
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"

	"TCGScanner/pkg/faiss"
	"TCGScanner/pkg/inference"
	"TCGScanner/pkg/ort"
)

// EmbeddingModel runs the YOLO → embed → FAISS/cosine pipeline.
type EmbeddingModel struct {
	name string

	// ORT state
	env         *ort.Env
	sessOpts    *ort.SessionOptions
	yoloSess    *ort.Session
	embedSess   *ort.Session

	// Pre-allocated tensors (reused across calls)
	yoloIn   *ort.Value // [1, 3, 640, 640]
	yoloOut  *ort.Value // [1, 5, 8400]
	yoloInBuf  []float32
	yoloOutBuf []float32
	embedIn    *ort.Value // [1, 224, 224, 3]
	embedOut   *ort.Value // [1, 512]
	embedInBuf  []float32
	embedOutBuf []float32

	// Search backend
	faissIdx    *faiss.Index
	faissLabels []string
	useFAISS    bool

	// Pure-Go fallback
	embedVecs   []float32
	embedLabels []string
	embedDim    int
}

// NewEmbeddingModel loads all assets from dir (models/<name>/).
// Expected files: yolo.onnx, embedding_model.onnx,
//   index.faiss + index_labels.json (FAISS)
//   OR embeddings.bin + embeddings_labels.json (pure-Go fallback).
func NewEmbeddingModel(name, dir string) (*EmbeddingModel, error) {
	env, err := ort.NewEnv("tcgscanner")
	if err != nil {
		return nil, fmt.Errorf("ort env: %w", err)
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		env.Release()
		return nil, fmt.Errorf("ort opts: %w", err)
	}

	yoloSess, err := ort.NewSession(env, filepath.Join(dir, "yolo.onnx"), opts)
	if err != nil {
		opts.Release(); env.Release()
		return nil, fmt.Errorf("yolo session: %w", err)
	}
	embedSess, err := ort.NewSession(env, filepath.Join(dir, "embedding_model.onnx"), opts)
	if err != nil {
		yoloSess.Release(); opts.Release(); env.Release()
		return nil, fmt.Errorf("embed session: %w", err)
	}

	// Pre-allocate input/output buffers and ORT tensors
	yoloInBuf := make([]float32, 1*3*640*640)
	yoloOutBuf := make([]float32, 1*5*8400)
	embedInBuf := make([]float32, 1*224*224*3)
	embedOutBuf := make([]float32, 1*512)

	yoloIn, _ := ort.NewFloat32Tensor(yoloInBuf, []int64{1, 3, 640, 640})
	yoloOut, _ := ort.NewFloat32Tensor(yoloOutBuf, []int64{1, 5, 8400})
	embedIn, _ := ort.NewFloat32Tensor(embedInBuf, []int64{1, 224, 224, 3})
	embedOut, _ := ort.NewFloat32Tensor(embedOutBuf, []int64{1, 512})

	m := &EmbeddingModel{
		name: name, env: env, sessOpts: opts,
		yoloSess: yoloSess, embedSess: embedSess,
		yoloIn: yoloIn, yoloOut: yoloOut,
		yoloInBuf: yoloInBuf, yoloOutBuf: yoloOutBuf,
		embedIn: embedIn, embedOut: embedOut,
		embedInBuf: embedInBuf, embedOutBuf: embedOutBuf,
	}

	// Prefer FAISS index; fall back to pure-Go cosine search
	faissPath := filepath.Join(dir, "index.faiss")
	labelsPath := filepath.Join(dir, "index_labels.json")
	if idx, err := faiss.Open(faissPath); err == nil {
		m.faissIdx = idx
		m.useFAISS = true
		raw, _ := os.ReadFile(labelsPath)
		json.Unmarshal(raw, &m.faissLabels)
		fmt.Printf("EmbeddingModel %q: FAISS index (%d cards)\n", name, idx.N)
	} else {
		binPath := filepath.Join(dir, "embeddings.bin")
		binLabelsPath := filepath.Join(dir, "embeddings_labels.json")
		vecs, lbls, dim, err2 := loadEmbeddingsBin(binPath, binLabelsPath)
		if err2 != nil {
			m.Delete()
			return nil, fmt.Errorf("no FAISS index and no embeddings.bin in %q", dir)
		}
		m.embedVecs, m.embedLabels, m.embedDim = vecs, lbls, dim
		fmt.Printf("EmbeddingModel %q: pure-Go search (%d cards × %d dims)\n", name, len(lbls), dim)
	}

	return m, nil
}

func (m *EmbeddingModel) Name() string { return m.name }

func (m *EmbeddingModel) Delete() {
	if m.yoloIn != nil { m.yoloIn.Release() }
	if m.yoloOut != nil { m.yoloOut.Release() }
	if m.embedIn != nil { m.embedIn.Release() }
	if m.embedOut != nil { m.embedOut.Release() }
	if m.yoloSess != nil { m.yoloSess.Release() }
	if m.embedSess != nil { m.embedSess.Release() }
	if m.sessOpts != nil { m.sessOpts.Release() }
	if m.env != nil { m.env.Release() }
	if m.faissIdx != nil { m.faissIdx.Free() }
}

func (m *EmbeddingModel) Predict(base64Frame string) (*Prediction, error) {
	img, err := decodeBase64Image(base64Frame) // defined in model.go
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()

	// ── YOLO ──────────────────────────────────────────────────────────────────
	copy(m.yoloInBuf, inference.PreprocessYOLO(img))
	if err := ort.Run(m.yoloSess, m.env,
		[]string{"images"}, []*ort.Value{m.yoloIn},
		[]string{"output0"}, []*ort.Value{m.yoloOut}); err != nil {
		return nil, fmt.Errorf("yolo: %w", err)
	}

	// yoloOut shares memory with yoloOutBuf; DecodeYOLO reads yoloOutBuf
	dets := inference.DecodeYOLO(m.yoloOutBuf, bounds.Dx(), bounds.Dy(), 0.4)

	var crop image.Image
	if len(dets) > 0 {
		crop = inference.CropImage(img, dets[0].Box)
	} else {
		crop = img // graceful fallback: embed full frame
	}

	// ── Embed ─────────────────────────────────────────────────────────────────
	copy(m.embedInBuf, inference.PreprocessConvNeXt(crop))
	if err := ort.Run(m.embedSess, m.env,
		[]string{"input"}, []*ort.Value{m.embedIn},
		[]string{"output"}, []*ort.Value{m.embedOut}); err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}

	// embedOutBuf is the live tensor data — copy before normalising
	embedding := make([]float32, 512)
	copy(embedding, m.embedOutBuf)
	l2Normalize(embedding)

	// ── Search ────────────────────────────────────────────────────────────────
	if m.useFAISS {
		dists, labels, err := m.faissIdx.Search(embedding, 1)
		if err != nil {
			return nil, err
		}
		if labels[0] < 0 || int(labels[0]) >= len(m.faissLabels) {
			return &Prediction{CardID: "unknown", Confidence: 0}, nil
		}
		return &Prediction{
			CardID:     m.faissLabels[labels[0]],
			Confidence: float64(dists[0]),
		}, nil
	}
	cardID, score := cosineSearch(embedding, m.embedVecs, m.embedLabels, m.embedDim)
	return &Prediction{CardID: cardID, Confidence: float64(score)}, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func l2Normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

func cosineSearch(query, vecs []float32, labels []string, dim int) (string, float32) {
	best, bestScore := 0, float32(-1)
	for i := 0; i < len(labels); i++ {
		ref := vecs[i*dim : (i+1)*dim]
		var s float32
		for j := range query {
			s += query[j] * ref[j]
		}
		if s > bestScore {
			bestScore = s
			best = i
		}
	}
	return labels[best], bestScore
}

func loadEmbeddingsBin(binPath, labelsPath string) (vecs []float32, labels []string, dim int, err error) {
	data, err := os.ReadFile(binPath)
	if err != nil {
		return nil, nil, 0, err
	}
	raw, err := os.ReadFile(labelsPath)
	if err != nil {
		return nil, nil, 0, err
	}
	if err := json.Unmarshal(raw, &labels); err != nil {
		return nil, nil, 0, err
	}
	n := len(labels)
	total := len(data) / 4
	if n == 0 || total%n != 0 {
		return nil, nil, 0, fmt.Errorf("size mismatch: %d bytes, %d labels", len(data), n)
	}
	dim = total / n
	vecs = make([]float32, total)
	if err := binary.Read(bytes.NewReader(data), binary.LittleEndian, vecs); err != nil {
		return nil, nil, 0, err
	}
	return vecs, labels, dim, nil
}
```

- [ ] **Step 2: Compile check**

```bash
go build .
# Fix import errors if any
```

---

## Task 10: Update `app.go` — Predictor interface + LoadModel

**Files:**
- Modify: `app.go`

- [ ] **Step 1: Add Predictor interface and update App struct**

Add near the top of `app.go`:
```go
// Predictor is implemented by both Model (TFLite) and EmbeddingModel (ONNX+FAISS).
type Predictor interface {
	Predict(base64Frame string) (*Prediction, error)
	Name() string
	Delete()
}
```

Change `App.model` from `*Model` to `Predictor`.

> Note: `Model` (TFLite) needs a `Name() string` method added:
> ```go
> func (m *Model) Name() string { return m.name }
> ```

- [ ] **Step 2: Add `Name()` to `model.go`**

```go
func (m *Model) Name() string { return m.name }
```

- [ ] **Step 3: Update `LoadModel` in `app.go`**

```go
func (a *App) LoadModel(modelName string) error {
	dir := filepath.Join("./models", modelName)

	var newModel Predictor
	var err error

	yoloPath := filepath.Join(dir, "yolo.onnx")
	embedPath := filepath.Join(dir, "embedding_model.onnx")

	if fileExists(yoloPath) && fileExists(embedPath) {
		newModel, err = NewEmbeddingModel(modelName, dir)
	} else {
		modelPath := filepath.Join(dir, "model.tflite")
		labelsPath := filepath.Join(dir, "labels.json")
		newModel, err = NewModel(modelName, modelPath, labelsPath)
	}
	if err != nil {
		return err
	}

	// Swap under lock (keep critical section short — no I/O inside lock).
	a.modelMu.Lock()
	old := a.model
	a.model = newModel
	a.modelMu.Unlock()

	// Delete old model outside the lock (Delete() may be slow).
	if old != nil {
		old.Delete()
	}

	runtime.EventsEmit(a.ctx, "modelLoaded", modelName)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
```

- [ ] **Step 4: Fix `GetCardImage` and `SaveCard`**

These methods reference `a.model.name`. After the interface change, use `a.model.Name()`:

```go
// In GetCardImage:
return loadCardImage(cardID, a.model.Name())

// In SaveCard (wherever model name is needed):
modelName := a.model.Name()
```

- [ ] **Step 5: Final build**

```bash
cd e:/TCGScanner
go build ./...
# Must succeed with no errors before moving on
```

---

## Task 11: Runtime DLLs — acquire and place

- [ ] **Step 1: ONNX Runtime (Windows)**

Download [onnxruntime-win-x64-1.17.3.zip](https://github.com/microsoft/onnxruntime/releases/tag/v1.17.3) (CPU variant, ~15 MB). Extract `onnxruntime.dll` and place next to `TCGScanner.exe` (or in the project root during `wails dev`).

- [ ] **Step 2: ONNX Runtime (Linux)**

```bash
pip install onnxruntime
find ~/.local -name "libonnxruntime.so*" 2>/dev/null
# Copy found .so next to binary
```

- [ ] **Step 3: FAISS (Windows)**

```bash
# If faiss-cpu was installed via conda for build_index.py:
conda run -n your_env python -c "import faiss; print(faiss.__file__)"
# DLL is in the same dir or in %CONDA_PREFIX%\Library\bin\faiss.dll
```

Copy `faiss.dll` next to `TCGScanner.exe`.

- [ ] **Step 4: FAISS (Linux)**

```bash
sudo apt install libfaiss-dev   # Ubuntu 22.04+
# or: conda install -c pytorch faiss-cpu
```

---

## Task 12: End-to-end smoke test

- [ ] **Step 1: Run in dev mode**

```bash
wails dev
```

In the UI: select a model that has `yolo.onnx` + `embedding_model.onnx`. Point camera at a Riftbound card. Verify prediction appears.

- [ ] **Step 2: Verify ORT struct offsets are correct**

If `ort.NewEnv` panics with "OrtApi returned nil" or produces garbage output, run `dump_ort_offsets.py` (Task 4 Step 4) and compare against the constants in `pkg/ort/ort.go`.

- [ ] **Step 3: Check YOLO is detecting (not full-frame fallback)**

Add a temporary log in `Predict`:
```go
if len(dets) > 0 {
    fmt.Printf("YOLO: bbox %v conf=%.2f\n", dets[0].Box, dets[0].Conf)
} else {
    fmt.Println("YOLO: no detection, using full frame")
}
```

---

## Summary: Model Directory Layout

```
models/tcg/
  yolo.onnx                  ← Task 1
  embedding_model.onnx       ← Task 2
  embedding_model.keras      ← training only
  index.faiss                ← existing (used by pkg/faiss)
  index_labels.json          ← existing
  embeddings.bin             ← Task 3 (pure-Go fallback)
  embeddings_labels.json     ← Task 3
  config.json
  images/

# Next to TCGScanner.exe:
onnxruntime.dll   ← Task 11
faiss.dll         ← Task 11 (optional; falls back to pure-Go search)
```

## Fallback Decision Tree

```
YOLO detected card?
  YES → embed crop
  NO  → embed full frame

FAISS DLL available?
  YES → pkg/faiss Search()
  NO  → pure-Go cosineSearch() (always works, no DLL needed)

embedding_model.onnx valid?
  YES → proceed
  NO  → re-export with opset=17 (Task 2)
```
