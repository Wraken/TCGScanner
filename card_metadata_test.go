package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCardMetadataLoadAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onepiece.json")
	writeTestFile(t, path, `{"OP01-025": {"name": "Zoro", "set": "OP01", "set_name": "Romance Dawn",
		"number": "025", "rarity": "SR", "type": "Character", "colors": ["Red"], "future_field": 1}}`)

	m := cardMetadata(dir, "onepiece")
	c, ok := m["OP01-025"]
	if !ok {
		t.Fatal("OP01-025 missing")
	}
	if c.Name != "Zoro" || c.Set != "OP01" || c.SetName != "Romance Dawn" || c.Number != "025" ||
		c.Rarity != "SR" || c.Type != "Character" || !slices.Equal(c.Colors, []string{"Red"}) {
		t.Errorf("unexpected card: %+v", c)
	}
	// ID/Game are filled by the lookup, never by the file.
	if c.ID != "" || c.Game != "" {
		t.Errorf("loader must not set ID/Game, got %q/%q", c.ID, c.Game)
	}

	// mtime-based reload picks up a rewritten file.
	writeTestFile(t, path, `{"OP01-025": {"name": "Roronoa Zoro"}}`)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if got := cardMetadata(dir, "onepiece")["OP01-025"].Name; got != "Roronoa Zoro" {
		t.Errorf("after rewrite name = %q, want Roronoa Zoro", got)
	}
}

func TestCardMetadataAbsentOrInvalid(t *testing.T) {
	dir := t.TempDir()
	if m := cardMetadata(dir, "pokemon"); len(m) != 0 {
		t.Errorf("absent file should yield an empty map, got %d entries", len(m))
	}
	writeTestFile(t, filepath.Join(dir, "lorcana.json"), `{not json`)
	if m := cardMetadata(dir, "lorcana"); len(m) != 0 {
		t.Errorf("invalid file should yield an empty map, got %d entries", len(m))
	}
	// Hostile game names never leave the metadata dir.
	for _, g := range []string{"", "..", "../x", "a/b"} {
		if m := cardMetadata(dir, g); len(m) != 0 {
			t.Errorf("game %q should yield an empty map", g)
		}
	}
}

func TestLookupCardInfo(t *testing.T) {
	images := t.TempDir()
	meta := t.TempDir()
	writeTestFile(t, filepath.Join(images, "manifest.json"),
		`{"OP01-025": "onepiece/OP01-025.webp", "sv01-020": "pokemon/sv01-020.webp"}`)
	writeTestFile(t, filepath.Join(meta, "onepiece.json"), `{"OP01-025": {"name": "Zoro"}}`)
	writeTestFile(t, filepath.Join(meta, "lorcana.json"), `{"1-1": {"name": "Ariel"}}`)

	// Via the manifest: game resolved, only that file consulted.
	c, err := lookupCardInfo([]string{images}, []string{meta}, "OP01-025")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "OP01-025" || c.Game != "onepiece" || c.Name != "Zoro" {
		t.Errorf("got %+v", c)
	}
	if c.Colors == nil {
		t.Error("Colors must be an empty slice, not nil, so the JSON binding sends []")
	}

	// Not in any manifest: fallback scan over every metadata file present.
	c, err = lookupCardInfo([]string{images}, []string{meta}, "1-1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Game != "lorcana" || c.Name != "Ariel" {
		t.Errorf("fallback lookup got %+v", c)
	}

	// In the manifest but not in its game's file, and nowhere else -> error.
	if _, err := lookupCardInfo([]string{images}, []string{meta}, "sv01-020"); err == nil {
		t.Error("missing metadata should error")
	}
	if _, err := lookupCardInfo([]string{images}, []string{meta}, "nope"); err == nil {
		t.Error("unknown id should error")
	}
}

func TestLookupCardInfoRealBundle(t *testing.T) {
	dir := modelMetadataDir("tcg")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Skip("models/tcg/metadata not present or empty")
	}
	// Whatever game is packaged first, one of its entries must resolve
	// through the real manifests with a non-empty name.
	game := strings.TrimSuffix(entries[0].Name(), ".json")
	m := cardMetadata(dir, game)
	if len(m) == 0 {
		t.Fatalf("%s.json loaded empty", game)
	}
	for id := range m {
		c, err := lookupCardInfo(allModelImagesDirs(), allModelMetadataDirs(), id)
		if err != nil {
			t.Fatalf("lookup %s: %v", id, err)
		}
		if c.Name == "" {
			t.Errorf("%s has an empty name", id)
		}
		break
	}
}
