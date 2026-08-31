package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// decodeDataURL splits a "data:<mime>;base64,<payload>" string.
func decodeDataURL(t *testing.T, url string) (mime, payload string) {
	t.Helper()
	head, b64, ok := strings.Cut(url, ",")
	if !ok {
		t.Fatalf("not a data URL: %q", url)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("bad base64 in %q: %v", url, err)
	}
	return strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), string(raw)
}

func TestLoadCardImageManifestThenFlatFallback(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "onepiece", "OP01-025.webp"), "nested")
	writeTestFile(t, filepath.Join(dir, "legacy-1.png"), "flat")
	writeTestFile(t, filepath.Join(dir, "manifest.json"), `{"OP01-025": "onepiece/OP01-025.webp"}`)

	got, err := loadCardImageFrom(dir, "OP01-025")
	if err != nil {
		t.Fatalf("manifest lookup: %v", err)
	}
	if mime, payload := decodeDataURL(t, got); mime != "image/webp" || payload != "nested" {
		t.Errorf("manifest lookup = (%s, %q), want (image/webp, nested)", mime, payload)
	}

	got, err = loadCardImageFrom(dir, "legacy-1")
	if err != nil {
		t.Fatalf("flat fallback: %v", err)
	}
	if mime, payload := decodeDataURL(t, got); mime != "image/png" || payload != "flat" {
		t.Errorf("flat fallback = (%s, %q), want (image/png, flat)", mime, payload)
	}

	if _, err := loadCardImageFrom(dir, "nope"); err == nil {
		t.Error("unknown card: want error, got nil")
	}
}

func TestLoadCardImageNoManifest(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "c.jpg"), "jpg")

	got, err := loadCardImageFrom(dir, "c")
	if err != nil {
		t.Fatal(err)
	}
	if mime, payload := decodeDataURL(t, got); mime != "image/jpeg" || payload != "jpg" {
		t.Errorf("got (%s, %q), want (image/jpeg, jpg)", mime, payload)
	}
}

func TestLoadCardImageManifestReload(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a", "x.webp"), "a")
	writeTestFile(t, filepath.Join(dir, "b", "x.webp"), "b")
	manifest := filepath.Join(dir, "manifest.json")
	writeTestFile(t, manifest, `{"x": "a/x.webp"}`)

	got, err := loadCardImageFrom(dir, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, payload := decodeDataURL(t, got); payload != "a" {
		t.Fatalf("initial = %q, want a", payload)
	}

	// Re-packaging rewrites the manifest; a changed mtime must trigger a reload.
	writeTestFile(t, manifest, `{"x": "b/x.webp"}`)
	later := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(manifest, later, later); err != nil {
		t.Fatal(err)
	}

	got, err = loadCardImageFrom(dir, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, payload := decodeDataURL(t, got); payload != "b" {
		t.Errorf("after rewrite = %q, want b", payload)
	}
}

// TestLoadCardImageRealModel checks the packaged tcg model end to end: every
// manifest entry must resolve to a real file, no card should fall back to the
// legacy flat layout, and one card per game must decode as webp.
func TestLoadCardImageRealModel(t *testing.T) {
	imagesDir := filepath.Join("models", "tcg", "images")
	if _, err := os.Stat(filepath.Join(imagesDir, imageManifestFile)); err != nil {
		t.Skipf("no packaged manifest at %s", imagesDir)
	}

	manifest := cardImageManifest(imagesDir)
	if len(manifest) == 0 {
		t.Fatal("manifest is empty")
	}
	games := map[string]string{} // game dir -> one card id
	for id, rel := range manifest {
		if _, err := os.Stat(filepath.Join(imagesDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("manifest entry %s -> %s: %v", id, rel, err)
		}
		game, _, ok := strings.Cut(rel, "/")
		if !ok {
			t.Errorf("manifest entry %s -> %s is not under a game dir", id, rel)
			continue
		}
		if _, seen := games[game]; !seen {
			games[game] = id
		}
	}
	t.Logf("manifest: %d cards across %d game dirs", len(manifest), len(games))

	for game, id := range games {
		got, err := loadCardImageFrom(imagesDir, id)
		if err != nil {
			t.Errorf("%s/%s: %v", game, id, err)
			continue
		}
		if !strings.HasPrefix(got, "data:image/webp;base64,") {
			t.Errorf("%s/%s: unexpected prefix %q", game, id, got[:min(40, len(got))])
		}
	}

	// A leftover flat file means the previous layout was not pruned.
	entries, err := os.ReadDir(imagesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && e.Name() != imageManifestFile {
			t.Errorf("stale flat file in images/: %s", e.Name())
		}
	}
}

func TestGameOfCard(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"OP01-025": "onepiece/OP01-025.webp", "flat-001": "flat-001.webp"}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if g := gameOfCard(dir, "OP01-025"); g != "onepiece" {
		t.Errorf("gameOfCard = %q, want onepiece", g)
	}
	if g := gameOfCard(dir, "flat-001"); g != "" {
		t.Errorf("flat entry should have no game, got %q", g)
	}
	if g := gameOfCard(dir, "unknown"); g != "" {
		t.Errorf("unknown id should have no game, got %q", g)
	}
}

func TestManifestGames(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"a": "pokemon/a.webp", "b": "onepiece/b.webp", "c": "pokemon/c.webp", "d": "d.webp"}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	got := manifestGames(dir)
	want := []string{"onepiece", "pokemon"}
	if !slices.Equal(got, want) {
		t.Errorf("manifestGames = %v, want %v", got, want)
	}
}

func TestGameOfCardRealModel(t *testing.T) {
	dir := modelImagesDir("tcg")
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Skip("models/tcg not present")
	}
	if g := gameOfCard(dir, "sv01-020"); g != "pokemon" {
		t.Errorf("sv01-020 game = %q, want pokemon", g)
	}
}

func TestLoadCardImageManifestEscapeIgnored(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "images")
	writeTestFile(t, filepath.Join(root, "secret.webp"), "outside")
	writeTestFile(t, filepath.Join(dir, "manifest.json"), `{"evil": "../secret.webp"}`)

	if _, err := loadCardImageFrom(dir, "evil"); err == nil {
		t.Error("path escaping the images dir must not resolve")
	}
}
