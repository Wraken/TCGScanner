//go:build windows

package ort

import (
	_ "embed"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"TCGScanner/pkg/nativelib"
)

//go:embed lib/onnxruntime.dll
var libBytes []byte

func init() {
	mustInit(mustLoadLib())
}

// mustLoadLib loads onnxruntime.dll. Search order:
//  1. ORT_LIB_PATH environment variable (dev override)
//  2. Embedded DLL, extracted to a temp path keyed by content checksum
func mustLoadLib() uintptr {
	if v := os.Getenv("ORT_LIB_PATH"); v != "" {
		if h, err := syscall.LoadLibrary(v); err == nil {
			return uintptr(h)
		}
	}

	libPath, err := nativelib.Extract("tcgscanner_ort", ".dll", libBytes)
	if err != nil {
		panic("ort: " + err.Error())
	}
	handle, err := syscall.LoadLibrary(libPath)
	if err != nil {
		panic("ort: failed to load DLL: " + err.Error())
	}
	return uintptr(handle)
}

// modelPathPtr converts a Go string to a UTF-16 pointer for CreateSession.
// On Windows, ORTCHAR_T = wchar_t (UTF-16LE).
// The returned keepAlive func must be deferred to prevent GC of the buffer.
func modelPathPtr(path string) (uintptr, func()) {
	// Manual UTF-16 encoding to avoid importing golang.org/x/sys/windows
	runes := []rune(path)
	buf := make([]uint16, len(runes)+1) // +1 for null terminator
	for i, r := range runes {
		buf[i] = uint16(r)
	}
	buf[len(runes)] = 0
	return uintptr(unsafe.Pointer(&buf[0])), func() { runtime.KeepAlive(buf) }
}
