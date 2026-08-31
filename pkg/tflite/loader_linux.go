//go:build linux

package tflite

import (
	_ "embed"

	"TCGScanner/pkg/nativelib"
	"github.com/ebitengine/purego"
)

//go:embed lib/libtensorflowlite_c.so
var libBytes []byte

// mustLoadLib extracts the embedded .so to a temp path and loads it.
func mustLoadLib() uintptr {
	libPath, err := nativelib.Extract("tcgscanner_tflite", ".so", libBytes)
	if err != nil {
		panic("tflite: " + err.Error())
	}

	lib, err := purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic("tflite: failed to load .so: " + err.Error())
	}
	return lib
}
