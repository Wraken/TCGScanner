package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadModelDispatch checks that App.LoadModel routes each model directory
// to the right pipeline. The unit tests build EmbeddingModel directly, so
// without this nothing covers the detection in LoadModel itself — and a wrong
// branch fails at runtime, not at compile time.
func TestLoadModelDispatch(t *testing.T) {
	entries, err := os.ReadDir("models")
	if err != nil {
		t.Skip("no models directory")
	}

	app := NewApp()
	t.Cleanup(func() {
		if app.model != nil {
			app.model.Delete()
		}
	})

	var loaded int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join("models", e.Name())
		wantEmbedding := isEmbeddingModel(dir)

		// Skip directories that hold neither pipeline's model file.
		if !wantEmbedding {
			if _, err := os.Stat(filepath.Join(dir, "model.tflite")); err != nil {
				t.Logf("%-28s skipped (no model file)", e.Name())
				continue
			}
		}

		if err := app.LoadModel(e.Name()); err != nil {
			t.Errorf("%s: LoadModel failed: %v", e.Name(), err)
			continue
		}
		loaded++

		switch m := app.model.(type) {
		case *EmbeddingModel:
			if !wantEmbedding {
				t.Errorf("%s: loaded as embedding model but has no %s", e.Name(), embeddingModelFile)
			}
			t.Logf("%-28s embedding  floor=%.2f margin=%.2f  %d refs", e.Name(), m.scoreFloor, m.marginMin, m.n)
		case *Model:
			if wantEmbedding {
				t.Errorf("%s: has %s but loaded as TFLite classifier", e.Name(), embeddingModelFile)
			}
			t.Logf("%-28s tflite     %dpx", e.Name(), m.imgSize)
		default:
			t.Errorf("%s: unexpected model type %T", e.Name(), app.model)
		}

		if app.model.Name() != e.Name() {
			t.Errorf("%s: Name() returned %q", e.Name(), app.model.Name())
		}
	}

	if loaded == 0 {
		t.Skip("no loadable models")
	}
	t.Logf("%d models loaded and dispatched", loaded)
}
