package main

import (
	"database/sql"
	"encoding/csv"
	"os"
	"strconv"
)

type CollectionCard struct {
	ID        int    `json:"id"`
	CardID    string `json:"card_id"`
	Foil      bool   `json:"foil"`
	ScannedAt string `json:"scanned_at"`
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
