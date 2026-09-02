// Pokemon card image fetcher (TCGdex open API).
//
// TCGdex (https://tcgdex.dev) is an open, unauthenticated REST API with the
// full card catalog. We enumerate it properly instead of guessing set codes:
//
//	GET /v2/<lang>/sets            -> [{id, name, cardCount}, ...]
//	GET /v2/<lang>/sets/<id>       -> {cards: [{id, image, localId, name}, ...]}
//	GET /v2/<lang>/cards/<id>      -> {id, localId, name, category, rarity,
//	                                    types, set: {id, name}, ...} (full detail)
//	<image>/high.png              -> the high-res scan (~745px)
//
// Card files land in images/ as <card.id>.png (e.g. base1-1.png, sv03-125.png)
// -- ids are globally unique and stable. Existing files are skipped, so the
// fetcher is resumable and incremental (re-run after each new set release).
//
// -meta writes cards.json (name, set, set_name, number, rarity, type,
// colors) instead of downloading images. The set listing lacks
// rarity/category/types, so this is one detail request per card -- slow
// (~30min for the whole English catalog at the fetcher's pacing), but
// resumable: existing ids are skipped and progress is flushed periodically.
//
// Usage:
//
//	go run .                 # every English set, whole catalog (~24k cards)
//	go run . -sets base1,sv03
//	go run . -lang fr        # French scans (for a FR collection)
//	go run . -list
//	go run . -meta -sets sv01 # write cards.json for one set
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
	apiBase    = "https://api.tcgdex.net/v2"
	imagesDir  = "images"
	quality    = "high" // high | low
	ext        = "png"  // png | webp
	delay      = 80 * time.Millisecond
	minBytes   = 3000 // guard against error pages / empty bodies
	metaFile   = "cards.json"
	flushEvery = 200
)

type setStub struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type card struct {
	ID    string `json:"id"`
	Image string `json:"image"` // base URL, needs /<quality>.<ext>; empty if none
	Name  string `json:"name"`
}

type setDetail struct {
	Cards []card `json:"cards"`
}

type cardDetail struct {
	ID       string   `json:"id"`
	LocalID  string   `json:"localId"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Rarity   string   `json:"rarity"`
	Types    []string `json:"types"`
	Set      struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"set"`
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

// nameCleaner normalises typographic variants the source mixes in (same rule
// as the lorcana fetcher) so the corpus is uniform for search.
var nameCleaner = strings.NewReplacer(" ", " ", "’", "'")

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
	return os.WriteFile(outPath, data, 0o644) == nil
}

func fetchSet(lang, setID string) (downloaded, skipped, failed int) {
	var detail setDetail
	if err := getJSON(fmt.Sprintf("%s/%s/sets/%s", apiBase, lang, setID), &detail); err != nil {
		fmt.Printf("  %s: set fetch failed: %v\n", setID, err)
		return
	}

	for _, c := range detail.Cards {
		if c.Image == "" {
			continue // some promos have no scan
		}
		outPath := filepath.Join(imagesDir, c.ID+"."+ext)
		if _, err := os.Stat(outPath); err == nil {
			skipped++
			continue
		}
		if download(fmt.Sprintf("%s/%s.%s", c.Image, quality, ext), outPath) {
			downloaded++
		} else {
			failed++
		}
		time.Sleep(delay)
	}
	fmt.Printf("  %s: %d downloaded, %d skipped, %d failed (%d cards)\n",
		setID, downloaded, skipped, failed, len(detail.Cards))
	return
}

func loadMeta() map[string]meta {
	cards := map[string]meta{}
	if data, err := os.ReadFile(metaFile); err == nil {
		_ = json.Unmarshal(data, &cards)
	}
	return cards
}

// saveMeta writes atomically (tmp + rename) so an interrupted flush never
// leaves a truncated cards.json behind.
func saveMeta(cards map[string]meta) error {
	tmp := metaFile + ".tmp"
	out, err := os.Create(tmp)
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
	return os.Rename(tmp, metaFile)
}

