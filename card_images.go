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

// manifestGameSegment extracts the leading game directory of a manifest path
// ("onepiece/OP01-025.webp" -> "onepiece"). Flat entries and degenerate
// segments ("." / "..") from a corrupt manifest yield "".
func manifestGameSegment(rel string) string {
	i := strings.IndexByte(rel, '/')
	if i <= 0 {
		return ""
	}
	if g := rel[:i]; g != "." && g != ".." {
		return g
	}
	return ""
}

// gameOfCard returns the game sub-directory the manifest files cardID under.
// Unknown ids and flat entries return "".
func gameOfCard(imagesDir, cardID string) string {
	return manifestGameSegment(cardImageManifest(imagesDir)[cardID])
}

// manifestGames returns the sorted distinct games of a manifest.
func manifestGames(imagesDir string) []string {
	seen := map[string]bool{}
	for _, rel := range cardImageManifest(imagesDir) {
		if g := manifestGameSegment(rel); g != "" {
			seen[g] = true
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

// A game's cover art lives at images/<game>/_cover.<ext> in the model
// bundle — distributed with the card images, never committed to the repo
// (official artwork stays out of git). The underscore keeps it clear of the
// card-id namespace, and package_app_model.py preserves it when pruning.
const gameCoverStem = "_cover"

// loadGameCoverFrom returns the first cover found for game across imagesDirs
// as a base64 data URL. The game name is used as a path segment, so anything
// that is not a plain directory name is rejected.
func loadGameCoverFrom(imagesDirs []string, game string) (string, error) {
	if game == "" || game == "." || game == ".." || strings.ContainsAny(game, `/\`) {
		return "", fmt.Errorf("invalid game name %q", game)
	}
	for _, dir := range imagesDirs {
		for _, ext := range cardImageExts {
			data, err := os.ReadFile(filepath.Join(dir, game, gameCoverStem+"."+ext))
			if err != nil {
				continue
			}
			return "data:" + mimeForExt(ext) + ";base64," + base64.StdEncoding.EncodeToString(data), nil
		}
	}
	return "", fmt.Errorf("no cover for game %q", game)
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

// modelNames lists the model directories under root, sorted by name.
func modelNames(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// latestModel is the model under root packaged most recently: the one whose
// images/manifest.json was written last (package_app_model.py rewrites it on
// every run); a model without a manifest ranks by its directory's own time.
func latestModel(root string) (string, bool) {
	var best string
	var bestTime time.Time
	for _, name := range modelNames(root) {
		info, err := os.Stat(filepath.Join(root, name, "images", imageManifestFile))
		if err != nil {
			if info, err = os.Stat(filepath.Join(root, name)); err != nil {
				continue
			}
		}
		if best == "" || info.ModTime().After(bestTime) {
			best, bestTime = name, info.ModTime()
		}
	}
	return best, best != ""
}

// loadCardImageAcross resolves cardID from the most recently packaged model
// under root first, then from preferred (the model that scanned the card),
// then from any other model. A card scanned with a model whose directory is
// gone, or that predates the current bundle, keeps its image.
func loadCardImageAcross(root, cardID, preferred string) (string, error) {
	var order []string
	if latest, ok := latestModel(root); ok {
		order = append(order, latest)
	}
	if preferred != "" {
		order = append(order, preferred)
	}
	order = append(order, modelNames(root)...)
	tried := map[string]bool{}
	for _, name := range order {
		if tried[name] {
			continue
		}
		tried[name] = true
		if img, err := loadCardImageFrom(filepath.Join(root, name, "images"), cardID); err == nil {
			return img, nil
		}
	}
	return "", fmt.Errorf("image not found for card %q in any model under %s", cardID, root)
}
