// Package ort provides a CGo-free wrapper around the ONNX Runtime C API.
// Uses purego.SyscallN to call through the OrtApi struct function pointers.
//
// Offsets verified against onnxruntime_c_api.h, ORT 1.24 (ORT_API_VERSION=24).
// Source: https://github.com/microsoft/onnxruntime/blob/main/include/onnxruntime/core/session/onnxruntime_c_api.h
package ort

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

const ortAPIVersion = 24

// Byte offsets into the OrtApi struct. Each function pointer is 8 bytes on 64-bit.
// Index = position in struct declaration order (0-based). Offset = index × 8.
const (
	offGetErrorMessage        = 2 * 8
	offCreateEnv              = 3 * 8
	offCreateSession          = 7 * 8
	offRun                    = 9 * 8
	offCreateSessionOptions   = 10 * 8
	offSetIntraOpNumThreads   = 24 * 8
	offCreateTensorWithData   = 49 * 8
	offGetTensorMutableData   = 51 * 8
	offGetDimensionsCount     = 61 * 8
	offGetDimensions          = 62 * 8
	offGetTensorTypeAndShape  = 65 * 8
	offCreateCpuMemoryInfo    = 69 * 8
	offReleaseEnv             = 92 * 8
	offReleaseStatus          = 93 * 8
	offReleaseMemoryInfo      = 94 * 8
	offReleaseSession         = 95 * 8
	offReleaseValue           = 96 * 8
	offReleaseTensorTypeShape = 99 * 8
	offReleaseSessionOptions  = 100 * 8
)

var (
	ortApi    uintptr // *OrtApi — set by platform loader via mustInit()
	globalEnv uintptr // *OrtEnv — one per process
	cpuMem    uintptr // *OrtMemoryInfo for CPU — shared across sessions
)

// mustInit is called by the platform-specific loader after loading the shared library.
func mustInit(lib uintptr) {
	// Load OrtGetApiBase from the DLL — the only exported function we need.
	var getApiBase func() uintptr
	purego.RegisterLibFunc(&getApiBase, lib, "OrtGetApiBase")

	apiBase := getApiBase()
	if apiBase == 0 {
		panic("ort: OrtGetApiBase returned nil")
	}

	// OrtApiBase struct: [0]=GetApi fn ptr, [1]=GetVersionString fn ptr.
	// Call GetApi(version) to get the full OrtApi struct pointer.
	getApiFn := *(*uintptr)(unsafe.Pointer(apiBase))
	api, _, _ := purego.SyscallN(getApiFn, uintptr(ortAPIVersion))
	if api == 0 {
		panic("ort: GetApi(24) returned nil — unsupported ORT version")
	}
	ortApi = api

	// CreateEnv(OrtLoggingLevel=3 (WARNING), logid, **OrtEnv)
	var envPtr uintptr
	logid := []byte("tcg\x00")
	mustOK(offCreateEnv,
		uintptr(3),
		uintptr(unsafe.Pointer(&logid[0])),
		uintptr(unsafe.Pointer(&envPtr)),
	)
	runtime.KeepAlive(logid)
	globalEnv = envPtr

	// CreateCpuMemoryInfo(OrtDeviceAllocator=0, OrtMemTypeDefault=0, **OrtMemoryInfo)
	var memPtr uintptr
	mustOK(offCreateCpuMemoryInfo, 0, 0, uintptr(unsafe.Pointer(&memPtr)))
	cpuMem = memPtr
}

// fp returns the function pointer at the given byte offset within OrtApi.
func fp(off uintptr) uintptr {
	return *(*uintptr)(unsafe.Pointer(ortApi + off))
}

