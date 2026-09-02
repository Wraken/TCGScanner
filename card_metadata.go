package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Card metadata (name, set, rarity...) ships in the model bundle as one JSON
// file per game: models/<model>/metadata/<game>.json, keyed by card id. It
// travels in the mandatory core of the bundle, unlike images, so names are
// available even when a game's image pack is not installed. Loading mirrors
// the images manifest: parsed once per file, re-read when its mtime changes.
type CardInfo struct {
	ID      string   `json:"id"`
	Game    string   `json:"game"`
	Name    string   `json:"name"`
	Set     string   `json:"set"`
	SetName string   `json:"set_name"`
	Number  string   `json:"number"`
	Rarity  string   `json:"rarity"`
	Type    string   `json:"type"`
	Colors  []string `json:"colors"`
}

type cardMetadataFile struct {
	modTime time.Time
	size    int64
	cards   map[string]CardInfo
}

var (
	cardMetadataMu    sync.Mutex
	cardMetadataFiles = map[string]*cardMetadataFile{} // keyed by file path
)

func modelMetadataDir(modelName string) string {
	return filepath.Join(".", "models", modelName, "metadata")
}

// allModelMetadataDirs lists the metadata dir of every model under ./models
// (the dir itself may not exist for older bundles; loads then yield nothing).
func allModelMetadataDirs() []string {
	entries, err := os.ReadDir(filepath.Join(".", "models"))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, modelMetadataDir(e.Name()))
		}
	}
	return dirs
}

// cardMetadata returns the cards of metadataDir/<game>.json. A missing or
// invalid file yields an empty map; the game name is used as a file name, so
// anything that is not a plain name is rejected the same way.
//
// The returned map is the shared cache itself, not a copy: callers must treat
// it as read-only. Do not mutate entries or their Colors slices — those
// backing arrays are shared with every other caller until the file changes.
func cardMetadata(metadataDir, game string) map[string]CardInfo {
	if game == "" || game == "." || game == ".." || strings.ContainsAny(game, `/\`) {
		return map[string]CardInfo{}
	}
	path := filepath.Join(metadataDir, game+".json")
	info, statErr := os.Stat(path)
	if statErr != nil {
		return map[string]CardInfo{}
	}

	cardMetadataMu.Lock()
	defer cardMetadataMu.Unlock()

	if cached := cardMetadataFiles[path]; cached != nil &&
		cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.cards
	}

	cards := map[string]CardInfo{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cards); err != nil {
			fmt.Printf("bad card metadata %s: %v\n", path, err)
			cards = map[string]CardInfo{}
		}
	}
	cardMetadataFiles[path] = &cardMetadataFile{modTime: info.ModTime(), size: info.Size(), cards: cards}
	return cards
}

// lookupCardInfo resolves cardID's game through the image manifests and reads
// that game's metadata; ids unknown to every manifest fall back to a scan of
// every metadata file present. ID and Game are always set by this function.
func lookupCardInfo(imagesDirs, metadataDirs []string, cardID string) (CardInfo, error) {
	find := func(game string) (CardInfo, bool) {
		for _, dir := range metadataDirs {
			if c, ok := cardMetadata(dir, game)[cardID]; ok {
				c.ID = cardID
				c.Game = game
				if c.Colors == nil {
					c.Colors = []string{}
				}
				return c, true
			}
		}
		return CardInfo{}, false
	}

	if game := gameResolver(imagesDirs)(cardID); game != "" {
		if c, ok := find(game); ok {
			return c, nil
		}
	}
	// Fallback: every distinct game stem across every metadata dir, each
	// probed once (find() itself already walks every dir per stem).
	seen := map[string]bool{}
	var stems []string
	for _, dir := range metadataDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") {
				continue
			}
			stem := strings.TrimSuffix(name, ".json")
			if !seen[stem] {
				seen[stem] = true
				stems = append(stems, stem)
			}
		}
	}
	for _, stem := range stems {
		if c, ok := find(stem); ok {
			return c, nil
		}
	}
	return CardInfo{}, fmt.Errorf("no metadata for card %q", cardID)
}
