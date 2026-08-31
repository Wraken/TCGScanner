package ort_test

import (
	"math"
	"os"
	"testing"

	"TCGScanner/pkg/ort"
)

const (
	yoloModel      = "../../scripts/embeddingtest/models/tcg/yolo.onnx"
	embeddingModel = "../../scripts/embeddingtest/models/tcg/embedding_model.onnx"
)

func TestYOLOInference(t *testing.T) {
	if _, err := os.Stat(yoloModel); err != nil {
		t.Skipf("model not found: %s", yoloModel)
	}

	sess, err := ort.NewSession(yoloModel)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	// Dummy black image: [1, 3, 640, 640] NCHW, values in [0, 1]
	input := make([]float32, 1*3*640*640)
	shape := []int64{1, 3, 640, 640}

	out, err := sess.Run("images", input, shape, "output0")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Expected output: [1, 5, 8400] = 42000 elements
	if len(out) != 1*5*8400 {
		t.Errorf("output len = %d, want %d", len(out), 1*5*8400)
	}
	t.Logf("YOLO output: %d elements (first 5: %v)", len(out), out[:5])
}

func TestEmbeddingInference(t *testing.T) {
	if _, err := os.Stat(embeddingModel); err != nil {
		t.Skipf("model not found: %s", embeddingModel)
	}

	sess, err := ort.NewSession(embeddingModel)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	// EfficientNetV2-S uses 384×384 input
	// Node names: input="input_image", output="normalize"
	input := make([]float32, 1*384*384*3)
	shape := []int64{1, 384, 384, 3}

	out, err := sess.Run("input_image", input, shape, "normalize")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Expected: [1, 512] = 512 elements
	if len(out) != 512 {
		t.Errorf("output len = %d, want 512", len(out))
	}
	t.Logf("Embedding output: %d dims (first 5: %v)", len(out), out[:5])
}

// TestIntraOpThreadsOption checks that the thread cap really reaches ORT. The
// option is dispatched through a hand-verified OrtApi offset: a wrong offset
// would call some other entry point with (options, 1) and crash or error here.
// Capping threads must also leave the result unchanged up to summation order.
func TestIntraOpThreadsOption(t *testing.T) {
	if _, err := os.Stat(yoloModel); err != nil {
		t.Skipf("model not found: %s", yoloModel)
	}

	// Deterministic, non-trivial input so the whole graph does real work.
	input := make([]float32, 1*3*640*640)
	for i := range input {
		input[i] = float32(i%251) / 250
	}
	shape := []int64{1, 3, 640, 640}

	run := func(options ...ort.SessionOption) []float32 {
		t.Helper()
		sess, err := ort.NewSession(yoloModel, options...)
		if err != nil {
			t.Fatalf("NewSession with %d option(s): %v", len(options), err)
		}
		defer sess.Close()
		out, err := sess.Run("images", input, shape, "output0")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return out
	}

	ref := run()
	capped := run(ort.WithIntraOpThreads(1))
	if len(ref) != len(capped) {
		t.Fatalf("output len %d (default) vs %d (1 thread)", len(ref), len(capped))
	}

	var maxDiff float64
	for i := range ref {
		maxDiff = math.Max(maxDiff, math.Abs(float64(ref[i]-capped[i])))
	}
	t.Logf("max |default threads - 1 thread| = %g over %d values", maxDiff, len(ref))
	if maxDiff > 1e-3 {
		t.Errorf("thread cap changed the output (max diff %g)", maxDiff)
	}
}
