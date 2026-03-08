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

	"github.com/mattn/go-tflite"
	"golang.org/x/image/draw"
)

const (
	imgSize = 224
)

type Model struct {
	mu          sync.Mutex
	name        string
	model       *tflite.Model
	options     *tflite.InterpreterOptions
	labels      map[int]string
	interpreter *tflite.Interpreter
	inputBuffer []float32
}

func NewModel(modelName, modelPath, labelsPath string) (*Model, error) {
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
		name:        modelName,
		model:       model,
		options:     options,
		labels:      labels,
		interpreter: interpreter,
		inputBuffer: make([]float32, imgSize*imgSize*3),
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
	resized := image.NewRGBA(image.Rect(0, 0, imgSize, imgSize))
	draw.BiLinear.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Over, nil)

	// Convert to float32 — raw [0, 255] values
	idx := 0
	for y := 0; y < imgSize; y++ {
		for x := 0; x < imgSize; x++ {
			r, g, b, _ := resized.At(x, y).RGBA()
			m.inputBuffer[idx+0] = float32(r >> 8)
			m.inputBuffer[idx+1] = float32(g >> 8)
			m.inputBuffer[idx+2] = float32(b >> 8)
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
