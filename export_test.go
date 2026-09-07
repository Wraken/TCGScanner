package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCardsCSVFiltered(t *testing.T) {
	db := testDB(t)
	seed := []struct {
		id, game string
		foil     bool
	}{
		{"sv01-020", "pokemon", false},
		{"sv01-020", "pokemon", false},
		{"sv01-020", "pokemon", true},
		{"OP01-025", "onepiece", false},
	}
	for _, s := range seed {
		if _, err := insertCard(db, 0, s.id, s.game, "tcg", s.foil, "en"); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(t.TempDir(), "out.csv")
	config := ExportConfig{Fields: []ExportField{FieldNormalCount, FieldFoilCount, FieldCardID}}
	if err := writeCardsCSV(db, CardFilter{Game: "pokemon"}, path, config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	want := "Normal Count,Foil Count,Card ID\n2,1,sv01-020"
	if got != want {
		t.Errorf("csv = %q, want %q", got, want)
	}

	if err := writeCardsCSV(db, CardFilter{}, path, config); err == nil {
		t.Error("invalid filter should be rejected")
	}
}

// The Language field switches the aggregation from per card to per card and
// language; without it every language merges into one row, as before.
func TestWriteCardsCSVByLanguage(t *testing.T) {
	db := testDB(t)
	seed := []struct {
		lang string
		foil bool
	}{{"en", false}, {"en", false}, {"fr", false}, {"fr", true}}
	for _, s := range seed {
		if _, err := insertCard(db, 0, "sv01-020", "pokemon", "tcg", s.foil, s.lang); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "out.csv")
	read := func() string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	}

	config := ExportConfig{Fields: []ExportField{FieldCardID, FieldNormalCount, FieldFoilCount}}
	if err := writeCardsCSV(db, CardFilter{Game: "pokemon"}, path, config); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), "Card ID,Normal Count,Foil Count\nsv01-020,3,1"; got != want {
		t.Errorf("without language: csv = %q, want %q", got, want)
	}

	config.Fields = append(config.Fields, FieldLanguage)
	if err := writeCardsCSV(db, CardFilter{Game: "pokemon"}, path, config); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), "Card ID,Normal Count,Foil Count,Language\nsv01-020,2,0,en\nsv01-020,1,1,fr"; got != want {
		t.Errorf("with language: csv = %q, want %q", got, want)
	}
}

func TestSafeFilename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Deck : Aggro", "Deck - Aggro"},
		{"Rouge/Bleu", "Rouge-Bleu"},
		{"...", "export"},
	}
	for _, c := range cases {
		if got := safeFilename(c.in); got != c.want {
			t.Errorf("safeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
