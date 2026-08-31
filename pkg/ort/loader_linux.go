//go:build !windows

package ort

import (
	"os"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

func init() {
	mustInit(mustLoadLib())
}

// mustLoadLib searches for libonnxruntime.so in common locations.
func mustLoadLib() uintptr {
	candidates := []string{}

	if v := os.Getenv("ORT_LIB_PATH"); v != "" {
		candidates = append(candidates, v)
	}
	candidates = append(candidates,
		"libonnxruntime.so",
		"libonnxruntime.so.1",
		"/usr/lib/libonnxruntime.so",
		"/usr/local/lib/libonnxruntime.so",
	)

	for _, p := range candidates {
		lib, err := purego.Dlopen(p, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err == nil {
			return lib
		}
	}
	panic("ort: cannot find libonnxruntime.so — install onnxruntime or set ORT_LIB_PATH")
}

// modelPathPtr converts a Go string to a null-terminated UTF-8 C string pointer.
// On Linux, ORTCHAR_T = char (UTF-8).
func modelPathPtr(path string) (uintptr, func()) {
	b := append([]byte(path), 0)
	return uintptr(unsafe.Pointer(&b[0])), func() { runtime.KeepAlive(b) }
}
