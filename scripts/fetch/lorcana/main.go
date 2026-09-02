// Lorcana card image fetcher (Lorcast open API).
//
// Lorcast (https://lorcast.com/docs/api) is an open, unauthenticated REST API
// with the full Disney Lorcana catalog (Scryfall-style):
//
//	GET /v0/sets                  -> {results: [{id, name, code, released_at}, ...]}
//	GET /v0/sets/<code>/cards     -> [{collector_number, name, image_uris, ...}, ...]
//	image_uris.digital.large      -> 674x940 AVIF scan on cards.lorcast.io (no rate limit)
//
// The CDN only serves AVIF; our training pipeline reads images with OpenCV
// (no AVIF support), so images are decoded here (pure-Go decoder, no cgo)
// and saved as PNG: images/<set>-<collector_number>.png (e.g. 1-207.png,
// P1-4.png). Ids are unique and stable. Existing files are skipped, so the
// fetcher is resumable and incremental.
//
// Usage:
//
//	go run .                 # every set, whole catalog (~3200 cards)
//	go run . -sets 1,9,P1
//	go run . -list
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gen2brain/avif"
)

const (
	apiBase   = "https://api.lorcast.com/v0"
	imagesDir = "images"
	quality   = "large" // small | normal | large
	delay     = 80 * time.Millisecond
	minBytes  = 3000 // guard against error pages / empty bodies
	metaFile  = "cards.json"
)

type setStub struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type setsResponse struct {
	Results []setStub `json:"results"`
}

type card struct {
	CollectorNumber string   `json:"collector_number"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Rarity          string   `json:"rarity"`
	Ink             string   `json:"ink"`
	Inks            []string `json:"inks"`
	Type            []string `json:"type"`
	Set             struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"set"`
	ImageURIs struct {
		Digital map[string]string `json:"digital"`
	} `json:"image_uris"`
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

var client = &http.Client{Timeout: 30 * time.Second}

func getJSON(url string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "TCGScanner card fetcher (personal ML project)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// download fetches an AVIF image and re-encodes it as PNG.
func download(url, outPath string) bool {
	resp, err := client.Get(url)
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
	img, err := avif.Decode(bytes.NewReader(data))
	if err != nil {
		fmt.Printf("    avif decode failed (%s): %v\n", filepath.Base(outPath), err)
		return false
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return false
	}
	return os.WriteFile(outPath, buf.Bytes(), 0o644) == nil
}

// cardID mirrors the image file naming: <set>-<collector_number> with "/"
// replaced ("1-207", "P1-4"), so metadata keys equal index labels.
func cardID(setCode, collectorNumber string) string {
	return setCode + "-" + strings.ReplaceAll(collectorNumber, "/", "-")
}

func fetchSet(setCode string) (downloaded, skipped, failed int) {
	var cards []card
	if err := getJSON(fmt.Sprintf("%s/sets/%s/cards", apiBase, setCode), &cards); err != nil {
		fmt.Printf("  %s: set fetch failed: %v\n", setCode, err)
		return
	}

	for _, c := range cards {
		url := c.ImageURIs.Digital[quality]
		if url == "" {
			continue // no scan available
		}
		id := cardID(setCode, c.CollectorNumber)
		outPath := filepath.Join(imagesDir, id+".png")
		if _, err := os.Stat(outPath); err == nil {
			skipped++
			continue
		}
		if download(url, outPath) {
			downloaded++
		} else {
			failed++
		}
		time.Sleep(delay)
	}
	fmt.Printf("  %s: %d downloaded, %d skipped, %d failed (%d cards)\n",
		setCode, downloaded, skipped, failed, len(cards))
	return
}

func allSetCodes() ([]string, error) {
	var sets setsResponse
	if err := getJSON(apiBase+"/sets", &sets); err != nil {
		return nil, err
	}
	codes := make([]string, len(sets.Results))
	for i, s := range sets.Results {
		codes[i] = s.Code
	}
	return codes, nil
}

func fetchMeta(sets []string) error {
	cards := map[string]meta{}
	for _, code := range sets {
		var list []card
		if err := getJSON(fmt.Sprintf("%s/sets/%s/cards", apiBase, code), &list); err != nil {
			fmt.Printf("  %s: set fetch failed: %v\n", code, err)
			continue
		}
		for _, c := range list {
			name := c.Name
			if c.Version != "" {
				name += " - " + c.Version
			}
			colors := c.Inks
			if len(colors) == 0 && c.Ink != "" {
				colors = []string{c.Ink}
			}
			if colors == nil {
				colors = []string{}
			}
			cards[cardID(code, c.CollectorNumber)] = meta{
				Name: name, Set: code, SetName: c.Set.Name, Number: c.CollectorNumber,
				Rarity: c.Rarity, Type: strings.Join(c.Type, " / "), Colors: colors,
			}
		}
		fmt.Printf("  %s: %d cards\n", code, len(list))
		time.Sleep(delay)
	}
	out, err := os.Create(metaFile)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cards); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	fmt.Printf("Done: %d cards in %s\n", len(cards), metaFile)
	return nil
}

func main() {
	setsFlag := flag.String("sets", "", "comma-separated set codes (default: every set)")
	list := flag.Bool("list", false, "print all set codes and exit")
	metaFlag := flag.Bool("meta", false, "write cards.json (metadata) instead of downloading images")
	flag.Parse()

	if *list {
		codes, err := allSetCodes()
		if err != nil {
			fmt.Println("sets list failed:", err)
			os.Exit(1)
		}
		fmt.Println(strings.Join(codes, " "))
		fmt.Printf("(%d sets)\n", len(codes))
		return
	}

	var sets []string
	if *setsFlag != "" {
		for _, s := range strings.Split(*setsFlag, ",") {
			if s = strings.TrimSpace(s); s != "" {
				sets = append(sets, s)
			}
		}
	} else {
		codes, err := allSetCodes()
		if err != nil {
			fmt.Println("sets list failed:", err)
			os.Exit(1)
		}
		sets = codes
	}

	if *metaFlag {
		if err := fetchMeta(sets); err != nil {
			fmt.Println("metadata failed:", err)
			os.Exit(1)
		}
		return
	}

	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		fmt.Println("cannot create images dir:", err)
		os.Exit(1)
	}

	start := time.Now()
	total := 0
	for _, s := range sets {
		fmt.Printf("Set %s...\n", s)
		ok, _, _ := fetchSet(s)
		total += ok
	}
	fmt.Printf("Done: %d new images in %s/ (%s)\n", total, imagesDir, time.Since(start).Round(time.Second))
}
