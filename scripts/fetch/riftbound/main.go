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
	SetName    string   `json:"setName,omitempty"` // gallery label ("Proving Grounds")
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

// setNames maps the lowercase set prefix of an id to its printed name, for
// entries extracted before the gallery label (Card.SetName) was kept.
// Unknown prefixes get an empty set_name.
var setNames = map[string]string{
	"ogn": "Origins",
	"ogs": "Proving Grounds",
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

// metaOf maps one parsedcards.json entry to the app's metadata record. The
// gallery's set label wins over the setNames table.
func metaOf(c Card) meta {
	set, _ := split(c.ID)
	setName := c.SetName
	if setName == "" {
		setName = setNames[strings.ToLower(set)]
	}
	colors := c.Domains
	if colors == nil {
		colors = []string{}
	}
	return meta{
		Name: nameCleaner.Replace(c.Name), Set: strings.ToUpper(set), SetName: setName,
		Number: numberOf(c), Rarity: c.Rarity, Type: c.Type, Colors: colors,
	}
}

// writeMeta turns the parsedcards.json entries that carry a name into
// cards.json; entries without a name are skipped (an extraction predating
// -extract yields an empty file).
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
		out[c.ID] = metaOf(c)
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
	extract := flag.Bool("extract", false, "download the official card gallery and rewrite parsedcards.json (no images)")
	htmlFile := flag.String("html", "", "with -extract: parse this saved gallery page (or its __NEXT_DATA__ JSON) instead of downloading")
	flag.Parse()
	start := time.Now()

	if *extract {
		var html []byte
		var err error
		if *htmlFile != "" {
			html, err = os.ReadFile(*htmlFile)
		} else {
			html, err = fetchGallery()
		}
		if err != nil {
			fmt.Println("extract:", err)
			os.Exit(1)
		}
		cards, err := extractGallery(html)
		if err != nil {
			fmt.Println("extract:", err)
			os.Exit(1)
		}
		if err := writeParsedCards(cards); err != nil {
			panic(err)
		}
		perSet := map[string]int{}
		for _, c := range cards {
			set, _ := split(c.ID)
			perSet[set]++
		}
		fmt.Printf("Done: %d cards in %s %v (%s)\n", len(cards), inputJSON, perSet, time.Since(start).Round(time.Millisecond))
		return
	}

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
