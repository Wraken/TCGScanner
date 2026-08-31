package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCustomCollectionCRUD(t *testing.T) {
	db := testDB(t)

	if _, err := createCollection(db, "  "); err == nil {
		t.Fatal("empty name should be rejected")
	}
	id, err := createCollection(db, "  Deck Ligue ")
	if err != nil {
		t.Fatal(err)
	}
	c, err := getCollection(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Deck Ligue" {
		t.Errorf("name = %q, want trimmed %q", c.Name, "Deck Ligue")
	}
	if err := renameCollection(db, id, ""); err == nil {
		t.Fatal("rename to empty should be rejected")
	}
	if err := renameCollection(db, id, "À vendre"); err != nil {
		t.Fatal(err)
	}

	// A copy in the custom survives the custom's deletion.
	rowID, err := insertCard(db, id, "sv01-020", "pokemon", "tcg", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteCollection(db, id); err != nil {
		t.Fatal(err)
	}
	card, err := getCard(db, rowID)
	if err != nil {
		t.Fatal(err)
	}
	if card.CollectionID != 0 {
		t.Errorf("collection_id = %d after delete, want 0", card.CollectionID)
	}
	if card.Game != "pokemon" {
		t.Errorf("game = %q after delete, want pokemon", card.Game)
	}
}

func TestGameCollections(t *testing.T) {
	db := testDB(t)
	seed := []struct {
		id, game string
		foil     bool
	}{
		{"sv01-020", "pokemon", false},
		{"sv01-020", "pokemon", true},
		{"OP01-025", "onepiece", false},
		{"weird-1", "", false},
	}
	for _, s := range seed {
		if _, err := insertCard(db, 0, s.id, s.game, "tcg", s.foil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := gameCollections(db, []string{"lorcana", "onepiece", "pokemon"})
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by game; '' bucket LAST and only when non-empty; known-but-empty
	// games present with zero counts.
	want := []GameCollection{
		{Game: "lorcana", CardCount: 0, FoilCount: 0},
		{Game: "onepiece", CardCount: 1, FoilCount: 0},
		{Game: "pokemon", CardCount: 2, FoilCount: 1},
		{Game: "", CardCount: 1, FoilCount: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestGameCollectionsEmptyBucketHidden(t *testing.T) {
	db := testDB(t)
	if _, err := insertCard(db, 0, "sv01-020", "pokemon", "tcg", false); err != nil {
		t.Fatal(err)
	}
	got, err := gameCollections(db, []string{"pokemon"})
	if err != nil {
		t.Fatal(err)
	}
	want := []GameCollection{{Game: "pokemon", CardCount: 1, FoilCount: 0}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %v, want %v (no '' bucket when empty)", got, want)
	}
}

func TestCardFilterValidation(t *testing.T) {
	db := testDB(t)
	bad := []CardFilter{
		{},
		{Game: "pokemon", CollectionID: 3},
		{Game: "pokemon", OtherBucket: true},
		{OtherBucket: true, CollectionID: 3},
	}
	for _, f := range bad {
		if _, err := listCards(db, f); err == nil {
			t.Errorf("filter %+v should be rejected", f)
		}
	}
}

func TestListCardsAndMove(t *testing.T) {
	db := testDB(t)
	custom, err := createCollection(db, "Trades")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := insertCard(db, 0, "sv01-020", "pokemon", "tcg", false)
	b, _ := insertCard(db, custom, "OP01-025", "onepiece", "tcg", true)
	if _, err := insertCard(db, 0, "weird-1", "", "tcg", false); err != nil {
		t.Fatal(err)
	}

	poke, err := listCards(db, CardFilter{Game: "pokemon"})
	if err != nil {
		t.Fatal(err)
	}
	if len(poke) != 1 || poke[0].ID != a || poke[0].Game != "pokemon" {
		t.Fatalf("pokemon view = %+v", poke)
	}

	other, err := listCards(db, CardFilter{OtherBucket: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 || other[0].CardID != "weird-1" {
		t.Fatalf("other bucket = %+v", other)
	}

	inCustom, err := listCards(db, CardFilter{CollectionID: custom})
	if err != nil {
		t.Fatal(err)
	}
	if len(inCustom) != 1 || inCustom[0].ID != b || inCustom[0].CollectionID != custom {
		t.Fatalf("custom view = %+v", inCustom)
	}

	// Move a into the custom, b out of it.
	if err := moveCardToCustom(db, a, custom); err != nil {
		t.Fatal(err)
	}
	if err := moveCardToCustom(db, b, 0); err != nil {
		t.Fatal(err)
	}
	inCustom, _ = listCards(db, CardFilter{CollectionID: custom})
	if len(inCustom) != 1 || inCustom[0].ID != a {
		t.Fatalf("custom view after moves = %+v", inCustom)
	}
}

func TestListCollectionsNewestFirst(t *testing.T) {
	db := testDB(t)
	first, err := createCollection(db, "First")
	if err != nil {
		t.Fatal(err)
	}
	second, err := createCollection(db, "Second")
	if err != nil {
		t.Fatal(err)
	}
	list, err := listCollections(db)
	if err != nil {
		t.Fatal(err)
	}
	// Same created_at second: the id DESC tie-break must put the newest first.
	if len(list) != 2 || list[0].ID != second || list[1].ID != first {
		t.Fatalf("order = %+v, want [%d %d]", list, second, first)
	}
}

func TestListCardsStableOrder(t *testing.T) {
	db := testDB(t)
	a, err := insertCard(db, 0, "sv01-001", "pokemon", "tcg", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := insertCard(db, 0, "sv01-002", "pokemon", "tcg", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := listCards(db, CardFilter{Game: "pokemon"})
	if err != nil {
		t.Fatal(err)
	}
	// Same scanned_at second: the id ASC tie-break keeps insertion order.
	if len(got) != 2 || got[0].ID != a || got[1].ID != b {
		t.Fatalf("order = %+v, want ids [%d %d]", got, a, b)
	}
}

func TestUpdateCardRewritesIdAndGame(t *testing.T) {
	db := testDB(t)
	id, _ := insertCard(db, 0, "sv01-020", "pokemon", "tcg", false)
	c, _ := getCard(db, id)
	c.CardID = "OP01-025"
	c.Game = "onepiece"
	c.Foil = true
	if err := updateCard(db, c); err != nil {
		t.Fatal(err)
	}
	got, _ := getCard(db, id)
	if got.CardID != "OP01-025" || got.Game != "onepiece" || !got.Foil {
		t.Fatalf("after update: %+v", got)
	}
}
