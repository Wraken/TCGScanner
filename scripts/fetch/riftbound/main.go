package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	inputJSON = "parsedcards.json"
	imagesDir = "images"
)

type Card struct {
	ID         string `json:"id"`
	PublicCode string `json:"publicCode"`
	ImageURL   string `json:"imageUrl"`
}

func main() {
	start := time.Now()

	data, err := os.ReadFile(inputJSON)
	if err != nil {
		panic(err)
	}
	var cards []Card
	json.Unmarshal(data, &cards)
	fmt.Printf("Loaded %d cards\n", len(cards))

	os.MkdirAll(imagesDir, 0755)
	ok, fail := 0, 0

	for i, card := range cards {
		imgURL := card.ImageURL
		if idx := strings.Index(imgURL, "?"); idx != -1 {
			imgURL = imgURL[:idx]
		}

		outPath := filepath.Join(imagesDir, card.ID+".png")

		if _, err := os.Stat(outPath); err == nil {
			fmt.Printf("[%d/%d] %s (skipped)\n", i+1, len(cards), card.ID)
			continue
		}

		resp, err := http.Get(imgURL)
		if err != nil || resp.StatusCode != 200 {
			fmt.Printf("[%d/%d] ✗ %s\n", i+1, len(cards), card.ID)
			fail++
			continue
		}

		f, _ := os.Create(outPath)
		io.Copy(f, resp.Body)
		resp.Body.Close()
		f.Close()

		ok++
		fmt.Printf("[%d/%d] ✓ %s\n", i+1, len(cards), card.ID)
	}

	fmt.Printf("Done: %d downloaded, %d failed (%s)\n", ok, fail, time.Since(start).Round(time.Millisecond))
}
