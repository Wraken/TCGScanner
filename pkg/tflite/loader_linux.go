//go:build linux

package tflite

import (
	_ "embed"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"

	"github.com/ebitengine/purego"
)

//go:embed lib/libtensorflowlite_c.so
var libBytes []byte

// mustLoadLib extracts the embedded .so to a temp path and loads it.
func mustLoadLib() uintptr {
	checksum := crc32.ChecksumIEEE(libBytes)
	libPath := filepath.Join(os.TempDir(), fmt.Sprintf("tcgscanner_tflite_%08x.so", checksum))

	if _, err := os.Stat(libPath); os.IsNotExist(err) {
		if err := os.WriteFile(libPath, libBytes, 0755); err != nil {
			panic("tflite: failed to write .so: " + err.Error())
		}
	}

	lib, err := purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic("tflite: failed to load .so: " + err.Error())
	}
	return lib
}