// call invokes OrtApi[off] and returns the OrtStatus* result.
// call invokes the OrtApi entry at byte offset off.
//
// The uintptrescapes directive matters: without it, a uintptr(unsafe.Pointer(&x))
// written in a call(...) argument list is just an integer to the compiler — x
// may live on the goroutine stack, and a stack move (growth, or shrink at GC)
// would leave ORT writing through a stale address. With it, such arguments are
// heap-allocated and kept alive for the duration of the call, the same
// guarantee purego.SyscallN provides. The conversion must still appear inside
// the call(...) expression itself.
//
//go:uintptrescapes
func call(off uintptr, args ...uintptr) uintptr {
	r, _, _ := purego.SyscallN(fp(off), args...)
	return r
}

// checkStatus converts a non-nil OrtStatus* to a Go error and releases it.
func checkStatus(status uintptr) error {
	if status == 0 {
		return nil
	}
	msgPtr, _, _ := purego.SyscallN(fp(offGetErrorMessage), status)
	msg := goString(msgPtr)
	purego.SyscallN(fp(offReleaseStatus), status)
	return fmt.Errorf("ort: %s", msg)
}

// mustOK calls OrtApi[off] and panics on error.
// mustOK is call for init-time entries whose failure is unrecoverable.
//
//go:uintptrescapes
func mustOK(off uintptr, args ...uintptr) {
	if err := checkStatus(call(off, args...)); err != nil {
		panic(err.Error())
	}
}

// goString converts a null-terminated C string pointer to a Go string.
func goString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	p := (*[1 << 20]byte)(unsafe.Pointer(ptr))
	n := 0
	for p[n] != 0 {
		n++
	}
	return string(p[:n])
}

// Session wraps an OrtSession for inference.
type Session struct {
	ptr uintptr
}

// NewSession loads an ONNX model file and creates an inference session.
// The model path is converted to the platform-native character type
// (wchar_t* on Windows, char* on Linux) by the platform loader.
// SessionOption configures the OrtSessionOptions a session is created with.
type SessionOption func(sessOpts uintptr) error

// WithIntraOpThreads caps the threads ORT uses inside a single operator — the
// pool that does the work for CPU inference. ORT's default is one thread per
// logical core, which starves anything rendering concurrently (the webcam
// preview, the UI). 0 keeps ORT's default.
func WithIntraOpThreads(n int) SessionOption {
	return func(sessOpts uintptr) error {
		if err := checkStatus(call(offSetIntraOpNumThreads, sessOpts, uintptr(n))); err != nil {
			return fmt.Errorf("SetIntraOpNumThreads(%d): %w", n, err)
		}
		return nil
	}
}

func NewSession(modelPath string, options ...SessionOption) (*Session, error) {
	// CreateSessionOptions
	var sessOpts uintptr
	if err := checkStatus(call(offCreateSessionOptions, uintptr(unsafe.Pointer(&sessOpts)))); err != nil {
		return nil, fmt.Errorf("NewSession: CreateSessionOptions: %w", err)
	}
	defer purego.SyscallN(fp(offReleaseSessionOptions), sessOpts)

	for _, opt := range options {
		if err := opt(sessOpts); err != nil {
			return nil, fmt.Errorf("NewSession: %w", err)
		}
	}

	// CreateSession — path encoding is platform-specific
	pathPtr, keepAlive := modelPathPtr(modelPath)
	defer keepAlive()

	var sess uintptr
	if err := checkStatus(call(offCreateSession,
		globalEnv,
		pathPtr,
		sessOpts,
		uintptr(unsafe.Pointer(&sess)),
	)); err != nil {
		return nil, fmt.Errorf("NewSession %q: %w", modelPath, err)
	}
	return &Session{ptr: sess}, nil
}

