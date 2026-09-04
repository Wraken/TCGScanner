package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// The official gallery is a Next.js page that inlines the whole card catalog
// (Sanity CMS data) in its __NEXT_DATA__ script: one GET is the extraction.
const galleryURL = "https://playriftbound.com/en-us/card-gallery/"

var (
	nextDataOpen  = []byte(`<script id="__NEXT_DATA__" type="application/json">`)
	nextDataClose = []byte(`</script>`)
)

// galleryCard mirrors the fields of a gallery entry that we keep.
type galleryCard struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PublicCode string `json:"publicCode"`
	Set        struct {
		Value struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"value"`
	} `json:"set"`
	CardType struct {
		Type []struct {
			Label string `json:"label"`
		} `json:"type"`
	} `json:"cardType"`
	Rarity struct {
		Value struct {
			Label string `json:"label"`
		} `json:"value"`
	} `json:"rarity"`
	Domain struct {
		Values []struct {
			Label string `json:"label"`
		} `json:"values"`
	} `json:"domain"`
	CardImage struct {
		URL string `json:"url"`
	} `json:"cardImage"`
}

// extractGallery parses the gallery page into parsedcards.json entries,
// sorted by id. The page is a list of "blades"; the card list is the one
// carrying cards.items (other blades are decoded as empty). The bare
// __NEXT_DATA__ JSON (saved from DevTools) is accepted too.
func extractGallery(html []byte) ([]Card, error) {
	data := bytes.TrimSpace(html)
	if !bytes.HasPrefix(data, []byte("{")) {
		start := bytes.Index(html, nextDataOpen)
		if start < 0 {
			return nil, fmt.Errorf("no __NEXT_DATA__ script in the page (anti-bot wall or page changed)")
		}
		start += len(nextDataOpen)
		end := bytes.Index(html[start:], nextDataClose)
		if end < 0 {
			return nil, fmt.Errorf("unterminated __NEXT_DATA__ script")
		}
		data = html[start : start+end]
	}
	var page struct {
		Props struct {
			PageProps struct {
				Page struct {
					Blades []struct {
						Cards *struct {
							Items []galleryCard `json:"items"`
						} `json:"cards"`
					} `json:"blades"`
				} `json:"page"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("decode __NEXT_DATA__: %w", err)
	}
	var cards []Card
	seen := map[string]bool{}
	for _, blade := range page.Props.PageProps.Page.Blades {
		if blade.Cards == nil {
			continue
		}
		for _, g := range blade.Cards.Items {
			if g.ID == "" {
				return nil, fmt.Errorf("gallery entry without id (publicCode %q)", g.PublicCode)
			}
			if seen[g.ID] {
				return nil, fmt.Errorf("duplicate gallery id %s", g.ID)
			}
			seen[g.ID] = true
			c := Card{
				ID: g.ID, PublicCode: g.PublicCode, ImageURL: g.CardImage.URL,
				Name: g.Name, SetName: g.Set.Value.Label, Rarity: g.Rarity.Value.Label,
			}
			var types []string
			for _, t := range g.CardType.Type {
				types = append(types, t.Label)
			}
			c.Type = strings.Join(types, " / ")
			for _, d := range g.Domain.Values {
				c.Domains = append(c.Domains, d.Label)
			}
			cards = append(cards, c)
		}
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("no cards found in the gallery page (page structure changed?)")
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].ID < cards[j].ID })
	return cards, nil
}

// fetchGallery downloads the gallery page. A browser-like User-Agent is what
// gets us past the site's bot filter today; on a non-200 answer fall back to
// the DevTools procedure in the README.
func fetchGallery() ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, galleryURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gallery: HTTP %d (see README for the browser fallback)", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// writeParsedCards replaces parsedcards.json atomically (tmp + rename) so an
// interrupted write never leaves the fetcher without its input.
func writeParsedCards(cards []Card) error {
	data, err := json.MarshalIndent(cards, "", " ")
	if err != nil {
		return err
	}
	tmp := inputJSON + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, inputJSON)
}
