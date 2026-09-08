package main

import (
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testDetectDir = "scripts/ml/datasets/card_detection"
	pythonRefFile = "testdata/yolo_python_boxes.json"

	// The fixture must come from the exported ONNX, not best.pt: ultralytics
	// letterboxes a .pt to the smallest stride-aligned rectangle but the ONNX
	// to a 640x640 square, and only the square is what the Go code feeds the
	// network. Same weights, different padding, different confidences.
	pythonRefModel = "yolo.onnx"

	// Both sides must read identical pixels. Go's image/jpeg and libjpeg decode
	// the same JPEG differently (chroma upsampling, IDCT rounding) — enough to
	// move YOLO's confidence by 0.1 on a small upscaled image. The generator
	// therefore writes cv2-decoded lossless PNGs and the fixture points at them.
	pythonRefImages = "images/val_png"

	// Tolerances for Go vs Python on identical pixels and preprocessing. What
	// remains is cv2 vs x/image resampling and float summation order.
	boxIoUMin    = 0.98
	confDeltaMax = 0.01
)

// pythonDetection is one entry of the fixture written by
// scripts/ml/dump_yolo_reference.py.
type pythonDetection struct {
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	Detected bool      `json:"detected"`
	Conf     float64   `json:"conf"`
	Box      []float64 `json:"box"` // x1, y1, x2, y2 — nil when not detected
}

type pythonReference struct {
	Model     string                     `json:"model"`
	Conf      float64                    `json:"conf"`
	ImagesDir string                     `json:"images_dir"` // relative to testDetectDir
	Images    map[string]pythonDetection `json:"images"`
}

func iou(a, b image.Rectangle) float64 {
	inter := a.Intersect(b)
	if inter.Empty() {
		return 0
	}
	interArea := float64(inter.Dx() * inter.Dy())
	union := float64(a.Dx()*a.Dy()+b.Dx()*b.Dy()) - interArea
	if union <= 0 {
		return 0
	}
	return interArea / union
}

func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

func decodeFile(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

// TestYOLOParity checks the Go detection path against what ultralytics produces
// on the same images with the same ONNX graph. Go reimplements letterboxing,
// output decoding and the transform back to image space; any error there moves
// the box, and a misplaced box silently ruins recognition downstream.
//
// Parity is the right assertion here rather than agreement with the annotated
// ground truth: on one 4590x8160 photo both implementations return a box
// covering the whole frame instead of the card, which is a property of the YOLO
// weights, not of this code.
func TestYOLOParity(t *testing.T) {
	m := loadTestModel(t)

	raw, err := os.ReadFile(pythonRefFile)
	if err != nil {
		t.Skipf("python reference not found (%s) — regenerate with scripts/ml/dump_yolo_reference.py", pythonRefFile)
	}
	var ref pythonReference
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatalf("parse %s: %v", pythonRefFile, err)
	}
	if ref.Model != pythonRefModel {
		t.Fatalf("fixture was generated from %q, want %q — rerun dump_yolo_reference.py", ref.Model, pythonRefModel)
	}
	if math.Abs(ref.Conf-yoloConfMin) > 1e-9 {
		t.Fatalf("fixture built with conf=%.2f but Go uses %.2f", ref.Conf, yoloConfMin)
	}
	if ref.ImagesDir != pythonRefImages {
		t.Fatalf("fixture reads pixels from %q, want %q — rerun dump_yolo_reference.py", ref.ImagesDir, pythonRefImages)
	}

	imgDir := filepath.Join(testDetectDir, filepath.FromSlash(ref.ImagesDir))
	if _, err := os.Stat(imgDir); err != nil {
		t.Skipf("lossless validation images not available (%s) — rerun dump_yolo_reference.py", imgDir)
	}

	var compared, agreed, boxes int
	var sumIoU, worstIoU, worstConfDelta float64
	worstIoU = 1

	for name, want := range ref.Images {
		img := decodeFile(t, filepath.Join(imgDir, name))
		if got := img.Bounds(); got.Dx() != want.Width || got.Dy() != want.Height {
			t.Errorf("%s: Go decoded %dx%d, Python read %dx%d — orientation or decoder mismatch",
				name, got.Dx(), got.Dy(), want.Width, want.Height)
			continue
		}

		box, score, found, err := m.detect(img)
		if err != nil {
			t.Fatalf("detect %s: %v", name, err)
		}
		compared++

		if found != want.Detected {
			// With identical preprocessing a flip can only happen when the
			// score sits within tolerance of the threshold itself.
			if math.Abs(float64(score)-yoloConfMin) <= confDeltaMax {
				t.Logf("  threshold flip on %s: Go score %.3f, Python found=%v (%.3f)",
					name, score, want.Detected, want.Conf)
				continue
			}
			t.Errorf("%s: Go found=%v (score %.3f) but Python found=%v (conf %.3f)",
				name, found, score, want.Detected, want.Conf)
			continue
		}
		if !found {
			agreed++
			continue
		}

		confDelta := math.Abs(float64(score) - want.Conf)
		worstConfDelta = math.Max(worstConfDelta, confDelta)
		if confDelta > confDeltaMax {
			t.Errorf("%s: confidence Go %.4f vs Python %.4f (delta %.4f > %.2f)",
				name, score, want.Conf, confDelta, confDeltaMax)
			continue
		}

		pyBox := image.Rect(
			int(math.Round(want.Box[0])), int(math.Round(want.Box[1])),
			int(math.Round(want.Box[2])), int(math.Round(want.Box[3])),
		)
		overlap := iou(box, pyBox)
		boxes++
		sumIoU += overlap
		worstIoU = math.Min(worstIoU, overlap)
		if overlap < boxIoUMin {
			t.Errorf("%s: box IoU vs Python %.4f < %.2f\n  go     = %v\n  python = %v",
				name, overlap, boxIoUMin, box, pyBox)
			continue
		}
		agreed++
	}

	if compared == 0 {
		t.Skip("no images compared")
	}
	t.Logf("YOLO parity vs %s: %d/%d agree; %d boxes, mean IoU %.4f (worst %.4f), worst conf delta %.4f",
		ref.Model, agreed, compared, boxes, sumIoU/float64(boxes), worstIoU, worstConfDelta)
}

