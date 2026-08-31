package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPipelineTimings measures each stage of the desktop scan pipeline the way
// the app actually runs it: a PNG (what the browser canvas sends) is decoded,
// YOLO runs, the crop is resampled and embedded, and the index is searched.
// Skipped unless TCG_TIMING=1: it is a measurement, not a correctness test.
//
//	TCG_TIMING=1 go test -run TestPipelineTimings -count=1 -v .
func TestPipelineTimings(t *testing.T) {
	if os.Getenv("TCG_TIMING") == "" {
		t.Skip("set TCG_TIMING=1 to run the timing report")
	}
	m := loadTestModel(t)

	for _, name := range []string{"frame_1280x720.png", "frame_1920x1080.png"} {
		t.Run(name, func(t *testing.T) { timeFrame(t, m, filepath.Join("testdata", "timing", name)) })
	}
}

// timeFrame reproduces the app's Predict path on one PNG frame.
func timeFrame(t *testing.T, m *EmbeddingModel, path string) {
	src := decodeFile(t, path)
	raw0, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	_ = buf
	b64 := base64.StdEncoding.EncodeToString(raw0)
	t.Logf("frame: %dx%d, png %d KB, base64 %d KB", src.Bounds().Dx(), src.Bounds().Dy(), buf.Len()/1024, len(b64)/1024)

	const rounds = 8
	var tDecode, tDetect, tEmbed, tMatch time.Duration
	for i := 0; i < rounds; i++ {
		t0 := time.Now()
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		t1 := time.Now()

		m.mu.Lock()
		box, score, found, err := m.detect(img)
		if err != nil || !found {
			m.mu.Unlock()
			t.Fatalf("detect: found=%v score=%.2f err=%v", found, score, err)
		}
		t2 := time.Now()
		emb, err := m.embedCrop(img, box)
		if err != nil {
			m.mu.Unlock()
			t.Fatal(err)
		}
		t3 := time.Now()
		idx, s, margin := m.match(emb)
		m.mu.Unlock()
		t4 := time.Now()

		if i == 0 {
			t.Logf("sanity: %s score=%.3f margin=%.3f", m.labels[idx], s, margin)
		} else { // skip the warm-up round in the averages
			tDecode += t1.Sub(t0)
			tDetect += t2.Sub(t1)
			tEmbed += t3.Sub(t2)
			tMatch += t4.Sub(t3)
		}
	}
	n := time.Duration(rounds - 1)
	total := (tDecode + tDetect + tEmbed + tMatch) / n
	t.Logf("avg over %d rounds (after warm-up):", rounds-1)
	t.Logf("  base64+png decode : %8s", tDecode/n)
	t.Logf("  yolo detect        : %8s", tDetect/n)
	t.Logf("  crop+embed         : %8s", tEmbed/n)
	t.Logf("  match %5d cards  : %8s", m.n, tMatch/n)
	t.Logf("  TOTAL              : %8s  (threads=%d)", total, inferenceThreads())
	_ = fmt.Sprintf
}
