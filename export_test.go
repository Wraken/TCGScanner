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
		if _, err := insertCard(db, 0, s.id, s.game, "tcg", s.foil); err != nil {
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
