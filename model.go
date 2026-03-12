package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync"

	"path/filepath"

	"TCGScanner/pkg/tflite"
	"golang.org/x/image/draw"
)

type ModelConfig struct {
	Backbone   string `json:"backbone"`
	ImgSize    int    `json:"img_size"`
	Dense      int    `json:"dense"`
	NumClasses int    `json:"num_classes"`
}

const defaultImgSize = 224

func loadModelConfig(configPath string) ModelConfig {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ModelConfig{ImgSize: defaultImgSize} // default for models without config
	}
	var cfg ModelConfig
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.ImgSize == 0 {
		return ModelConfig{ImgSize: defaultImgSize}
	}
	return cfg
}

type Model struct {
	mu           sync.Mutex
	name         string
	imgSize      int
	model        *tflite.Model
	options      *tflite.InterpreterOptions
	labels       map[int]string
	interpreter  *tflite.Interpreter
	inputBuffer  []float32
	resizeBuffer *image.RGBA
}

func NewModel(modelName, modelPath, labelsPath string) (*Model, error) {
	configPath := filepath.Join(filepath.Dir(modelPath), "config.json")
	cfg := loadModelConfig(configPath)

	labels, err := loadLabels(labelsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load labels: %w", err)
	}

	model := tflite.NewModelFromFile(modelPath)
	if model == nil {
		return nil, fmt.Errorf("failed to load TFLite model from %q", modelPath)
	}

	options := tflite.NewInterpreterOptions()
	threads := runtime.NumCPU() / 2
	if threads < 1 {
		threads = 1
	}
	options.SetNumThread(threads)

	interpreter := tflite.NewInterpreter(model, options)
	if interpreter == nil {
		model.Delete()
		options.Delete()
		return nil, fmt.Errorf("failed to create TFLite interpreter")
	}

	if status := interpreter.AllocateTensors(); status != tflite.OK {
		interpreter.Delete()
		model.Delete()
		options.Delete()
		return nil, fmt.Errorf("failed to allocate tensors (status %d)", status)
	}

	return &Model{
		name:         modelName,
		imgSize:      cfg.ImgSize,
		model:        model,
		options:      options,
		labels:       labels,
		interpreter:  interpreter,
		inputBuffer:  make([]float32, cfg.ImgSize*cfg.ImgSize*3),
		resizeBuffer: image.NewRGBA(image.Rect(0, 0, cfg.ImgSize, cfg.ImgSize)),
	}, nil
}

func (m *Model) Delete() {
	m.interpreter.Delete()
	m.options.Delete()
	m.model.Delete()
}

type Prediction struct {
	CardID     string  `json:"card_id"`
	Confidence float64 `json:"confidence"`
}

func (m *Model) Predict(base64Frame string) (*Prediction, error) {
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

	m.preprocessImage(img)

	inputTensor := m.interpreter.GetInputTensor(0)
	copy(inputTensor.Float32s(), m.inputBuffer)

	if status := m.interpreter.Invoke(); status != tflite.OK {
		return nil, fmt.Errorf("inference failed (status %d)", status)
	}

	output := m.interpreter.GetOutputTensor(0).Float32s()
	pred := getBestPrediction(output, m.labels)

	return pred, nil
}

func (m *Model) preprocessImage(img image.Image) {
	draw.BiLinear.Scale(m.resizeBuffer, m.resizeBuffer.Bounds(), img, img.Bounds(), draw.Over, nil)

	// Convert to float32 — raw [0, 255] values via direct Pix access (avoids interface dispatch)
	idx := 0
	for y := 0; y < m.imgSize; y++ {
		for x := 0; x < m.imgSize; x++ {
			base := y*m.resizeBuffer.Stride + x*4
			m.inputBuffer[idx+0] = float32(m.resizeBuffer.Pix[base+0])
			m.inputBuffer[idx+1] = float32(m.resizeBuffer.Pix[base+1])
			m.inputBuffer[idx+2] = float32(m.resizeBuffer.Pix[base+2])
			idx += 3
		}
	}
}

func getBestPrediction(output []float32, labels map[int]string) *Prediction {
	bestIdx := 0
	bestVal := float32(-math.MaxFloat32)

	for i, v := range output {
		if v > bestVal {
			bestVal = v
			bestIdx = i
		}
	}

	cardID := labels[bestIdx]
	if cardID == "" {
		cardID = fmt.Sprintf("unknown-%d", bestIdx)
	}

	return &Prediction{
		CardID:     cardID,
		Confidence: float64(bestVal),
	}
}

func loadLabels(path string) (map[int]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	labels := make(map[int]string, len(raw))
	for k, v := range raw {
		idx, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("invalid label index %q: %w", k, err)
		}
		labels[idx] = v
	}

	return labels, nil
}
