//go:build windows

package main

import (
	"fmt"
	"os"

	"TCGScanner/pkg/tflite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: main <path/to/model.tflite>")
		os.Exit(1)
	}
	modelPath := os.Args[1]

	fmt.Println("Loading model:", modelPath)
	model := tflite.NewModelFromFile(modelPath)
	if model == nil {
		fmt.Println("FAIL: could not load model")
		os.Exit(1)
	}
	defer model.Delete()
	fmt.Println("OK: model loaded")

	opts := tflite.NewInterpreterOptions()
	opts.SetNumThread(2)
	defer opts.Delete()

	interp := tflite.NewInterpreter(model, opts)
	if interp == nil {
		fmt.Println("FAIL: could not create interpreter")
		os.Exit(1)
	}
	defer interp.Delete()
	fmt.Println("OK: interpreter created")

	if status := interp.AllocateTensors(); status != tflite.OK {
		fmt.Printf("FAIL: AllocateTensors status=%d\n", status)
		os.Exit(1)
	}
	fmt.Println("OK: tensors allocated")

	// Fill input tensor with zeros (224x224x3 float32)
	input := interp.GetInputTensor(0)
	if input == nil {
		fmt.Println("FAIL: could not get input tensor")
		os.Exit(1)
	}
	data := input.Float32s()
	fmt.Printf("OK: input tensor size=%d floats (%d bytes)\n", len(data), len(data)*4)
	for i := range data {
		data[i] = 0
	}

	if status := interp.Invoke(); status != tflite.OK {
		fmt.Printf("FAIL: Invoke status=%d\n", status)
		os.Exit(1)
	}
	fmt.Println("OK: inference ran")

	output := interp.GetOutputTensor(0)
	if output == nil {
		fmt.Println("FAIL: could not get output tensor")
		os.Exit(1)
	}
	out := output.Float32s()
	fmt.Printf("OK: output tensor size=%d classes\n", len(out))

	// Print top-3 scores
	fmt.Println("Top scores (index: value):")
	for i := 0; i < 3 && i < len(out); i++ {
		best, bestIdx := float32(-1e9), 0
		for j, v := range out {
			if v > best {
				best, bestIdx = v, j
			}
		}
		fmt.Printf("  #%d  index=%d  score=%.6f\n", i+1, bestIdx, best)
		out[bestIdx] = -1e9 // mask for next iteration
	}

	fmt.Println("\nAll tests passed.")
}
