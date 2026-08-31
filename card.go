package main

import (
	"database/sql"
	"fmt"
)

type CollectionCard struct {
	ID           int    `json:"id"`
	CardID       string `json:"card_id"`
	Game         string `json:"game"`
	CollectionID int    `json:"collection_id"` // 0 = not in a custom collection
	Foil         bool   `json:"foil"`
	ModelName    string `json:"model_name"`
	ScannedAt    string `json:"scanned_at"`
}

// CardFilter selects one view: a game collection, the unknown-game bucket,
// or a custom collection. Exactly one selector must be set.
type CardFilter struct {
	Game         string `json:"game,omitempty"`
	OtherBucket  bool   `json:"other_bucket,omitempty"`
	CollectionID int    `json:"collection_id,omitempty"`
}

func cardFilterWhere(f CardFilter) (string, []any, error) {
	set := 0
	if f.Game != "" {
		set++
	}
	if f.OtherBucket {
		set++
	}
	if f.CollectionID != 0 {
		set++
	}
	if set != 1 {
		return "", nil, fmt.Errorf("card filter must set exactly one of game, other_bucket, collection_id")
	}
	switch {
	case f.Game != "":
		return "game = ?", []any{f.Game}, nil
	case f.OtherBucket:
		return "game = ''", nil, nil
	default:
		return "collection_id = ?", []any{f.CollectionID}, nil
	}
}

func nullableID(id int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(id), Valid: id > 0}
}

func insertCard(db *sql.DB, collectionID int, cardID, game, modelName string, foil bool) (int, error) {
	foilInt := 0
	if foil {
		foilInt = 1
	}
	res, err := db.Exec(
		`INSERT INTO cards (collection_id, card_id, game, foil, model_name) VALUES (?, ?, ?, ?, ?)`,
		nullableID(collectionID), cardID, game, foilInt, modelName,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func scanCard(row interface{ Scan(...any) error }) (CollectionCard, error) {
	var c CollectionCard
	var foilInt int
	var cid sql.NullInt64
	err := row.Scan(&c.ID, &c.CardID, &c.Game, &cid, &foilInt, &c.ModelName, &c.ScannedAt)
	if err != nil {
		return c, err
	}
	c.Foil = foilInt != 0
	if cid.Valid {
		c.CollectionID = int(cid.Int64)
	}
	return c, nil
}

const cardColumns = `id, card_id, game, collection_id, foil, model_name, scanned_at`

func getCard(db *sql.DB, id int) (CollectionCard, error) {
	return scanCard(db.QueryRow(`SELECT `+cardColumns+` FROM cards WHERE id = ?`, id))
}

func listCards(db *sql.DB, f CardFilter) ([]CollectionCard, error) {
	where, args, err := cardFilterWhere(f)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT `+cardColumns+` FROM cards WHERE `+where+` ORDER BY scanned_at ASC, id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cards []CollectionCard
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

func moveCardToCustom(db *sql.DB, rowID, customID int) error {
	_, err := db.Exec(`UPDATE cards SET collection_id = ? WHERE id = ?`, nullableID(customID), rowID)
	return err
}

func updateCard(db *sql.DB, c CollectionCard) error {
	foilInt := 0
	if c.Foil {
		foilInt = 1
	}
	_, err := db.Exec(`UPDATE cards SET foil = ?, card_id = ?, game = ? WHERE id = ?`,
		foilInt, c.CardID, c.Game, c.ID)
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
