package main

import (
	"path/filepath"
	"testing"
)

func TestBackfillCardGames(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "bf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO cards (card_id, game) VALUES ('OP01-025', '')`)
	mustExec(`INSERT INTO cards (card_id, game) VALUES ('OP01-025', '')`) // duplicate copy
	mustExec(`INSERT INTO cards (card_id, game) VALUES ('mystery-1', '')`)
	mustExec(`INSERT INTO cards (card_id, game) VALUES ('sv01-020', 'pokemon')`) // already set

	resolve := func(id string) string {
		if id == "OP01-025" {
			return "onepiece"
		}
		return ""
	}
	n, err := backfillCardGames(db, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("backfill updated %d rows, want 2", n)
	}
	var unknown int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cards WHERE game = ''`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if unknown != 1 {
		t.Errorf("%d rows still unknown, want 1 (mystery-1)", unknown)
	}

	// Backfill runs on every startup: a second pass must be a clean no-op.
	n, err = backfillCardGames(db, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("second backfill updated %d rows, want 0", n)
	}
}
