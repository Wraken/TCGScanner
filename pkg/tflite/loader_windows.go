//go:build windows

package tflite

import (
	_ "embed"
	"syscall"

	"TCGScanner/pkg/nativelib"
)

//go:embed lib/tensorflowlite_c.dll
var libBytes []byte

// mustLoadLib extracts the embedded DLL to a temp path and loads it.
func mustLoadLib() uintptr {
	libPath, err := nativelib.Extract("tcgscanner_tflite", ".dll", libBytes)
	if err != nil {
		panic("tflite: " + err.Error())
	}
	handle, err := syscall.LoadLibrary(libPath)
	if err != nil {
		panic("tflite: failed to load DLL: " + err.Error())
	}
	return uintptr(handle)
}
