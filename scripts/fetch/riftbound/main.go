package main

import (
	"encoding/json"
	"flag"
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
	metaFile  = "cards.json"
)

type Card struct {
	ID         string   `json:"id"`
	PublicCode string   `json:"publicCode"`
	ImageURL   string   `json:"imageUrl"`
	Name       string   `json:"name,omitempty"`
	Rarity     string   `json:"rarity,omitempty"`
	Domains    []string `json:"domains,omitempty"`
	Type       string   `json:"type,omitempty"`
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

// nameCleaner normalises typographic variants (same rule as the other
// fetchers) so the corpus is uniform for search.
var nameCleaner = strings.NewReplacer("\u00a0", " ", "\u2019", "'")

// setNames maps the lowercase set prefix of an id to its printed name.
// Extend when a new set ships (unknown prefixes get an empty set_name).
var setNames = map[string]string{
	"ogn": "Origins",
	"ogs": "Origins",
	"sfd": "Spiritforged",
	"unl": "Unleashed",
	"ven": "Vendetta",
}

// split returns the set prefix and number of an id: "ogn-299-star-298" ->
// "ogn", "299".
func split(id string) (set, number string) {
	set, rest, _ := strings.Cut(id, "-")
	number, _, _ = strings.Cut(rest, "-")
	number, _, _ = strings.Cut(number, "_")
	return set, number
}

// numberOf returns the printed collector number for a card. When PublicCode
// is present (e.g. "OGN-299*/298", "SFD-T03", "VEN-SP1/006") it is the
// substring after the first "-", cut at "/" when present: "299*", "T03",
// "SP1". This preserves the "*" star-variant marker and upper-case
// token/promo codes that split(c.ID) loses. Falls back to split(c.ID)'s
// number when PublicCode is empty.
func numberOf(c Card) string {
	if c.PublicCode == "" {
		_, number := split(c.ID)
		return number
	}
	_, rest, _ := strings.Cut(c.PublicCode, "-")
	number, _, _ := strings.Cut(rest, "/")
	return number
}

// writeMeta turns the enriched parsedcards.json entries (those carrying a
// name) into cards.json; entries without a name are skipped, so the file is
// empty until the gallery extraction is redone with metadata fields.
func writeMeta(cards []Card) error {
	out := map[string]meta{}
	skipped := 0
	for _, c := range cards {
		if c.Name == "" {
			skipped++
			continue
		}
		if _, dup := out[c.ID]; dup {
			fmt.Printf("  duplicate id %s\n", c.ID)
		}
		set, _ := split(c.ID)
		colors := c.Domains
		if colors == nil {
			colors = []string{}
		}
		out[c.ID] = meta{
			Name: nameCleaner.Replace(c.Name), Set: strings.ToUpper(set), SetName: setNames[strings.ToLower(set)],
			Number: numberOf(c), Rarity: c.Rarity, Type: c.Type, Colors: colors,
		}
	}
	f, err := os.Create(metaFile)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("Done: %d cards with metadata in %s (%d entries had no name)\n", len(out), metaFile, skipped)
	return nil
}

func main() {
	metaFlag := flag.Bool("meta", false, "write cards.json (metadata) from parsedcards.json instead of downloading images")
	flag.Parse()
	start := time.Now()

	data, err := os.ReadFile(inputJSON)
	if err != nil {
		panic(err)
	}
	var cards []Card
	if err := json.Unmarshal(data, &cards); err != nil {
		panic(err)
	}
	fmt.Printf("Loaded %d cards\n", len(cards))

	if *metaFlag {
		if err := writeMeta(cards); err != nil {
			panic(err)
		}
		return
	}

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
