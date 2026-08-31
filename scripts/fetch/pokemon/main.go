// Pokemon card image fetcher (TCGdex open API).
//
// TCGdex (https://tcgdex.dev) is an open, unauthenticated REST API with the
// full card catalog. We enumerate it properly instead of guessing set codes:
//
//	GET /v2/<lang>/sets            -> [{id, name, cardCount}, ...]
//	GET /v2/<lang>/sets/<id>       -> {cards: [{id, image, localId, name}, ...]}
//	<image>/high.png              -> the high-res scan (~745px)
//
// Card files land in images/ as <card.id>.png (e.g. base1-1.png, sv03-125.png)
// -- ids are globally unique and stable. Existing files are skipped, so the
// fetcher is resumable and incremental (re-run after each new set release).
//
// Usage:
//
//	go run .                 # every English set, whole catalog (~24k cards)
//	go run . -sets base1,sv03
//	go run . -lang fr        # French scans (for a FR collection)
//	go run . -list
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
	apiBase   = "https://api.tcgdex.net/v2"
	imagesDir = "images"
	quality   = "high" // high | low
	ext       = "png"  // png | webp
	delay     = 80 * time.Millisecond
	minBytes  = 3000 // guard against error pages / empty bodies
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
		for _, s := range strings.Split(*setsFlag, ",") {
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
