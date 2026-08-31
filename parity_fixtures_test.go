package main

import (
	"encoding/binary"
	"encoding/json"
	"image"
	stddraw "image/draw"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Fixtures for the mobile port (E:\TCGScannerMobile). Everything the phone
// pipeline must reproduce is dumped from the Go pipeline on one val image:
// the raw frame, the raw YOLO output and its decoded best anchor, the crop
// rectangle, Go's ApproxBiLinear crop, and the embedding ORT computed on it.
//
// The val image is a JPEG, which Go decodes as YCbCr and resamples through a
// YCbCr-specific path; the phone only ever sees RGB. So the frame is converted
// to RGBA first and detect/embedCrop run on that copy — the fixture then tests
// exactly the RGB-in / RGB-out path the phone implements.
//
//	TCG_DUMP_PARITY=1 go test -run TestDumpMobileParityFixtures .
const (
	parityDir   = "testdata/parity"
	parityImage = "32e7b147-sfd-065-221_004.jpg" // locks on desktop (see TestPredictEndToEnd)
)

func parityWrite(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(parityDir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func parityWriteF32(t *testing.T, name string, v []float32) {
	t.Helper()
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[4*i:], mathFloat32bits(f))
	}
	parityWrite(t, name, buf)
}

func parityWriteJSON(t *testing.T, name string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	parityWrite(t, name, data)
}

// rgbaToRGB strips the alpha channel, honouring the stride.
func rgbaToRGB(img *image.RGBA) []byte {
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy()*3)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[(y-b.Min.Y)*img.Stride:]
		for x := 0; x < b.Dx(); x++ {
			out = append(out, row[x*4], row[x*4+1], row[x*4+2])
		}
	}
	return out
}

func TestDumpMobileParityFixtures(t *testing.T) {
	if os.Getenv("TCG_DUMP_PARITY") == "" {
		t.Skip("set TCG_DUMP_PARITY=1 to regenerate testdata/parity")
	}
	m := loadTestModel(t)
	src := decodeFile(t, filepath.Join(testDetectDir, "images", "val", parityImage))

	// RGB copy of the frame — the phone's view of the world.
	rgba := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	stddraw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, stddraw.Src)
	if err := os.MkdirAll(parityDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. frame
	parityWrite(t, "frame.rgb", rgbaToRGB(rgba))
	parityWriteJSON(t, "frame.json", map[string]any{
		"width": rgba.Bounds().Dx(), "height": rgba.Bounds().Dy(), "source": parityImage,
	})

	m.mu.Lock()
	defer m.mu.Unlock()

	// 2. raw YOLO output + decoded best anchor, mirroring detect()
	m.letterbox(rgba)
	const plane = yoloInputSize * yoloInputSize
	for y := 0; y < yoloInputSize; y++ {
		row := y * m.yoloCanvas.Stride
		for x := 0; x < yoloInputSize; x++ {
			base := row + x*4
			p := y*yoloInputSize + x
			m.yoloInput[p] = float32(m.yoloCanvas.Pix[base+0]) / 255
			m.yoloInput[plane+p] = float32(m.yoloCanvas.Pix[base+1]) / 255
			m.yoloInput[2*plane+p] = float32(m.yoloCanvas.Pix[base+2]) / 255
		}
	}
	out, err := m.yolo.Run(yoloInputName, m.yoloInput, []int64{1, 3, yoloInputSize, yoloInputSize}, yoloOutName)
	if err != nil {
		t.Fatal(err)
	}
	parityWriteF32(t, "yolo_output0.f32", out)
	bestIdx, bestScore := 0, float32(-1)
	for i := 0; i < yoloNumAnchor; i++ {
		if s := out[4*yoloNumAnchor+i]; s > bestScore {
			bestScore, bestIdx = s, i
		}
	}
	parityWriteJSON(t, "yolo_box.json", map[string]any{
		"cx": out[0*yoloNumAnchor+bestIdx], "cy": out[1*yoloNumAnchor+bestIdx],
		"w": out[2*yoloNumAnchor+bestIdx], "h": out[3*yoloNumAnchor+bestIdx],
		"score": bestScore, "anchor": bestIdx,
	})

	// 3. crop rectangle (frame coordinates) via the production path
	box, score, found, err := m.detect(rgba)
	if err != nil || !found {
		t.Fatalf("detect: found=%v score=%.3f err=%v", found, score, err)
	}
	parityWriteJSON(t, "crop_rect.json", map[string]any{
		"x": box.Min.X, "y": box.Min.Y, "w": box.Dx(), "h": box.Dy(),
	})

	// 4. Go's resample of that crop + the embedding computed on it
	emb, err := m.embedCrop(rgba, box)
	if err != nil {
		t.Fatal(err)
	}
	parityWrite(t, "crop_384.rgb", rgbaToRGB(m.cropCanvas))
	parityWriteF32(t, "embedding.f32", emb)

	t.Logf("fixtures written to %s: frame %dx%d, box %v (score %.3f), embedding dim %d",
		parityDir, rgba.Bounds().Dx(), rgba.Bounds().Dy(), box, score, len(emb))
}

func mathFloat32bits(f float32) uint32 { return math.Float32bits(f) }
