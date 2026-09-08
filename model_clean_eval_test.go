package main

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestEvalCleanedImages measures, for every PNG in $CLEAN_DIR whose stem is an
// index label, the cosine between the embedding of that image and the index
// reference vector — i.e. how well a cleaned English image reproduces the
// reference built from the March French pull. Writes $CLEAN_OUT (JSON id →
// cosine) and logs the distribution. Skips without the env vars.
func TestEvalCleanedImages(t *testing.T) {
	dir := os.Getenv("CLEAN_DIR")
	if dir == "" {
		t.Skip("CLEAN_DIR not set")
	}
	m := loadTestModel(t)
	pos := map[string]int{}
	for i, l := range m.labels {
		pos[l] = i
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float32{}
	var cos []float64
	for _, e := range entries {
		id := e.Name()
		if filepath.Ext(id) != ".png" {
			continue
		}
		id = id[:len(id)-4]
		i, ok := pos[id]
		if !ok {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		emb, err := m.embedCrop(img, img.Bounds())
		if err != nil {
			t.Fatal(err)
		}
		c := dot(emb, m.vectors[i*m.dim:(i+1)*m.dim])
		out[id] = c
		cos = append(cos, float64(c))
	}
	if len(cos) == 0 {
		t.Fatal("no indexed ids found in CLEAN_DIR")
	}
	sort.Float64s(cos)
	q := func(p float64) float64 { return cos[int(p*float64(len(cos)-1))] }
	t.Logf("n=%d  min=%.3f  p10=%.3f  median=%.3f  p90=%.3f  max=%.3f  share>=0.95: %.0f%%",
		len(cos), cos[0], q(0.10), q(0.50), q(0.90), cos[len(cos)-1],
		100*float64(countAbove(cos, 0.95))/float64(len(cos)))
	if outPath := os.Getenv("CLEAN_OUT"); outPath != "" {
		data, _ := json.MarshalIndent(out, "", " ")
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Println("wrote", outPath)
	}
}

func countAbove(sorted []float64, v float64) int {
	n := 0
	for _, x := range sorted {
		if x >= v {
			n++
		}
	}
	return n
}