// fetchMeta fills cards.json for every card of the given sets, one detail
// request per card (the set listing lacks rarity/category/types). Resumable:
// ids already present are skipped, progress is flushed every flushEvery cards.
func fetchMeta(lang string, sets []string) error {
	cards := loadMeta()
	fmt.Printf("%d cards already in %s\n", len(cards), metaFile)
	pending, failed := 0, 0
	for _, setID := range sets {
		var detail setDetail
		if err := getJSON(fmt.Sprintf("%s/%s/sets/%s", apiBase, lang, setID), &detail); err != nil {
			fmt.Printf("  %s: set fetch failed: %v\n", setID, err)
			failed++
			continue
		}
		added := 0
		for _, c := range detail.Cards {
			if _, ok := cards[c.ID]; ok {
				continue
			}
			var d cardDetail
			if err := getJSON(fmt.Sprintf("%s/%s/cards/%s", apiBase, lang, c.ID), &d); err != nil {
				fmt.Printf("    %s: %v\n", c.ID, err)
				failed++
				time.Sleep(delay)
				continue
			}
			colors := d.Types
			if colors == nil {
				colors = []string{}
			}
			cards[c.ID] = meta{
				Name: nameCleaner.Replace(d.Name), Set: d.Set.ID, SetName: nameCleaner.Replace(d.Set.Name), Number: d.LocalID,
				Rarity: d.Rarity, Type: d.Category, Colors: colors,
			}
			added++
			pending++
			if pending >= flushEvery {
				if err := saveMeta(cards); err != nil {
					return err
				}
				pending = 0
			}
			time.Sleep(delay)
		}
		fmt.Printf("  %s: %d new (%d cards)\n", setID, added, len(detail.Cards))
	}
	if err := saveMeta(cards); err != nil {
		return err
	}
	fmt.Printf("Done: %d cards in %s (%d failures — re-run to retry)\n", len(cards), metaFile, failed)
	if failed > 0 {
		return fmt.Errorf("%d request(s) failed", failed)
	}
	return nil
}

func allSetIDs(lang string) ([]string, error) {
	var sets []setStub
	if err := getJSON(fmt.Sprintf("%s/%s/sets", apiBase, lang), &sets); err != nil {
		return nil, err
	}
	ids := make([]string, len(sets))
	for i, s := range sets {
		ids[i] = s.ID
	}
	return ids, nil
}

func main() {
	setsFlag := flag.String("sets", "", "comma-separated set ids (default: every set of the language)")
	lang := flag.String("lang", "en", "catalog language (en, fr, de, es, it, pt, ...)")
	list := flag.Bool("list", false, "print all set ids for the language and exit")
	metaFlag := flag.Bool("meta", false, "write cards.json (metadata) instead of downloading images; resumable")
	flag.Parse()

	if *list {
		ids, err := allSetIDs(*lang)
		if err != nil {
			fmt.Println("sets list failed:", err)
			os.Exit(1)
		}
		fmt.Println(strings.Join(ids, " "))
		fmt.Printf("(%d sets)\n", len(ids))
		return
	}

	var sets []string
	if *setsFlag != "" {
		for s := range strings.SplitSeq(*setsFlag, ",") {
			if s = strings.TrimSpace(s); s != "" {
				sets = append(sets, s)
			}
		}
	} else {
		ids, err := allSetIDs(*lang)
		if err != nil {
			fmt.Println("sets list failed:", err)
			os.Exit(1)
		}
		sets = ids
	}

	if *metaFlag {
		if err := fetchMeta(*lang, sets); err != nil {
			fmt.Println("metadata:", err)
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
		ok, _, _ := fetchSet(*lang, s)
		total += ok
	}
	fmt.Printf("Done: %d new images in %s/ (%s)\n", total, imagesDir, time.Since(start).Round(time.Second))
}
