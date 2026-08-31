// Package nativelib extracts embedded shared libraries (DLL / .so) to disk so
// they can be loaded with LoadLibrary / dlopen, which need a file path.
package nativelib

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

// Extract writes data to "<prefix>_<crc32><ext>" in the OS temp directory and
// returns the path. See ExtractTo.
func Extract(prefix, ext string, data []byte) (string, error) {
	return ExtractTo(os.TempDir(), prefix, ext, data)
}

// ExtractTo writes data to "<prefix>_<crc32><ext>" under dir and returns the path.
//
// The checksum in the name means a new library version never collides with an
// old one. A file that already exists with the expected size is reused as is.
// A missing or truncated file — an earlier run crashed mid-write, or a cleaner
// removed it — is rewritten through a temp file and a rename, so a concurrent
// process never observes a partial library and a stale partial file cannot
// make every later start fail.
func ExtractTo(dir, prefix, ext string, data []byte) (string, error) {
	path := filepath.Join(dir, fmt.Sprintf("%s_%08x%s", prefix, crc32.ChecksumIEEE(data), ext))

	if complete(path, len(data)) {
		return path, nil
	}

	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return "", fmt.Errorf("nativelib: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		// Another process may have extracted a complete copy in the meantime;
		// on Windows the rename then fails because that copy is mapped.
		if complete(path, len(data)) {
			return path, nil
		}
		return "", fmt.Errorf("nativelib: rename %s: %w", tmp, err)
	}
	return path, nil
}

func complete(path string, size int) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Size() == int64(size)
}