// Run performs one inference pass.
//   - inputName:  ONNX input node name  (e.g. "images" for YOLO, "input" for embedding)
//   - data:       flat float32 input, must match product of shape
//   - shape:      tensor dimensions     (e.g. [1,3,640,640] or [1,224,224,3])
//   - outputName: ONNX output node name (e.g. "output0" or "output")
//
// Returns the flat float32 output; shape can be inferred from the model.
func (s *Session) Run(inputName string, data []float32, shape []int64, outputName string) ([]float32, error) {
	// Wrap input slice in an OrtValue (zero-copy — ORT borrows the slice)
	var inVal uintptr
	if err := checkStatus(call(offCreateTensorWithData,
		cpuMem,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)*4),
		uintptr(unsafe.Pointer(&shape[0])),
		uintptr(len(shape)),
		uintptr(1), // ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT
		uintptr(unsafe.Pointer(&inVal)),
	)); err != nil {
		return nil, fmt.Errorf("Run: CreateTensorWithData: %w", err)
	}
	defer purego.SyscallN(fp(offReleaseValue), inVal)
	// ORT copies shape into the OrtValue but only borrows data, so data must
	// stay reachable until Run has returned — see the KeepAlive after the call.
	runtime.KeepAlive(shape)

	// Build C-style arrays for Run():
	//   const char* const* input_names   — array of 1 string pointer
	//   const OrtValue* const* inputs    — array of 1 value pointer
	//   OrtValue** outputs               — array of 1 output slot
	inNameB := append([]byte(inputName), 0)
	outNameB := append([]byte(outputName), 0)
	// Pointer-typed on purpose: these arrays escape through the uintptrescapes
	// call below, which drags the name buffers they point to onto the heap as
	// well. As plain uintptrs the buffers could stay on the stack and be left
	// behind by a stack move while ORT reads them.
	inNames := [1]unsafe.Pointer{unsafe.Pointer(&inNameB[0])}
	outNames := [1]unsafe.Pointer{unsafe.Pointer(&outNameB[0])}
	inVals := [1]uintptr{inVal}
	outVals := [1]uintptr{0}

	if err := checkStatus(call(offRun,
		s.ptr,
		0, // null OrtRunOptions
		uintptr(unsafe.Pointer(&inNames[0])),
		uintptr(unsafe.Pointer(&inVals[0])),
		1,
		uintptr(unsafe.Pointer(&outNames[0])),
		1,
		uintptr(unsafe.Pointer(&outVals[0])),
	)); err != nil {
		return nil, fmt.Errorf("Run: %w", err)
	}
	outVal := outVals[0]
	defer purego.SyscallN(fp(offReleaseValue), outVal)
	runtime.KeepAlive(data)
	runtime.KeepAlive(inNames)
	runtime.KeepAlive(outNames)
	runtime.KeepAlive(inVals)

	// Determine output size via GetTensorTypeAndShape → GetDimensionsCount → GetDimensions
	var typeShape uintptr
	if err := checkStatus(call(offGetTensorTypeAndShape, outVal, uintptr(unsafe.Pointer(&typeShape)))); err != nil {
		return nil, fmt.Errorf("Run: GetTensorTypeAndShape: %w", err)
	}
	defer purego.SyscallN(fp(offReleaseTensorTypeShape), typeShape)

	var ndim uintptr
	call(offGetDimensionsCount, typeShape, uintptr(unsafe.Pointer(&ndim)))

	dims := make([]int64, ndim)
	call(offGetDimensions, typeShape, uintptr(unsafe.Pointer(&dims[0])), ndim)

	total := int64(1)
	for _, d := range dims {
		total *= d
	}

	// GetTensorMutableData — returns pointer into ORT-owned buffer
	var rawPtr uintptr
	if err := checkStatus(call(offGetTensorMutableData, outVal, uintptr(unsafe.Pointer(&rawPtr)))); err != nil {
		return nil, fmt.Errorf("Run: GetTensorMutableData: %w", err)
	}

	// Copy out before releasing outVal
	result := make([]float32, total)
	copy(result, unsafe.Slice((*float32)(unsafe.Pointer(rawPtr)), total))
	return result, nil
}

// Close releases the underlying OrtSession.
func (s *Session) Close() {
	if s.ptr != 0 {
		purego.SyscallN(fp(offReleaseSession), s.ptr)
		s.ptr = 0
	}
}
