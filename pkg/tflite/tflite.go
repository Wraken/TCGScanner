// Package tflite provides a CGo-free wrapper around the TensorFlow Lite C API.
// The shared library is embedded in the binary and extracted to a temp file on first use.
package tflite

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// Status mirrors TfLiteStatus: 0 = OK.
type Status int32

const OK Status = 0

type Model struct{ ptr uintptr }
type InterpreterOptions struct{ ptr uintptr }
type Interpreter struct{ ptr uintptr }
type Tensor struct{ ptr uintptr }

// ── raw C bindings ────────────────────────────────────────────────────────────

var (
	tfLiteModelCreateFromFile             func(path string) uintptr
	tfLiteModelDelete                     func(model uintptr)
	tfLiteInterpreterOptionsCreate        func() uintptr
	tfLiteInterpreterOptionsDelete        func(options uintptr)
	tfLiteInterpreterOptionsSetNumThreads func(options uintptr, n int32)
	tfLiteInterpreterCreate               func(model, options uintptr) uintptr
	tfLiteInterpreterDelete               func(interp uintptr)
	tfLiteInterpreterAllocateTensors      func(interp uintptr) int32
	tfLiteInterpreterInvoke               func(interp uintptr) int32
	tfLiteInterpreterGetInputTensor       func(interp uintptr, index int32) uintptr
	tfLiteInterpreterGetOutputTensor      func(interp uintptr, index int32) uintptr
	tfLiteTensorData                      func(tensor uintptr) uintptr
	tfLiteTensorByteSize                  func(tensor uintptr) uintptr
)

func init() {
	lib := mustLoadLib()

	purego.RegisterLibFunc(&tfLiteModelCreateFromFile, lib, "TfLiteModelCreateFromFile")
	purego.RegisterLibFunc(&tfLiteModelDelete, lib, "TfLiteModelDelete")
	purego.RegisterLibFunc(&tfLiteInterpreterOptionsCreate, lib, "TfLiteInterpreterOptionsCreate")
	purego.RegisterLibFunc(&tfLiteInterpreterOptionsDelete, lib, "TfLiteInterpreterOptionsDelete")
	purego.RegisterLibFunc(&tfLiteInterpreterOptionsSetNumThreads, lib, "TfLiteInterpreterOptionsSetNumThreads")
	purego.RegisterLibFunc(&tfLiteInterpreterCreate, lib, "TfLiteInterpreterCreate")
	purego.RegisterLibFunc(&tfLiteInterpreterDelete, lib, "TfLiteInterpreterDelete")
	purego.RegisterLibFunc(&tfLiteInterpreterAllocateTensors, lib, "TfLiteInterpreterAllocateTensors")
	purego.RegisterLibFunc(&tfLiteInterpreterInvoke, lib, "TfLiteInterpreterInvoke")
	purego.RegisterLibFunc(&tfLiteInterpreterGetInputTensor, lib, "TfLiteInterpreterGetInputTensor")
	purego.RegisterLibFunc(&tfLiteInterpreterGetOutputTensor, lib, "TfLiteInterpreterGetOutputTensor")
	purego.RegisterLibFunc(&tfLiteTensorData, lib, "TfLiteTensorData")
	purego.RegisterLibFunc(&tfLiteTensorByteSize, lib, "TfLiteTensorByteSize")
}

// ── Public API ────────────────────────────────────────────────────────────────

func NewModelFromFile(path string) *Model {
	ptr := tfLiteModelCreateFromFile(path)
	if ptr == 0 {
		return nil
	}
	return &Model{ptr: ptr}
}

func (m *Model) Delete() { tfLiteModelDelete(m.ptr) }

func NewInterpreterOptions() *InterpreterOptions {
	ptr := tfLiteInterpreterOptionsCreate()
	if ptr == 0 {
		return nil
	}
	return &InterpreterOptions{ptr: ptr}
}

func (o *InterpreterOptions) Delete() { tfLiteInterpreterOptionsDelete(o.ptr) }
func (o *InterpreterOptions) SetNumThread(n int) {
	tfLiteInterpreterOptionsSetNumThreads(o.ptr, int32(n))
}

func NewInterpreter(model *Model, options *InterpreterOptions) *Interpreter {
	ptr := tfLiteInterpreterCreate(model.ptr, options.ptr)
	if ptr == 0 {
		return nil
	}
	return &Interpreter{ptr: ptr}
}

func (i *Interpreter) Delete() { tfLiteInterpreterDelete(i.ptr) }
func (i *Interpreter) AllocateTensors() Status {
	return Status(tfLiteInterpreterAllocateTensors(i.ptr))
}
func (i *Interpreter) Invoke() Status { return Status(tfLiteInterpreterInvoke(i.ptr)) }

func (i *Interpreter) GetInputTensor(index int) *Tensor {
	ptr := tfLiteInterpreterGetInputTensor(i.ptr, int32(index))
	if ptr == 0 {
		return nil
	}
	return &Tensor{ptr: ptr}
}

func (i *Interpreter) GetOutputTensor(index int) *Tensor {
	ptr := tfLiteInterpreterGetOutputTensor(i.ptr, int32(index))
	if ptr == 0 {
		return nil
	}
	return &Tensor{ptr: ptr}
}

// Float32s returns a Go slice backed directly by the tensor's C memory.
func (t *Tensor) Float32s() []float32 {
	dataPtr := tfLiteTensorData(t.ptr)
	byteSize := tfLiteTensorByteSize(t.ptr)
	if dataPtr == 0 || byteSize == 0 {
		return nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(dataPtr)), byteSize/4)
}
