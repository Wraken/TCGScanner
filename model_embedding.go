package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"sync"

	"TCGScanner/pkg/ort"
	"golang.org/x/image/draw"
)

// Embedding pipeline (pipeline B):
//   frame -> YOLO detect -> best box -> crop -> resize -> embedding -> cosine search
//
// Unlike the TFLite classifier, adding new cards only requires rebuilding the
// index — the embedding model itself stays frozen.

const (
	yoloInputSize = 640
	yoloPadValue  = 114 // ultralytics letterbox padding
	yoloInputName = "images"
	yoloOutName   = "output0"
	yoloNumAnchor = 8400 // anchors for a 640x640 input
	yoloNumAttr   = 5    // cx, cy, w, h, score (single "card" class)
	yoloConfMin   = 0.4  // matches live_test_detection.py --conf

	embedInputName = "input_image"
	embedOutName   = "normalize"

	embeddingModelFile = "embedding_model.onnx"
	yoloModelFile      = "yolo.onnx"
	indexFile          = "index.bin"
	indexLabelsFile    = "index_labels.json"

	defaultEmbeddingImgSize = 384

	// A match locks the UI when the top hit scores at least defaultScoreFloor
	// AND leads the best *different* card by at least defaultMarginMin. The
	// margin, not the absolute score, is what separates a confident match from
	// an ambiguous one: on the reference photos, correct matches lead by
	// 0.14-0.33 while blurry / wrong frames lead by only 0.01-0.04, even though
	// their absolute scores overlap. The floor just rejects pure noise.
	defaultScoreFloor = 0.4
	defaultMarginMin  = 0.1
)

var indexMagic = []byte("TCGEMBIX")

// resampler must approximate cv2.INTER_LINEAR, which is what both the YOLO
// training pipeline (ultralytics) and the index builder used to resize images.
// draw.BiLinear is *better* — it widens its kernel when downscaling, producing
// a properly anti-aliased result — but that is out of distribution for models
// trained on aliased cv2 output, and measurably costs confidence: on a 4590x8160
// photo (12.7x downscale) YOLO scored 0.287 with BiLinear versus 0.407 here,
// against 0.406 for cv2 in Python (ultralytics running the same ONNX with the
// same square letterbox). Reference self-retrieval also improves, 0.945 -> 0.957.
// ApproxBiLinear samples 2x2 source pixels like cv2, and is cheaper too.
var resampler draw.Scaler = draw.ApproxBiLinear

type EmbeddingConfig struct {
	Backbone     string  `json:"backbone"`
	ImgSize      int     `json:"img_size"`
	EmbeddingDim int     `json:"embedding_dim"`
	ScoreFloor   float64 `json:"score_floor"`
	MarginMin    float64 `json:"margin_min"`
}

// isEmbeddingModel reports whether a model directory holds a pipeline-B model.
func isEmbeddingModel(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, embeddingModelFile))
	return err == nil
}

type EmbeddingModel struct {
	mu         sync.Mutex
	name       string
	imgSize    int
	dim        int
	scoreFloor float64
	marginMin  float64

	yolo  *ort.Session
	embed *ort.Session

	labels  []string
	canon   []string  // canonicalCard(labels[i]), precomputed for the margin
	vectors []float32 // n x dim, L2-normalized, row-major
	n       int

	// Reusable buffers, guarded by mu.
	yoloCanvas *image.RGBA
	yoloInput  []float32
	cropCanvas *image.RGBA
	embedInput []float32
	scores     []float32 // cosine of the query against every reference
}

