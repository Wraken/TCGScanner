package nativelib

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractToWritesAndReuses(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("onnxruntime"), 100)

	p1, err := ExtractTo(dir, "tcgscanner_test", ".dll", data)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p1); !bytes.Equal(got, data) {
		t.Fatalf("content mismatch after first extract")
	}
	if !strings.HasPrefix(filepath.Base(p1), "tcgscanner_test_") || !strings.HasSuffix(p1, ".dll") {
		t.Fatalf("unexpected name %s", p1)
	}

	info1, _ := os.Stat(p1)
	p2, err := ExtractTo(dir, "tcgscanner_test", ".dll", data)
	if err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(p2)
	if p2 != p1 || !info2.ModTime().Equal(info1.ModTime()) {
		t.Fatalf("complete file was rewritten instead of reused")
	}

	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}

func TestExtractToRepairsTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("tensorflowlite"), 100)

	p, err := ExtractTo(dir, "tcgscanner_test", ".so", data)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a crash mid-write from an earlier run.
	if err := os.WriteFile(p, data[:17], 0o644); err != nil {
		t.Fatal(err)
	}

	p2, err := ExtractTo(dir, "tcgscanner_test", ".so", data)
	if err != nil {
		t.Fatal(err)
	}
	if p2 != p {
		t.Fatalf("path changed: %s -> %s", p, p2)
	}
	if got, _ := os.ReadFile(p2); !bytes.Equal(got, data) {
		t.Fatalf("truncated file was not repaired (%d bytes, want %d)", len(got), len(data))
	}
}

func TestExtractToDistinguishesVersions(t *testing.T) {
	dir := t.TempDir()
	p1, _ := ExtractTo(dir, "lib", ".dll", []byte("version one"))
	p2, _ := ExtractTo(dir, "lib", ".dll", []byte("version two"))
	if p1 == p2 {
		t.Fatalf("different contents mapped to the same path %s", p1)
	}
}
