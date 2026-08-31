package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Card reference images live under models/<model>/images/. Two layouts are
// supported:
//
//   - manifest: images/manifest.json maps card id -> path relative to images/
//     (e.g. "onepiece/OP01-025.webp"). package_app_model.py writes it while
//     sorting images into one sub-directory per game. It is parsed once per
//     model and re-read only when its mtime changes, so a lookup is a single
//     map access — no directory walk, which matters at 30k+ cards.
//   - flat (legacy): images/<card_id>.<ext>, tried when the manifest has no
//     entry for the card or does not exist at all.

const imageManifestFile = "manifest.json"

var cardImageExts = []string{"webp", "png", "jpg", "jpeg"}

type imageManifest struct {
	modTime time.Time
	paths   map[string]string // card id -> path relative to the images dir
}

var (
	imageManifestMu sync.Mutex
	imageManifests  = map[string]*imageManifest{} // keyed by images dir
)

// cardImageManifest returns the manifest for imagesDir, loading it on first
// use and reloading it when the file changes (re-packaging while the app is
// running). A missing or unreadable manifest yields an empty map.
func cardImageManifest(imagesDir string) map[string]string {
	path := filepath.Join(imagesDir, imageManifestFile)
	info, statErr := os.Stat(path)

	imageManifestMu.Lock()
	defer imageManifestMu.Unlock()

	cached := imageManifests[imagesDir]
	if statErr != nil {
		if cached == nil {
			cached = &imageManifest{paths: map[string]string{}}
			imageManifests[imagesDir] = cached
		}
		return cached.paths
	}
	if cached != nil && cached.modTime.Equal(info.ModTime()) {
		return cached.paths
	}

	paths := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &paths); err != nil {
			fmt.Printf("bad image manifest %s: %v\n", path, err)
			paths = map[string]string{}
		}
	}
	imageManifests[imagesDir] = &imageManifest{modTime: info.ModTime(), paths: paths}
	return paths
}

func mimeForExt(ext string) string {
	ext = strings.ToLower(ext)
	if ext == "jpg" {
		return "image/jpeg"
	}
	return "image/" + ext
}

// loadCardImageFrom resolves cardID inside imagesDir — manifest entry first,
// then the flat legacy layout — and returns it as a base64 data URL.
func loadCardImageFrom(imagesDir, cardID string) (string, error) {
	var candidates []string
	if rel, ok := cardImageManifest(imagesDir)[cardID]; ok {
		rel = filepath.Clean(filepath.FromSlash(rel))
		// The manifest is our own output, but never let it escape the images dir.
		if !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			candidates = append(candidates, filepath.Join(imagesDir, rel))
		}
	}
	for _, ext := range cardImageExts {
		candidates = append(candidates, filepath.Join(imagesDir, cardID+"."+ext))
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		ext := strings.TrimPrefix(filepath.Ext(path), ".")
		return "data:" + mimeForExt(ext) + ";base64," + base64.StdEncoding.EncodeToString(data), nil
	}
	return "", fmt.Errorf("image not found for card %q in %s", cardID, imagesDir)
}

func loadCardImage(cardID, modelName string) (string, error) {
	return loadCardImageFrom(modelImagesDir(modelName), cardID)
}

func modelImagesDir(modelName string) string {
	return filepath.Join(".", "models", modelName, "images")
}

// gameOfCard returns the game sub-directory the manifest files cardID under
// ("onepiece/OP01-025.webp" -> "onepiece"). Unknown ids and flat entries
// return "".
func gameOfCard(imagesDir, cardID string) string {
	rel, ok := cardImageManifest(imagesDir)[cardID]
	if !ok {
		return ""
	}
	if i := strings.IndexByte(rel, '/'); i > 0 {
		return rel[:i]
	}
	return ""
}

// manifestGames returns the sorted distinct games of a manifest.
func manifestGames(imagesDir string) []string {
	seen := map[string]bool{}
	for _, rel := range cardImageManifest(imagesDir) {
		if i := strings.IndexByte(rel, '/'); i > 0 {
			seen[rel[:i]] = true
		}
	}
	games := make([]string, 0, len(seen))
	for g := range seen {
		games = append(games, g)
	}
	sort.Strings(games)
	return games
}

// allModelImagesDirs lists the images dir of every model under ./models.
func allModelImagesDirs() []string {
	entries, err := os.ReadDir(filepath.Join(".", "models"))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, modelImagesDir(e.Name()))
		}
	}
	return dirs
}

// gameResolver resolves a card id through several manifests, first hit wins.
func gameResolver(imagesDirs []string) func(cardID string) string {
	return func(cardID string) string {
		for _, dir := range imagesDirs {
			if g := gameOfCard(dir, cardID); g != "" {
				return g
			}
		}
		return ""
	}
}
