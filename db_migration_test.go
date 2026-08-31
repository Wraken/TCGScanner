package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// v1Schema is the pre-2026-08-31 layout: NOT NULL collection_id, no game
// column, started_at instead of created_at.
const v1Schema = `
	CREATE TABLE collections (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT    NOT NULL DEFAULT '',
		started_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE cards (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_id INTEGER NOT NULL REFERENCES collections(id),
		card_id       TEXT    NOT NULL,
		foil          INTEGER NOT NULL DEFAULT 0,
		model_name    TEXT    NOT NULL DEFAULT '',
		scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
	);
`

// buildEmptyV1DB creates a database with the v1 schema and no rows.
func buildEmptyV1DB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(v1Schema); err != nil {
		t.Fatal(err)
	}
	return path
}

// buildV1DB creates a database with the pre-2026-08-31 schema and some rows:
// collection 1 = empty unnamed session, collection 2 = unnamed with 2 cards,
// collection 3 = named "Trades" with 1 card.
func buildV1DB(t *testing.T) string {
	t.Helper()
	path := buildEmptyV1DB(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := `
		INSERT INTO collections (name) VALUES (''), (''), ('Trades');
		INSERT INTO cards (collection_id, card_id, foil, model_name) VALUES
			(2, 'OP01-025', 0, 'tcg'),
			(2, 'ogn-045-123', 1, 'tcg'),
			(3, 'sv01-020', 0, 'tcg');
	`
	if _, err := db.Exec(stmts); err != nil {
		t.Fatal(err)
	}
	return path
}

func columnInfo(t *testing.T, db *sql.DB, table string) map[string]struct {
	notNull bool
} {
	t.Helper()
	rows, err := db.Query(`SELECT name, "notnull" FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]struct{ notNull bool }{}
	for rows.Next() {
		var name string
		var nn int
		if err := rows.Scan(&name, &nn); err != nil {
			t.Fatal(err)
		}
		out[name] = struct{ notNull bool }{nn != 0}
	}
	return out
}

func TestMigrationV1ToV2(t *testing.T) {
	path := buildV1DB(t)
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cards := columnInfo(t, db, "cards")
	if _, ok := cards["game"]; !ok {
		t.Fatal("cards.game column missing after migration")
	}
	if cards["collection_id"].notNull {
		t.Fatal("cards.collection_id should be nullable after migration")
	}
	cols := columnInfo(t, db, "collections")
	if _, ok := cols["created_at"]; !ok {
		t.Fatal("collections.created_at missing (rename from started_at)")
	}

	// Empty unnamed session deleted; unnamed-with-cards renamed; named kept.
	rows, err := db.Query(`SELECT id, name FROM collections ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int]string{}
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		got[id] = name
	}
	if len(got) != 2 {
		t.Fatalf("want 2 collections after migration, got %v", got)
	}
	if got[2] != "Collection 2" {
		t.Errorf("collection 2 name = %q, want %q", got[2], "Collection 2")
	}
	if got[3] != "Trades" {
		t.Errorf("collection 3 name = %q, want %q", got[3], "Trades")
	}

	// Existing cards kept, with game backfill target '' for now.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cards WHERE game = ''`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("want 3 cards with empty game, got %d", n)
	}

	// A migrated card's full row survives the rebuild.
	var cid, foil int
	var model string
	if err := db.QueryRow(`SELECT collection_id, foil, model_name FROM cards WHERE card_id = 'ogn-045-123'`).
		Scan(&cid, &foil, &model); err != nil {
		t.Fatal(err)
	}
	if cid != 2 || foil != 1 || model != "tcg" {
		t.Errorf("migrated card = (collection_id %d, foil %d, model %q), want (2, 1, %q)", cid, foil, model, "tcg")
	}
}

func TestMigrationEmptyV1DB(t *testing.T) {
	path := buildEmptyV1DB(t)
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"collections", "cards"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("want empty %s table after migrating empty v1 DB, got %d rows", table, n)
		}
	}
}

func TestMigrationIdempotent(t *testing.T) {
	path := buildV1DB(t)
	for i := range 2 {
		db, err := openDB(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		db.Close()
	}
}

// The v1 cleanup (delete empty unnamed collections, rename the rest) must
// not run on an already-v2 database: a v2-era unnamed collection stays as-is.
func TestUnnamedV2CollectionSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO collections (name) VALUES ('')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM collections`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 collection after reopen, got %d", n)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM collections`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "" {
		t.Errorf("unnamed v2 collection renamed to %q on reopen, want unchanged empty name", name)
	}
}

func TestFreshDBSchemaV2(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// A copy can exist with no custom collection.
	if _, err := db.Exec(`INSERT INTO cards (collection_id, card_id, game) VALUES (NULL, 'sv01-020', 'pokemon')`); err != nil {
		t.Fatalf("insert with NULL collection_id: %v", err)
	}
}

func TestDeleteCollectionSetsNull(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "fk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO collections (name) VALUES ('Deck')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cards (collection_id, card_id, game) VALUES (1, 'a', 'pokemon')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM collections WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var cid sql.NullInt64
	if err := db.QueryRow(`SELECT collection_id FROM cards WHERE card_id = 'a'`).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	if cid.Valid {
		t.Fatalf("collection_id should be NULL after deleting the collection, got %d", cid.Int64)
	}
}
