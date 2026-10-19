package main

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMigrateSyncGivesEveryRowAUidOnce(t *testing.T) {
	db := testDB(t) // openDB migrates, so uids already exist: insert rows the old way, then migrate again
	db.Exec(`INSERT INTO collections (name, uid) VALUES ('To sell', '')`)
	db.Exec(`INSERT INTO cards (card_id, game, uid) VALUES ('OP01-006_p4', 'onepiece', '')`)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var uids []string
	rows, _ := db.Query(`SELECT uid FROM collections UNION ALL SELECT uid FROM cards`)
	for rows.Next() {
		var u string
		rows.Scan(&u)
		uids = append(uids, u)
	}
	rows.Close()
	for _, u := range uids {
		if len(u) != 36 {
			t.Errorf("uid %q", u)
		}
	}
	first := uids[0]
	migrate(db)
	var again string
	db.QueryRow(`SELECT uid FROM collections`).Scan(&again)
	if again != first {
		t.Error("a second migration must keep uids")
	}
	id, name, err := syncDevice(db)
	if err != nil || len(id) != 36 || name == "" {
		t.Errorf("device %q %q %v", id, name, err)
	}
	id2, _, _ := syncDevice(db)
	if id2 != id {
		t.Error("device id must be stable")
	}
}

func TestBootstrapEventsAreWrittenOnceInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, _ := sql.Open("sqlite", path) // a pre-sync database: schema without the sync additions
	raw.Exec(`CREATE TABLE collections (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
	raw.Exec(`CREATE TABLE cards (id INTEGER PRIMARY KEY AUTOINCREMENT, collection_id INTEGER, card_id TEXT NOT NULL, game TEXT NOT NULL DEFAULT '', foil INTEGER NOT NULL DEFAULT 0, lang TEXT NOT NULL DEFAULT 'en', model_name TEXT NOT NULL DEFAULT '', scanned_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
	raw.Exec(`INSERT INTO collections (name, created_at) VALUES ('To sell', '2026-10-01 10:00:00')`)
	raw.Exec(`INSERT INTO cards (collection_id, card_id, game, foil, lang, model_name, scanned_at) VALUES (1, 'OP01-006_p4', 'onepiece', 1, 'ja', 'tcg', '2026-09-30 18:41:10'), (NULL, 'sv01-020', 'pokemon', 0, 'en', 'tcg', '2026-10-02 09:00:00')`)
	raw.Close()
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setPriceRef(db, PriceRef{CardID: "OP01-006_p4", Product: 799296, Label: "Jolly Roger Foil", ChosenAt: "2026-10-08T20:00:00Z"}) // after migration: recorded as a normal event
	content, last, err := renderJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	_, events, skipped, err := decodeJournal(strings.NewReader(content))
	if err != nil || skipped != 0 {
		t.Fatal(err, skipped)
	}
	ops := []string{}
	for _, e := range events {
		ops = append(ops, e.Op)
	}
	want := []string{opCollectionCreate, opCopyAdd, opCopyAdd, opPriceRefSet}
	if !slices.Equal(ops, want) || last != 4 {
		t.Fatalf("ops %v last %d, want %v 4", ops, last, want)
	}
	if events[0].At != "2026-10-01T10:00:00.000Z" || events[1].At != "2026-09-30T18:41:10.000Z" || events[1].ScannedAt != events[1].At {
		t.Errorf("bootstrap dates: %+v %+v", events[0], events[1])
	}
	if *events[1].Foil != true || events[1].Lang != "ja" || *events[1].Collection != events[0].UID || *events[2].Collection != "" {
		t.Errorf("bootstrap copy fields: %+v %+v", events[1], events[2])
	}
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Errorf("seq %d at %d", e.Seq, i)
		}
	}
	migrate(db)
	if _, last2, _ := renderJournal(db); last2 != 4 {
		t.Errorf("a second migration re-bootstrapped: %d events", last2)
	}
}