func NewEmbeddingModel(modelName, dir string) (*EmbeddingModel, error) {
	cfg := EmbeddingConfig{ImgSize: defaultEmbeddingImgSize, ScoreFloor: defaultScoreFloor, MarginMin: defaultMarginMin}
	if data, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config.json: %w", err)
		}
	}
	if cfg.ImgSize == 0 {
		cfg.ImgSize = defaultEmbeddingImgSize
	}
	if cfg.ScoreFloor <= 0 {
		cfg.ScoreFloor = defaultScoreFloor
	}
	if cfg.MarginMin <= 0 {
		cfg.MarginMin = defaultMarginMin
	}

	vectors, n, dim, err := loadIndex(filepath.Join(dir, indexFile))
	if err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}
	if cfg.EmbeddingDim != 0 && cfg.EmbeddingDim != dim {
		return nil, fmt.Errorf("index dim %d != config embedding_dim %d", dim, cfg.EmbeddingDim)
	}

	labels, err := loadIndexLabels(filepath.Join(dir, indexLabelsFile))
	if err != nil {
		return nil, fmt.Errorf("load index labels: %w", err)
	}
	if len(labels) != n {
		return nil, fmt.Errorf("index has %d vectors but %d labels", n, len(labels))
	}

	canon := make([]string, len(labels))
	for i, l := range labels {
		canon[i] = canonicalCard(l)
	}

	threads := ort.WithIntraOpThreads(inferenceThreads())
	yolo, err := ort.NewSession(filepath.Join(dir, yoloModelFile), threads)
	if err != nil {
		return nil, fmt.Errorf("load YOLO model: %w", err)
	}

	embed, err := ort.NewSession(filepath.Join(dir, embeddingModelFile), threads)
	if err != nil {
		yolo.Close()
		return nil, fmt.Errorf("load embedding model: %w", err)
	}

	return &EmbeddingModel{
		name:       modelName,
		imgSize:    cfg.ImgSize,
		dim:        dim,
		scoreFloor: cfg.ScoreFloor,
		marginMin:  cfg.MarginMin,
		yolo:       yolo,
		embed:      embed,
		labels:     labels,
		canon:      canon,
		vectors:    vectors,
		n:          n,
		yoloCanvas: image.NewRGBA(image.Rect(0, 0, yoloInputSize, yoloInputSize)),
		yoloInput:  make([]float32, 3*yoloInputSize*yoloInputSize),
		cropCanvas: image.NewRGBA(image.Rect(0, 0, cfg.ImgSize, cfg.ImgSize)),
		embedInput: make([]float32, cfg.ImgSize*cfg.ImgSize*3),
		scores:     make([]float32, n),
	}, nil
}

func (m *EmbeddingModel) Name() string { return m.name }

func (m *EmbeddingModel) Delete() {
	m.embed.Close()
	m.yolo.Close()
}

