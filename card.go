package main

import (
	"database/sql"
)

type CollectionCard struct {
	ID        int    `json:"id"`
	CardID    string `json:"card_id"`
	Foil      bool   `json:"foil"`
	ModelName string `json:"model_name"`
	ScannedAt string `json:"scanned_at"`
}

func insertCard(db *sql.DB, collectionID int, cardID, modelName string, foil bool) error {
	foilInt := 0
	if foil {
		foilInt = 1
	}
	_, err := db.Exec(
		`INSERT INTO cards (collection_id, card_id, foil, model_name) VALUES (?, ?, ?, ?)`,
		collectionID, cardID, foilInt, modelName,
	)
	return err
}

func listCollectionCards(db *sql.DB, collectionID int) ([]CollectionCard, error) {
	rows, err := db.Query(`
		SELECT id, card_id, foil, model_name, scanned_at
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
		if err := rows.Scan(&c.ID, &c.CardID, &foilInt, &c.ModelName, &c.ScannedAt); err != nil {
			return nil, err
		}
		c.Foil = foilInt != 0
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

func updateCard(db *sql.DB, c CollectionCard) error {
	foilInt := 0
	if c.Foil {
		foilInt = 1
	}
	_, err := db.Exec(`UPDATE cards SET foil = ? WHERE id = ?`, foilInt, c.ID)
	return err
}

func deleteCard(db *sql.DB, id int) error {
	_, err := db.Exec(`DELETE FROM cards WHERE id = ?`, id)
	return err
}
