//go:build windows

package tflite

import (
	_ "embed"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"syscall"
)

//go:embed lib/tensorflowlite_c.dll
var libBytes []byte

// mustLoadLib extracts the embedded DLL to a temp path and loads it.
func mustLoadLib() uintptr {
	checksum := crc32.ChecksumIEEE(libBytes)
	libPath := filepath.Join(os.TempDir(), fmt.Sprintf("tcgscanner_tflite_%08x.dll", checksum))

	if _, err := os.Stat(libPath); os.IsNotExist(err) {
		if err := os.WriteFile(libPath, libBytes, 0644); err != nil {
			panic("tflite: failed to write DLL: " + err.Error())
		}
	}

	handle, err := syscall.LoadLibrary(libPath)
	if err != nil {
		panic("tflite: failed to load DLL: " + err.Error())
	}
	return uintptr(handle)
}
