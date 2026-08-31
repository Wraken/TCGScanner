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

// backfillCardGames fills cards.game for rows where it is still unknown.
// Runs on every startup: cards scanned before a game's images were installed
// heal themselves once the manifest knows their id.
func backfillCardGames(db *sql.DB, resolve func(cardID string) string) (int, error) {
	rows, err := db.Query(`SELECT DISTINCT card_id FROM cards WHERE game = ''`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// One transaction for the whole pass: on the first post-migration run
	// every row is unknown, and per-id autocommit UPDATEs (a journal commit
	// plus a full table scan each) would stall startup for seconds.
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	total := 0
	for _, id := range ids {
		g := resolve(id)
		if g == "" {
			continue
		}
		res, err := tx.Exec(`UPDATE cards SET game = ? WHERE card_id = ? AND game = ''`, g, id)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}
