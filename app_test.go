package main

import "testing"

// UpdateCard only re-resolves the game when the card id itself changed: an
// unrelated edit (foil toggle) must never downgrade a stored game, even if
// the caller sends a bogus Game field or a manifest shrank since the scan.
func TestUpdateCardKeepsGameWhenIdUnchanged(t *testing.T) {
	app := &App{db: testDB(t)}
	id, err := insertCard(app.db, 0, "sv01-020", "pokemon", "tcg", false)
	if err != nil {
		t.Fatal(err)
	}

	card, err := getCard(app.db, id)
	if err != nil {
		t.Fatal(err)
	}
	card.Foil = true
	card.Game = "garbage" // callers cannot force a game
	if err := app.UpdateCard(card); err != nil {
		t.Fatal(err)
	}
	got, err := getCard(app.db, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Game != "pokemon" || !got.Foil {
		t.Errorf("after foil toggle: game = %q foil = %t, want pokemon true", got.Game, got.Foil)
	}

	// A hand-corrected id unknown to every manifest resolves to "".
	card = got
	card.CardID = "zz99-999"
	if err := app.UpdateCard(card); err != nil {
		t.Fatal(err)
	}
	got, err = getCard(app.db, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.CardID != "zz99-999" || got.Game != "" {
		t.Errorf("after id change: card_id = %q game = %q, want zz99-999 \"\"", got.CardID, got.Game)
	}
}
