package main

import (
	"database/sql"
	"encoding/csv"
	"os"
	"strconv"

	_ "modernc.org/sqlite"
)

type Collection struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	StartedAt string `json:"started_at"`
	CardCount int    `json:"card_count"`
}

type CollectionCard struct {
	ID        int    `json:"id"`
	CardID    string `json:"card_id"`
	Foil      bool   `json:"foil"`
	ScannedAt string `json:"scanned_at"`
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	_, _ = db.Exec(`PRAGMA foreign_keys = ON`)

	// Migrations for existing databases (no-op if already renamed)
	_, _ = db.Exec(`ALTER TABLE sessions RENAME TO collections`)
	_, _ = db.Exec(`ALTER TABLE cards RENAME COLUMN session_id TO collection_id`)

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
			scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		db.Close()
		return nil, err
	}
	// Migration: add name column to existing databases (no-op if already present)
	_, _ = db.Exec(`ALTER TABLE collections ADD COLUMN name TEXT NOT NULL DEFAULT ''`)
	return db, nil
}

func createCollection(db *sql.DB) (int, error) {
	res, err := db.Exec(`INSERT INTO collections DEFAULT VALUES`)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func insertCard(db *sql.DB, collectionID int, cardID string, foil bool) error {
	foilInt := 0
	if foil {
		foilInt = 1
	}
	_, err := db.Exec(
		`INSERT INTO cards (collection_id, card_id, foil) VALUES (?, ?, ?)`,
		collectionID, cardID, foilInt,
	)
	return err
}

func getLastCollection(db *sql.DB) (Collection, error) {
	var c Collection
	err := db.QueryRow(`
		SELECT co.id, co.name, co.started_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		GROUP BY co.id
		ORDER BY co.started_at DESC
		LIMIT 1
	`).Scan(&c.ID, &c.Name, &c.StartedAt, &c.CardCount)
	return c, err
}

func getCollection(db *sql.DB, id int) (Collection, error) {
	var c Collection
	err := db.QueryRow(`
		SELECT co.id, co.name, co.started_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		WHERE co.id = ?
		GROUP BY co.id
	`, id).Scan(&c.ID, &c.Name, &c.StartedAt, &c.CardCount)
	return c, err
}

func listCollections(db *sql.DB) ([]Collection, error) {
	rows, err := db.Query(`
		SELECT co.id, co.name, co.started_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		GROUP BY co.id
		ORDER BY co.started_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.StartedAt, &c.CardCount); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

func listCollectionCards(db *sql.DB, collectionID int) ([]CollectionCard, error) {
	rows, err := db.Query(`
		SELECT id, card_id, foil, scanned_at
		FROM cards
		WHERE collection_id = ?
		ORDER BY scanned_at ASC
	`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cards []CollectionCard
	for rows.Next() {
		var c CollectionCard
		var foilInt int
		if err := rows.Scan(&c.ID, &c.CardID, &foilInt, &c.ScannedAt); err != nil {
			return nil, err
		}
		c.Foil = foilInt != 0
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

func deleteCard(db *sql.DB, id int) error {
	_, err := db.Exec(`DELETE FROM cards WHERE id = ?`, id)
	return err
}

func renameCollection(db *sql.DB, id int, name string) error {
	_, err := db.Exec(`UPDATE collections SET name = ? WHERE id = ?`, name, id)
	return err
}

func writeCollectionCSV(db *sql.DB, collectionID int, path string) error {
	rows, err := db.Query(`
		SELECT card_id,
		       SUM(CASE WHEN foil = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN foil = 1 THEN 1 ELSE 0 END)
		FROM cards
		WHERE collection_id = ?
		GROUP BY card_id
		ORDER BY card_id ASC
	`, collectionID)
	if err != nil {
		return err
	}
	defer rows.Close()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)

	w.Write([]string{"Normal Count", "Foil Count", "Card ID"})
	for rows.Next() {
		var cardID string
		var normalCount, foilCount int
		if err := rows.Scan(&cardID, &normalCount, &foilCount); err != nil {
			return err
		}
		w.Write([]string{
			strconv.Itoa(normalCount),
			strconv.Itoa(foilCount),
			cardID,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	w.Flush()
	return w.Error()
}