// TestGroundTruthIoU reports how well the Go boxes match the human annotations.
// This measures the YOLO weights, not this code, so it only logs.
func TestGroundTruthIoU(t *testing.T) {
	m := loadTestModel(t)

	imgDir := filepath.Join(testDetectDir, "images", "val")
	lblDir := filepath.Join(testDetectDir, "labels", "val")
	entries, err := os.ReadDir(imgDir)
	if err != nil {
		t.Skipf("detection val set not found: %s", imgDir)
	}

	var tested, detected, good int
	var sumIoU float64

	for _, e := range entries {
		if e.IsDir() || !isImageName(e.Name()) {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		truth, err := readYOLOLabels(filepath.Join(lblDir, stem+".txt"))
		if err != nil || len(truth) == 0 {
			continue
		}

		img := decodeFile(t, filepath.Join(imgDir, e.Name()))
		tested++
		box, score, found, err := m.detect(img)
		if err != nil {
			t.Fatalf("detect %s: %v", e.Name(), err)
		}
		if !found {
			t.Logf("  not detected: %s (best anchor %.3f)", e.Name(), score)
			continue
		}
		detected++

		w, h := img.Bounds().Dx(), img.Bounds().Dy()
		best := 0.0
		for _, gt := range truth {
			best = math.Max(best, iou(box, gt.rect(w, h)))
		}
		sumIoU += best
		if best >= 0.5 {
			good++
		} else {
			t.Logf("  low IoU %.2f on %s", best, e.Name())
		}
	}

	if tested == 0 {
		t.Skip("no labelled validation images")
	}
	t.Logf("vs ground truth: %d/%d detected, %d/%d IoU>=0.5, mean IoU=%.3f",
		detected, tested, good, detected, sumIoU/float64(detected))
}

// expectedCardID extracts the card id embedded in a validation image name,
// e.g. "32e7b147-sfd-065-221_004.jpg" -> "sfd-065-221". It returns "" when the
// name carries no id known to the index (camera names, web downloads).
func expectedCardID(name string, known map[string]bool) string {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if i := strings.Index(stem, "-"); i == 8 { // 8-hex label-studio prefix
		stem = stem[i+1:]
	}
	if i := strings.LastIndex(stem, "_"); i > 0 {
		stem = stem[:i]
	}
	if known[stem] {
		return stem
	}
	return ""
}

// TestPredictEndToEnd runs the full public entry point on real photos: base64
// JPEG in, card id + cosine score out. Where the file name carries the card's
// id, the prediction must match it whenever a card is detected.
func TestPredictEndToEnd(t *testing.T) {
	m := loadTestModel(t)

	imgDir := filepath.Join(testDetectDir, "images", "val")
	entries, err := os.ReadDir(imgDir)
	if err != nil {
		t.Skipf("detection val set not found: %s", imgDir)
	}

	known := make(map[string]bool, len(m.labels))
	for _, l := range m.labels {
		known[l] = true
	}

	var checked int
	for _, e := range entries {
		if e.IsDir() || !isImageName(e.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(imgDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}

		pred, err := m.Predict(base64.StdEncoding.EncodeToString(raw))
		if err != nil {
			t.Fatalf("Predict %s: %v", e.Name(), err)
		}

		if pred.CardID != "" && !known[pred.CardID] {
			t.Errorf("%s: predicted %q, which is not in the index", e.Name(), pred.CardID)
		}
		if pred.Confidence < -1 || pred.Confidence > 1+1e-6 {
			t.Errorf("%s: cosine score %.4f out of range", e.Name(), pred.Confidence)
		}

		verdict := "not confident"
		if pred.Confident {
			verdict = "LOCK"
		}
		want := expectedCardID(e.Name(), known)
		if want != "" && pred.CardID != "" {
			checked++
			if pred.CardID != want {
				t.Errorf("%s: predicted %s, file name says %s", e.Name(), pred.CardID, want)
				verdict = "WRONG"
			} else {
				verdict += " (expected)"
			}
		}
		t.Logf("%-46s -> %-16s %.3f  margin=%.3f detect=%.2f  %s", e.Name(), pred.CardID, pred.Confidence, pred.Margin, pred.DetectScore, verdict)
	}
	t.Logf("%d predictions checked against ids embedded in file names", checked)
}
