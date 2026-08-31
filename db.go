package main

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

// Schema v2 (2026-08-31): per-game collections are DERIVED (GROUP BY
// cards.game); the collections table holds only user-named custom
// collections, and a copy belongs to at most one of them.
const schemaV2 = `
	CREATE TABLE IF NOT EXISTS collections (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT    NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS cards (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_id INTEGER REFERENCES collections(id) ON DELETE SET NULL,
		card_id       TEXT    NOT NULL,
		game          TEXT    NOT NULL DEFAULT '',
		foil          INTEGER NOT NULL DEFAULT 0,
		model_name    TEXT    NOT NULL DEFAULT '',
		scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
	);
`

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
	return db, nil
}

func columnNotNull(db *sql.DB, table, column string) (bool, error) {
	var nn int
	err := db.QueryRow(`SELECT "notnull" FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&nn)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return nn != 0, err
}

// migrate brings any database (fresh, or any older layout) to schema v2.
// Idempotent: every step is a no-op on an already-migrated database.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaV2); err != nil {
		return err
	}

	// Pre-model_name databases (before the sessions->collections rename era).
	_, _ = db.Exec(`ALTER TABLE cards ADD COLUMN model_name TEXT NOT NULL DEFAULT ''`)
	// v1 -> v2 additions that ALTER can handle.
	_, _ = db.Exec(`ALTER TABLE cards ADD COLUMN game TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE collections RENAME COLUMN started_at TO created_at`)

	// v1 declared collection_id NOT NULL; SQLite cannot relax that in place,
	// so rebuild the table. This branch is the one-shot v1 -> v2 migration:
	// it runs at most once per database, and the session cleanup below must
	// stay inside it so v2-era unnamed collections are left alone.
	notNull, err := columnNotNull(db, "cards", "collection_id")
	if err != nil {
		return err
	}
	if notNull {
		if err := rebuildCards(db); err != nil {
			return err
		}
		// Hand-edited v1 databases could hold dangling collection ids; the
		// migration runs with foreign keys off, so clear them explicitly
		// (before the empty-unnamed DELETE, so a dangling ref does not keep
		// an unnamed collection's id "in use").
		if _, err := db.Exec(`UPDATE cards SET collection_id = NULL WHERE collection_id NOT IN (SELECT id FROM collections)`); err != nil {
			return err
		}
		// Old auto-created scan sessions: drop the empty unnamed ones,
		// promote the unnamed ones that hold cards to regular named customs.
		if _, err := db.Exec(`
			DELETE FROM collections WHERE name = '' AND id NOT IN (
				SELECT DISTINCT collection_id FROM cards WHERE collection_id IS NOT NULL
			)`); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE collections SET name = 'Collection ' || id WHERE name = ''`); err != nil {
			return err
		}
	}
	return nil
}

func rebuildCards(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`CREATE TABLE cards_v2 (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			collection_id INTEGER REFERENCES collections(id) ON DELETE SET NULL,
			card_id       TEXT    NOT NULL,
			game          TEXT    NOT NULL DEFAULT '',
			foil          INTEGER NOT NULL DEFAULT 0,
			model_name    TEXT    NOT NULL DEFAULT '',
			scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO cards_v2 (id, collection_id, card_id, game, foil, model_name, scanned_at)
			SELECT id, collection_id, card_id, game, foil, model_name, scanned_at FROM cards`,
		`DROP TABLE cards`,
		`ALTER TABLE cards_v2 RENAME TO cards`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return tx.Commit()
}