// loadIndex reads a flat float32 index written by package_app_model.py.
// Layout: magic[8] | uint32 version | uint32 n | uint32 dim | n*dim float32 (LE)
func loadIndex(path string) ([]float32, int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	const headerSize = 8 + 12
	if len(data) < headerSize {
		return nil, 0, 0, fmt.Errorf("file too short (%d bytes)", len(data))
	}
	if !bytes.Equal(data[:8], indexMagic) {
		return nil, 0, 0, fmt.Errorf("bad magic %q", data[:8])
	}
	version := binary.LittleEndian.Uint32(data[8:12])
	if version != 1 {
		return nil, 0, 0, fmt.Errorf("unsupported index version %d", version)
	}
	n := int(binary.LittleEndian.Uint32(data[12:16]))
	dim := int(binary.LittleEndian.Uint32(data[16:20]))
	if n <= 0 || dim <= 0 {
		return nil, 0, 0, fmt.Errorf("invalid index shape %dx%d", n, dim)
	}

	want := headerSize + n*dim*4
	if len(data) != want {
		return nil, 0, 0, fmt.Errorf("size mismatch: %d bytes, expected %d for %dx%d", len(data), want, n, dim)
	}

	vectors := make([]float32, n*dim)
	for i := range vectors {
		off := headerSize + i*4
		vectors[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
	}
	return vectors, n, dim, nil
}

func loadIndexLabels(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var labels []string
	if err := json.Unmarshal(data, &labels); err != nil {
		return nil, err
	}
	return labels, nil
}

func (m *EmbeddingModel) Predict(base64Frame string) (*Prediction, error) {
	imgData, err := base64.StdEncoding.DecodeString(base64Frame)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil, fmt.Errorf("image decode: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	box, detectScore, found, err := m.detect(img)
	if err != nil {
		return nil, err
	}
	if !found {
		// No card in frame — not an error, the UI simply stays unlocked.
		return &Prediction{DetectScore: float64(detectScore)}, nil
	}

	// Normalize the box against the frame so the UI can draw the contour
	// regardless of how the video is scaled on screen.
	b := img.Bounds()
	boxNorm := &BoxNorm{
		X: float64(box.Min.X-b.Min.X) / float64(b.Dx()),
		Y: float64(box.Min.Y-b.Min.Y) / float64(b.Dy()),
		W: float64(box.Dx()) / float64(b.Dx()),
		H: float64(box.Dy()) / float64(b.Dy()),
	}

	embedding, err := m.embedCrop(img, box)
	if err != nil {
		return nil, err
	}

	idx, score, margin := m.match(embedding)
	if idx < 0 {
		return &Prediction{DetectScore: float64(detectScore), Box: boxNorm}, nil
	}

	return &Prediction{
		CardID:      m.labels[idx],
		Confidence:  float64(score),
		Margin:      float64(margin),
		Confident:   score >= float32(m.scoreFloor) && margin >= float32(m.marginMin),
		DetectScore: float64(detectScore),
		Box:         boxNorm,
	}, nil
}

// detect runs YOLO and returns the highest-confidence box in original image
// coordinates. Only the best box is needed since Predict returns a single card,
// so NMS would not change the result. The score is the best anchor's confidence
// and is returned even below yoloConfMin, so callers can see near misses.
func (m *EmbeddingModel) detect(img image.Image) (image.Rectangle, float32, bool, error) {
	scale, padX, padY := m.letterbox(img)

	// RGBA canvas -> NCHW float32 in [0,1], RGB order.
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

	out, err := m.yolo.Run(yoloInputName, m.yoloInput,
		[]int64{1, 3, yoloInputSize, yoloInputSize}, yoloOutName)
	if err != nil {
		return image.Rectangle{}, 0, false, fmt.Errorf("yolo inference: %w", err)
	}
	if len(out) != yoloNumAttr*yoloNumAnchor {
		return image.Rectangle{}, 0, false, fmt.Errorf("yolo output len %d, expected %d",
			len(out), yoloNumAttr*yoloNumAnchor)
	}

	// Output is [1, 5, 8400] row-major: attribute-major, anchor-minor.
	// Take the highest-scoring anchor, then apply the threshold the way
	// ultralytics does (strictly greater).
	bestIdx, bestScore := 0, float32(-1)
	for i := 0; i < yoloNumAnchor; i++ {
		if s := out[4*yoloNumAnchor+i]; s > bestScore {
			bestScore, bestIdx = s, i
		}
	}
	if bestScore <= yoloConfMin {
		return image.Rectangle{}, bestScore, false, nil
	}

	// Box is cx, cy, w, h in letterboxed 640-pixel space -> undo padding & scale.
	cx := float64(out[0*yoloNumAnchor+bestIdx])
	cy := float64(out[1*yoloNumAnchor+bestIdx])
	w := float64(out[2*yoloNumAnchor+bestIdx])
	h := float64(out[3*yoloNumAnchor+bestIdx])

	b := img.Bounds()
	x1 := int(math.Round((cx - w/2 - float64(padX)) / scale))
	y1 := int(math.Round((cy - h/2 - float64(padY)) / scale))
	x2 := int(math.Round((cx + w/2 - float64(padX)) / scale))
	y2 := int(math.Round((cy + h/2 - float64(padY)) / scale))

	box := image.Rect(x1, y1, x2, y2).Add(b.Min).Intersect(b)
	if box.Dx() < 2 || box.Dy() < 2 {
		return image.Rectangle{}, bestScore, false, nil
	}
	return box, bestScore, true, nil
}

// letterbox draws img centered on the 640x640 canvas, preserving aspect ratio
// and padding with gray, and returns the transform needed to undo it.
func (m *EmbeddingModel) letterbox(img image.Image) (scale float64, padX, padY int) {
	b := img.Bounds()
	scale = math.Min(float64(yoloInputSize)/float64(b.Dx()), float64(yoloInputSize)/float64(b.Dy()))
	newW := int(math.Round(float64(b.Dx()) * scale))
	newH := int(math.Round(float64(b.Dy()) * scale))
	padX = (yoloInputSize - newW) / 2
	padY = (yoloInputSize - newH) / 2

	gray := color.RGBA{yoloPadValue, yoloPadValue, yoloPadValue, 255}
	draw.Draw(m.yoloCanvas, m.yoloCanvas.Bounds(), &image.Uniform{gray}, image.Point{}, draw.Src)
	dst := image.Rect(padX, padY, padX+newW, padY+newH)
	resampler.Scale(m.yoloCanvas, dst, img, b, draw.Src, nil)
	return scale, padX, padY
}

// embedCrop crops box out of img, resizes it to the model input size and runs
// the embedding model. The model rescales [0,255] internally (Mul 1/128, Add -1),
// so pixels are fed raw — matching keras efficientnet_v2.preprocess_input, which
// is a no-op.
func (m *EmbeddingModel) embedCrop(img image.Image, box image.Rectangle) ([]float32, error) {
	resampler.Scale(m.cropCanvas, m.cropCanvas.Bounds(), img, box, draw.Src, nil)

	idx := 0
	for y := 0; y < m.imgSize; y++ {
		row := y * m.cropCanvas.Stride
		for x := 0; x < m.imgSize; x++ {
			base := row + x*4
			m.embedInput[idx+0] = float32(m.cropCanvas.Pix[base+0])
			m.embedInput[idx+1] = float32(m.cropCanvas.Pix[base+1])
			m.embedInput[idx+2] = float32(m.cropCanvas.Pix[base+2])
			idx += 3
		}
	}

	out, err := m.embed.Run(embedInputName, m.embedInput,
		[]int64{1, int64(m.imgSize), int64(m.imgSize), 3}, embedOutName)
	if err != nil {
		return nil, fmt.Errorf("embedding inference: %w", err)
	}
	if len(out) != m.dim {
		return nil, fmt.Errorf("embedding len %d, expected %d", len(out), m.dim)
	}

	// The model ends with an L2 normalize, but renormalize defensively so the
	// dot product below is a true cosine (mirrors faiss.normalize_L2 in Python).
	var norm float32
	for _, v := range out {
		norm += v * v
	}
	if norm > 0 {
		inv := float32(1 / math.Sqrt(float64(norm)))
		for i := range out {
			out[i] *= inv
		}
	}
	return out, nil
}

// match returns the closest reference vector, its cosine similarity, and the
// margin by which it leads the best reference of a *different* card. Both sides
// are L2-normalized, so the dot product is the cosine.
//
// The margin, rather than the absolute score, is what tells a confident match
// from an ambiguous one: alternate printings of the winning card are excluded
// via the canonical id, so the runner-up is a genuinely different card. When
// there is no other card in the index (a single-card model), the margin is the
// winner's own score.
func (m *EmbeddingModel) match(query []float32) (idx int, score, margin float32) {
	// One pass to score everything, so the margin pass is plain comparisons.
	for i := 0; i < m.n; i++ {
		row := m.vectors[i*m.dim : (i+1)*m.dim]
		var s float32
		for j, v := range row {
			s += v * query[j]
		}
		m.scores[i] = s
	}

	idx, score = -1, float32(-2)
	for i, s := range m.scores {
		if s > score {
			score, idx = s, i
		}
	}
	if idx < 0 {
		return -1, 0, 0
	}

	winner := m.canon[idx]
	rival := float32(-2)
	for i, s := range m.scores {
		if s > rival && m.canon[i] != winner {
			rival = s
		}
	}
	if rival < -1 {
		return idx, score, score // no different card in the index
	}
	return idx, score, score - rival
}
