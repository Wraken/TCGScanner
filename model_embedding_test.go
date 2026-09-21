package main

import (
	"image"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// testModelDir is the bundle the model tests load. TCG_TEST_MODEL_DIR points
// them at another bundle (a retrained candidate packaged next to models/tcg)
// without swapping directories.
var testModelDir = envOr("TCG_TEST_MODEL_DIR", "models/tcg")

// The Riftbound sources: every twin pair used by the tests lives here.
const testRefDir = "scripts/ml/images/riftbound"

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadTestModel(t *testing.T) *EmbeddingModel {
	t.Helper()
	if _, err := os.Stat(filepath.Join(testModelDir, embeddingModelFile)); err != nil {
		t.Skipf("model not found: %s", testModelDir)
	}
	m, err := NewEmbeddingModel("tcg", testModelDir)
	if err != nil {
		t.Fatalf("NewEmbeddingModel: %v", err)
	}
	t.Cleanup(m.Delete)
	return m
}

// loadRefImage decodes testRefDir/<id>.png, skipping the test when the local
// source images are not present.
func loadRefImage(t *testing.T, id string) image.Image {
	t.Helper()
	path := filepath.Join(testRefDir, id+".png")
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("reference image missing: %s", path)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

func TestIndexLoads(t *testing.T) {
	m := loadTestModel(t)

	if m.n != len(m.labels) {
		t.Fatalf("n=%d but %d labels", m.n, len(m.labels))
	}
	if len(m.vectors) != m.n*m.dim {
		t.Fatalf("vectors len=%d, want %d", len(m.vectors), m.n*m.dim)
	}
	t.Logf("index: %d vectors x %d dims, floor=%.2f margin=%.2f", m.n, m.dim, m.scoreFloor, m.marginMin)

	// Every reference vector must be L2-normalized, otherwise the dot product
	// in search() is not a cosine.
	for i := 0; i < m.n; i++ {
		var norm float32
		for _, v := range m.vectors[i*m.dim : (i+1)*m.dim] {
			norm += v * v
		}
		if math.Abs(float64(norm)-1) > 1e-3 {
			t.Fatalf("vector %d (%s) has squared norm %f, want 1", i, m.labels[i], norm)
		}
	}
}

// TestReferenceSelfRetrieval feeds reference images through the Go embedding
// path and checks each one retrieves itself from the index. This validates the
// whole chain end to end: resize, channel order, raw [0,255] input, index
// parsing and cosine search. A retrieval of a *different* card, or a low mean
// score, means the Go preprocessing diverges from Python's.
//
// "Itself" is judged up to canonicalCard: printings that share the artwork
// (Riftbound star foils, One Piece _pN) sit at cosine ~0.97 in the index, and
// which of the two the Go embedding lands closest to is a coin flip of ~0.01
// that changes from one model to the next. The app folds them onto one
// canonical card and offers both in the variant popup, so that flip is not a
// defect; the strict count is still logged.
func TestReferenceSelfRetrieval(t *testing.T) {
	m := loadTestModel(t)

	entries, err := os.ReadDir(testRefDir)
	if err != nil {
		t.Skipf("reference images not found: %s", testRefDir)
	}

	// Sample evenly across the set so several games and sets are covered.
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".png" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Skip("no reference PNGs")
	}

	const sampleSize = 40
	step := len(names) / sampleSize
	if step < 1 {
		step = 1
	}

	var hits, strictHits, total int
	var minScore, sumScore float64
	minScore = 2
	var failures, twinFlips []string

	for i := 0; i < len(names) && total < sampleSize; i += step {
		name := names[i]
		want := name[:len(name)-len(filepath.Ext(name))]

		f, err := os.Open(filepath.Join(testRefDir, name))
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}

		emb, err := m.embedCrop(img, img.Bounds())
		if err != nil {
			t.Fatalf("embed %s: %v", name, err)
		}
		idx, score, _, _ := m.match(emb)
		got := m.labels[idx]

		total++
		sumScore += float64(score)
		if float64(score) < minScore {
			minScore = float64(score)
		}
		switch {
		case got == want:
			hits++
			strictHits++
		case canonicalCard(got) == canonicalCard(want):
			hits++
			twinFlips = append(twinFlips, want+" -> "+got)
		case len(failures) < 10:
			failures = append(failures, want+" -> "+got)
		}
	}

	t.Logf("self-retrieval: %d/%d up to canonical (%d/%d strict)  mean score=%.3f  min score=%.3f",
		hits, total, strictHits, total, sumScore/float64(total), minScore)
	for _, f := range twinFlips {
		t.Logf("  same-artwork printing: %s", f)
	}
	for _, f := range failures {
		t.Logf("  mismatch: %s", f)
	}

	if hits != total {
		t.Errorf("self-retrieval %d/%d, want all — Go preprocessing likely diverges from Python", hits, total)
	}
	// Python reports near-1.0 similarity for reference images against their own
	// index entry; a low score means the pixels reaching the model are wrong.
	if mean := sumScore / float64(total); mean < 0.9 {
		t.Errorf("mean self-similarity %.3f is too low, want > 0.9", mean)
	}
}
