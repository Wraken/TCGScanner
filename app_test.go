package main

import "testing"

// UpdateCard only re-resolves the game when the card id itself changed: an
// unrelated edit (foil toggle) must never downgrade a stored game, even if
// the caller sends a bogus Game field or a manifest shrank since the scan.
func TestUpdateCardKeepsGameWhenIdUnchanged(t *testing.T) {
	app := &App{db: testDB(t)}
	id, err := insertCard(app.db, 0, "sv01-020", "pokemon", "tcg", false, "en")
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

// stubModel satisfies Predictor for bindings that only need a model name.
type stubModel struct{ name string }

func (s stubModel) Predict(string) (*Prediction, error) { return nil, nil }
func (s stubModel) Name() string                        { return s.name }
func (s stubModel) Delete()                             {}

func TestScanLang(t *testing.T) {
	app := &App{db: testDB(t)}
	if got := app.GetScanLang(); got != "en" {
		t.Errorf("initial scan lang = %q, want en", got)
	}
	if err := app.SetScanLang("xx"); err == nil {
		t.Error("unknown language accepted")
	}
	if got := app.GetScanLang(); got != "en" {
		t.Errorf("scan lang after rejected set = %q, want en", got)
	}
	if err := app.SetScanLang("fr"); err != nil {
		t.Fatal(err)
	}
	if got := app.GetScanLang(); got != "fr" {
		t.Errorf("scan lang = %q, want fr", got)
	}
	if n := len(app.ListLanguages()); n != len(cardLanguages) {
		t.Errorf("ListLanguages returned %d entries, want %d", n, len(cardLanguages))
	}
}

func TestSaveCardStampsScanLang(t *testing.T) {
	app := &App{db: testDB(t), model: stubModel{name: "tcg"}}
	if err := app.SetScanLang("ja"); err != nil {
		t.Fatal(err)
	}
	saved, err := app.SaveCard("OP01-025", false)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Lang != "ja" {
		t.Errorf("saved lang = %q, want ja", saved.Lang)
	}
}

func TestUpdateCardValidatesLang(t *testing.T) {
	app := &App{db: testDB(t)}
	id, err := insertCard(app.db, 0, "sv01-020", "pokemon", "tcg", false, "en")
	if err != nil {
		t.Fatal(err)
	}
	card, err := getCard(app.db, id)
	if err != nil {
		t.Fatal(err)
	}
	card.Lang = "xx"
	if err := app.UpdateCard(card); err == nil {
		t.Error("unknown language accepted by UpdateCard")
	}
	got, _ := getCard(app.db, id)
	if got.Lang != "en" {
		t.Errorf("lang after rejected update = %q, want en", got.Lang)
	}
	card.Lang = "de"
	if err := app.UpdateCard(card); err != nil {
		t.Fatal(err)
	}
	got, _ = getCard(app.db, id)
	if got.Lang != "de" {
		t.Errorf("lang after update = %q, want de", got.Lang)
	}
}
