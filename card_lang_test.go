package main

import "testing"

func TestLanguages(t *testing.T) {
	if !validLanguage(defaultLanguage) {
		t.Fatalf("defaultLanguage %q is not in cardLanguages", defaultLanguage)
	}
	for _, bad := range []string{"", "EN", "xx", "en ", "fr-FR"} {
		if validLanguage(bad) {
			t.Errorf("validLanguage(%q) = true, want false", bad)
		}
	}
	seen := map[string]bool{}
	for _, l := range cardLanguages {
		if l.Code == "" || l.Label == "" || seen[l.Code] {
			t.Errorf("bad or duplicate language entry %+v", l)
		}
		seen[l.Code] = true
	}
}

func TestCardLanguageRoundTrip(t *testing.T) {
	db := testDB(t)
	id, err := insertCard(db, 0, "OP01-025", "onepiece", "tcg", false, "fr")
	if err != nil {
		t.Fatal(err)
	}
	got, err := getCard(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lang != "fr" {
		t.Errorf("lang after insert = %q, want fr", got.Lang)
	}
	got.Lang = "ja"
	if err := updateCard(db, got); err != nil {
		t.Fatal(err)
	}
	list, err := listCards(db, CardFilter{Game: "onepiece"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Lang != "ja" {
		t.Errorf("listCards after update = %+v, want one copy in ja", list)
	}
}

func TestCardLayerRejectsUnknownLanguage(t *testing.T) {
	db := testDB(t)
	if _, err := insertCard(db, 0, "OP01-025", "onepiece", "tcg", false, ""); err == nil {
		t.Error("insertCard accepted an empty language")
	}
	id, err := insertCard(db, 0, "OP01-025", "onepiece", "tcg", false, "fr")
	if err != nil {
		t.Fatal(err)
	}
	card, err := getCard(db, id)
	if err != nil {
		t.Fatal(err)
	}
	card.Lang = "xx"
	if err := updateCard(db, card); err == nil {
		t.Error("updateCard accepted an unknown language")
	}
	got, _ := getCard(db, id)
	if got.Lang != "fr" {
		t.Errorf("lang after rejected update = %q, want fr", got.Lang)
	}
}
