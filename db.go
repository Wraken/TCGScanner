package main

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	_, _ = db.Exec(`PRAGMA foreign_keys = ON`)

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS collections (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT    NOT NULL DEFAULT '',
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS cards (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			collection_id INTEGER NOT NULL REFERENCES collections(id),
			card_id       TEXT    NOT NULL,
			foil          INTEGER NOT NULL DEFAULT 0,
			model_name    TEXT    NOT NULL DEFAULT '',
			scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	// Migration: add model_name to existing databases (no-op if column already exists)
	_, _ = db.Exec(`ALTER TABLE cards ADD COLUMN model_name TEXT NOT NULL DEFAULT ''`)

	return db, nil
}
