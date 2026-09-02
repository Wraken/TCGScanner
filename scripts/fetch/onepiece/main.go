// One Piece card metadata fetcher (punk-records dataset).
//
// Reads the English dataset of github.com/buhbbl/punk-records (generated with
// vegapull from the official card list) and writes cards.json in the app's
// common metadata format. Card ids in the dataset already carry the variant
// suffixes (OP01-006_p3, EB01-006_r1) used by our index labels.
//
// Usage:
//
//	go run .            # download the two source files into cache/, write cards.json
//	go run . -images    # same, then download every card image into images/ (resumable)
//	go run . -offline   # rebuild cards.json from cache/ without downloading
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	rawBase   = "https://raw.githubusercontent.com/buhbbl/punk-records/main/english"
	cacheDir  = "cache"
	outFile   = "cards.json"
	imagesDir = "images"
	delay     = 80 * time.Millisecond
	minBytes  = 3000 // guard against error pages / empty bodies
)

type pack struct {
	ID         string `json:"id"`
	RawTitle   string `json:"raw_title"`
	TitleParts *struct {
		Title string `json:"title"`
		Label string `json:"label"` // "OP-01", "EB-02", "ST-01", "PRB-01"
	} `json:"title_parts"`
}

type sourceCard struct {
	Name     string   `json:"name"`
	Rarity   string   `json:"rarity"`
	Category string   `json:"category"`
	Colors   []string `json:"colors"`
	PackID   string   `json:"pack_id"`
	ImgURL   string   `json:"img_url"`
}

// meta is the app's common metadata record (see the card metadata spec).
type meta struct {
	Name    string   `json:"name"`
	Set     string   `json:"set"`
	SetName string   `json:"set_name"`
	Number  string   `json:"number"`
	Rarity  string   `json:"rarity"`
	Type    string   `json:"type"`
	Colors  []string `json:"colors"`
}

var client = &http.Client{Timeout: 120 * time.Second}

// fetch downloads rel (a path under rawBase) into cache/ unless offline, then
// returns its bytes.
func fetch(rel string, offline bool) ([]byte, error) {
	path := filepath.Join(cacheDir, filepath.Base(rel))
	if offline {
		return os.ReadFile(path)
	}
	resp, err := client.Get(rawBase + "/" + rel)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, rel)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, os.WriteFile(path, data, 0o644)
}

// setCode turns a pack label ("OP-01") into the id prefix ("OP01").
func setCode(label string) string {
	return strings.ReplaceAll(label, "-", "")
}

// split returns the set prefix and the number of an id: "OP01-006_p3" ->
// "OP01", "006" (the variant suffix is dropped from the number only).
func split(id string) (set, number string) {
	set, rest, _ := strings.Cut(id, "-")
	number, _, _ = strings.Cut(rest, "_")
	return set, number
}

func download(url, outPath string) bool {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "TCGScanner card fetcher (personal ML project)")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) < minBytes {
		return false
	}
	return os.WriteFile(outPath, data, 0o644) == nil
}

// fetchImages downloads every card's official image into images/<id>.png,
// skipping files already present (resumable, incremental per new set).
func fetchImages(source map[string]sourceCard) error {
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return err
	}
	ids := make([]string, 0, len(source))
	for id := range source {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	downloaded, skipped, failed := 0, 0, 0
	for _, id := range ids {
		c := source[id]
		if c.ImgURL == "" {
			continue
		}
		outPath := filepath.Join(imagesDir, id+".png")
		if _, err := os.Stat(outPath); err == nil {
			skipped++
			continue
		}
		if download(c.ImgURL, outPath) {
			downloaded++
		} else {
			failed++
			fmt.Printf("  image failed: %s\n", id)
		}
		time.Sleep(delay)
	}
	fmt.Printf("Images: %d downloaded, %d skipped, %d failed (%d cards)\n", downloaded, skipped, failed, len(ids))
	return nil
}

func main() {
	offline := flag.Bool("offline", false, "rebuild from cache/ without downloading")
	images := flag.Bool("images", false, "also download every card image into images/ (official site, SAMPLE-watermarked)")
	flag.Parse()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		fmt.Println("cannot create cache dir:", err)
		os.Exit(1)
	}

	data, err := fetch("packs.json", *offline)
	if err != nil {
		fmt.Println("packs.json:", err)
		os.Exit(1)
	}
	var packs map[string]pack
	if err := json.Unmarshal(data, &packs); err != nil {
		fmt.Println("packs.json:", err)
		os.Exit(1)
	}
	byLabel := map[string]string{} // "OP01" -> "ROMANCE DAWN"
	byPack := map[string]string{}  // "569001" -> "Straw Hat Crew"
	for id, p := range packs {
		if p.TitleParts == nil {
			continue
		}
		if p.TitleParts.Title != "" {
			byPack[id] = p.TitleParts.Title
		}
		if p.TitleParts.Label != "" && p.TitleParts.Title != "" {
			byLabel[setCode(p.TitleParts.Label)] = p.TitleParts.Title
		}
	}

	data, err = fetch("index/cards_by_id.json", *offline)
	if err != nil {
		fmt.Println("cards_by_id.json:", err)
		os.Exit(1)
	}
	var source map[string]sourceCard
	if err := json.Unmarshal(data, &source); err != nil {
		fmt.Println("cards_by_id.json:", err)
		os.Exit(1)
	}

	cards := make(map[string]meta, len(source))
	noSetName := 0
	for id, c := range source {
		set, number := split(id)
		setName, ok := byLabel[set]
		if !ok {
			setName = byPack[c.PackID] // odd labels such as "OP14-EB04"
		}
		if setName == "" {
			noSetName++
		}
		colors := c.Colors
		if colors == nil {
			colors = []string{}
		}
		cards[id] = meta{
			Name: c.Name, Set: set, SetName: setName, Number: number,
			Rarity: c.Rarity, Type: c.Category, Colors: colors,
		}
	}

	// encoding/json sorts map keys, so the file is deterministic and diff-friendly.
	out, err := os.Create(outFile)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer out.Close()
	enc := json.NewEncoder(out)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cards); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Done: %d cards in %s (%d packs, %d cards without a set name)\n", len(cards), outFile, len(packs), noSetName)

	if *images {
		if err := fetchImages(source); err != nil {
			fmt.Println("images:", err)
			os.Exit(1)
		}
	}
}
