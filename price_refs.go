package main

import (
	"database/sql"
	"time"
)

// PriceRef is the owner's choice of Cardmarket product for one card id,
// made in the price reference selector among the candidates the bundle
// ships (docs/superpowers/specs/2026-10-08-price-reference-selector-design.md).
// It outranks the bundle's own mapping for that id.
type PriceRef struct {
	CardID     string `json:"card_id"`
	Product    int    `json:"product"`
	NonEnglish int    `json:"non_english"`
	Label      string `json:"label"`
	ChosenAt   string `json:"chosen_at"` // RFC 3339, UTC
}

func getPriceRefs(db *sql.DB) (map[string]PriceRef, error) {
	rows, err := db.Query(`SELECT card_id, product, non_english, label, chosen_at FROM price_refs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]PriceRef{}
	for rows.Next() {
		var r PriceRef
		if err := rows.Scan(&r.CardID, &r.Product, &r.NonEnglish, &r.Label, &r.ChosenAt); err != nil {
			return nil, err
		}
		out[r.CardID] = r
	}
	return out, rows.Err()
}

func setPriceRef(db *sql.DB, r PriceRef) error {
	if r.ChosenAt == "" {
		r.ChosenAt = time.Now().UTC().Format(time.RFC3339)
	}
	deviceID, _, err := syncDevice(db)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Stamped after the reference's current key (its row, else its last
	// clear), so this choice outranks every change this device has seen.
	keyAt, _, _, err := priceRefKey(tx, r.CardID)
	if err != nil {
		return err
	}
	at := bumpSyncAt(priceRefSyncAt(r.ChosenAt, syncNow()), keyAt)
	if _, err := tx.Exec(`INSERT INTO price_refs (card_id, product, non_english, label, chosen_at, sync_at, sync_by) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(card_id) DO UPDATE SET product = excluded.product, non_english = excluded.non_english, label = excluded.label, chosen_at = excluded.chosen_at, sync_at = excluded.sync_at, sync_by = excluded.sync_by`,
		r.CardID, r.Product, r.NonEnglish, r.Label, r.ChosenAt, at, deviceID); err != nil {
		return err
	}
	if err := appendSyncEvent(tx, priceRefEvent(r, at)); err != nil {
		return err
	}
	return tx.Commit()
}

// clearPriceRef deletes the reference and remembers the clear's key in
// sync_ref_clears, so an older price_ref.set pulled later stays out.
func clearPriceRef(db *sql.DB, cardID string) error {
	deviceID, _, err := syncDevice(db)
	if err != nil {
		return err
	}
	now := syncNow()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keyAt, _, _, err := priceRefKey(tx, cardID)
	if err != nil {
		return err
	}
	at := bumpSyncAt(now, keyAt)
	if _, err := tx.Exec(`DELETE FROM price_refs WHERE card_id = ?`, cardID); err != nil {
		return err
	}
	if err := upsertRefClear(tx, cardID, at, deviceID); err != nil {
		return err
	}
	if err := appendSyncEvent(tx, syncEvent{Op: opPriceRefClear, At: at, CardID: cardID}); err != nil {
		return err
	}
	return tx.Commit()
}
