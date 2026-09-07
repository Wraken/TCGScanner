package main

import (
	"bytes"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestJPEGQualityDrift answers one question: if the frontend sent JPEG at high
// quality instead of PNG (5 MB -> a few hundred KB), how much would the
// embedding move? For each val image: embed the losslessly-decoded original,
// then re-encode at several JPEG qualities, decode, embed the SAME box, and
// compare cosines. Also reports the payload size ratio per quality.
//
//	TCG_TIMING=1 go test -run TestJPEGQualityDrift -count=1 -v .
func TestJPEGQualityDrift(t *testing.T) {
	if os.Getenv("TCG_TIMING") == "" {
		t.Skip("set TCG_TIMING=1 to run")
	}
	m := loadTestModel(t)
	imgDir := filepath.Join(testDetectDir, "images", "val")
	entries, err := os.ReadDir(imgDir)
	if err != nil {
		t.Skip("val set not found")
	}

	qualities := []int{85, 90, 92, 95}
	cos := map[int][]float64{}
	marginShift := map[int][]float64{}
	var sizes = map[int][]float64{} // jpeg bytes / png-equivalent pixels, per quality (ratio vs original file)
	n := 0
	for _, e := range entries {
		if e.IsDir() || !isImageName(e.Name()) {
			continue
		}
		src := decodeFile(t, filepath.Join(imgDir, e.Name()))
		m.mu.Lock()
		box, _, found, err := m.detect(src)
		if err != nil || !found {
			m.mu.Unlock()
			continue
		}
		e0, err := m.embedCrop(src, box)
		if err != nil {
			m.mu.Unlock()
			continue
		}
		ref := append([]float32(nil), e0...)
		_, s0, mg0, _ := m.match(ref)
		m.mu.Unlock()

		for _, q := range qualities {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: q}); err != nil {
				t.Fatal(err)
			}
			img2, _, err := image.Decode(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			// Same box: isolate compression drift from detection drift.
			eq, err := m.embedCrop(img2, box)
			if err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			_, sq, mgq, _ := m.match(eq)
			m.mu.Unlock()
			var dot float64
			for i := range ref {
				dot += float64(ref[i]) * float64(eq[i])
			}
			cos[q] = append(cos[q], dot)
			marginShift[q] = append(marginShift[q], float64(mgq-mg0))
			_ = s0
			_ = sq
			px := float64(src.Bounds().Dx() * src.Bounds().Dy())
			sizes[q] = append(sizes[q], float64(buf.Len())/px) // bytes per pixel
		}
		n++
		if n >= 12 {
			break
		}
	}
	if n == 0 {
		t.Skip("no usable val images")
	}
	t.Logf("%d images; per JPEG quality: cosine(orig, jpeg) and margin shift", n)
	for _, q := range qualities {
		minC, sum := 1.0, 0.0
		for _, c := range cos[q] {
			sum += c
			minC = math.Min(minC, c)
		}
		var worstM, sumM float64
		for _, d := range marginShift[q] {
			sumM += d
			worstM = math.Min(worstM, d)
		}
		var bpp float64
		for _, s := range sizes[q] {
			bpp += s
		}
		bpp /= float64(len(sizes[q]))
		t.Logf("  q%d: cosine mean=%.4f min=%.4f | margin shift mean=%+.4f worst=%+.4f | ~%.2f bytes/px (1080p ≈ %.0f KB)",
			q, sum/float64(len(cos[q])), minC, sumM/float64(len(marginShift[q])), worstM, bpp, bpp*1920*1080/1024)
	}
}
