package main

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Folding other devices' events into the rows: the batch order, the
// last-writer-wins key, and the uid-keyed row functions (syncTombstone,
// deleteCollectionRow, priceRefKey and upsertRefClear also serve the local
// write paths). See the collection sync design, "Merge rules".

// syncKindRank orders a batch by entity kind: collections, then copies, then
// price references, so a copy always finds its collection whatever the ats.
func syncKindRank(op string) int {
	switch {
	case strings.HasPrefix(op, "collection."):
		return 0
	case strings.HasPrefix(op, "copy."):
		return 1
	}
	return 2
}

// syncKeyAfter is the whole last-writer-wins rule: the key (at, by) beats the
// key (rowAt, rowBy) when it is strictly greater, at first then device id. at
// is fixed-width, so string order is time order; equal keys (an event applied
// again) do not win.
func syncKeyAfter(at, by, rowAt, rowBy string) bool {
	if at != rowAt {
		return at > rowAt
	}
	return by > rowBy
}

// applySyncEvents folds a batch of other devices' events into the rows, in
// one transaction: kind by kind (collections, copies, price references), each
// kind in (at, device, seq) order. Last writer wins is per entity, so the
// kind order only changes one thing: a copy finds its collection even when
// it was filed before the collection's create (bootstrap, clock skew). It records
// nothing in sync_events, advances sync_sources.applied_seq per device to
// the highest seq in the batch, and returns how many events changed a row.
func applySyncEvents(db *sql.DB, batch []syncSourced, resolveGame func(cardID string) string) (applied int, err error) {
	maxSeq := map[string]int64{}
	events := make([]syncSourced, 0, len(batch))
	for _, s := range batch {
		if s.Device == "" {
			fmt.Printf("sync: skipped event %d: no device id\n", s.Event.Seq)
			continue
		}
		if seq, ok := maxSeq[s.Device]; !ok || s.Event.Seq > seq {
			maxSeq[s.Device] = s.Event.Seq
		}
		if _, err := parseSyncAt(s.Event.At); err != nil {
			fmt.Printf("sync: skipped event %d of device %s: %v\n", s.Event.Seq, s.Device, err)
			continue
		}
		events = append(events, s)
	}
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if ra, rb := syncKindRank(a.Event.Op), syncKindRank(b.Event.Op); ra != rb {
			return ra < rb
		}
		if a.Event.At != b.Event.At {
			return a.Event.At < b.Event.At
		}
		if a.Device != b.Device {
			return a.Device < b.Device
		}
		return a.Event.Seq < b.Event.Seq
	})

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, s := range events {
		changed, err := applySyncEvent(tx, s, resolveGame)
		if err != nil {
			return 0, fmt.Errorf("sync: applying %s (seq %d) of device %s: %w", s.Event.Op, s.Event.Seq, s.Device, err)
		}
		if changed {
			applied++
		}
	}
	for device, seq := range maxSeq {
		if _, err := tx.Exec(`INSERT INTO sync_sources (device_id, applied_seq) VALUES (?, ?)
			ON CONFLICT(device_id) DO UPDATE SET applied_seq = excluded.applied_seq`, device, seq); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return applied, nil
}

// applySyncEvent applies one foreign event; changed reports whether it wrote
// a row (insert, update, delete, detach). It never records an event: pulled
// events are not re-emitted.
func applySyncEvent(tx *sql.Tx, s syncSourced, resolveGame func(string) string) (changed bool, err error) {
	e := s.Event
	switch e.Op {
	case opCollectionCreate, opCollectionRename, opCollectionDelete, opCopyAdd, opCopyUpdate, opCopyRemove:
		if e.UID == "" {
			fmt.Printf("sync: skipped %s (seq %d) of device %s: no uid\n", e.Op, e.Seq, s.Device)
			return false, nil
		}
		var one int
		err := tx.QueryRow(`SELECT 1 FROM sync_tombstones WHERE uid = ?`, e.UID).Scan(&one)
		if err == nil {
			return false, nil // removals are final
		} else if err != sql.ErrNoRows {
			return false, err
		}
	}
	switch e.Op {
	case opCollectionCreate, opCollectionRename:
		return applyCollectionEvent(tx, s)
	case opCollectionDelete:
		return applyCollectionDelete(tx, e)
	case opCopyAdd, opCopyUpdate:
		return applyCopyEvent(tx, s, resolveGame)
	case opCopyRemove:
		return applyCopyRemove(tx, e)
	case opPriceRefSet:
		return applyPriceRefSet(tx, s)
	case opPriceRefClear:
		return applyPriceRefClear(tx, s)
	}
	fmt.Printf("sync: skipped event %d of device %s: unexpected op %q\n", e.Seq, s.Device, e.Op)
	return false, nil
}

// syncRowKey reads the id and last-writer-wins key of the row uid of table.
func syncRowKey(tx *sql.Tx, table, uid string) (id int64, at, by string, found bool, err error) {
	err = tx.QueryRow(`SELECT id, sync_at, sync_by FROM `+table+` WHERE uid = ?`, uid).Scan(&id, &at, &by)
	if err == sql.ErrNoRows {
		return 0, "", "", false, nil
	}
	return id, at, by, err == nil, err
}

// syncTombstone records that uid was removed: later events for it are ignored.
func syncTombstone(tx syncDB, uid, at string) error {
	_, err := tx.Exec(`INSERT OR REPLACE INTO sync_tombstones (uid, deleted_at) VALUES (?, ?)`, uid, at)
	return err
}

// applyCollectionEvent: collection.create inserts an unknown uid and ignores a
// known one; collection.rename renames when its key wins. A rename of a uid
// not seen yet creates the collection, so the later create is the one ignored
// and every device ends with the same name.
func applyCollectionEvent(tx *sql.Tx, s syncSourced) (bool, error) {
	e := s.Event
	id, rowAt, rowBy, found, err := syncRowKey(tx, "collections", e.UID)
	if err != nil {
		return false, err
	}
	if !found {
		_, err := tx.Exec(`INSERT INTO collections (name, created_at, uid, sync_at, sync_by) VALUES (?, ?, ?, ?, ?)`,
			e.Name, syncAtToSQLite(e.At), e.UID, e.At, s.Device)
		return err == nil, err
	}
	if e.Op == opCollectionCreate || !syncKeyAfter(e.At, s.Device, rowAt, rowBy) {
		return false, nil
	}
	_, err = tx.Exec(`UPDATE collections SET name = ?, sync_at = ?, sync_by = ? WHERE id = ?`, e.Name, e.At, s.Device, id)
	return err == nil, err
}

// applyCollectionDelete detaches the collection's copies, deletes it and
// tombstones its uid, whatever the keys: a removal is final.
func applyCollectionDelete(tx *sql.Tx, e syncEvent) (bool, error) {
	id, _, _, found, err := syncRowKey(tx, "collections", e.UID)
	if err != nil {
		return false, err
	}
	if found {
		if err := deleteCollectionRow(tx, id); err != nil {
			return false, err
		}
	}
	return found, syncTombstone(tx, e.UID, e.At)
}

// deleteCollectionRow detaches the collection's copies (they become unfiled)
// and deletes it. The explicit UPDATE makes this independent of the
// connection's foreign_keys pragma.
func deleteCollectionRow(tx syncDB, id int64) error {
	if _, err := tx.Exec(`UPDATE cards SET collection_id = NULL WHERE collection_id = ?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM collections WHERE id = ?`, id)
	return err
}

// syncCollectionID resolves a collection uid to its row id; NULL (unfiled)
// for "" or a uid this device does not have.
func syncCollectionID(tx *sql.Tx, uid string) (sql.NullInt64, error) {
	if uid == "" {
		return sql.NullInt64{}, nil
	}
	var id int64
	err := tx.QueryRow(`SELECT id FROM collections WHERE uid = ?`, uid).Scan(&id)
	if err == sql.ErrNoRows {
		return sql.NullInt64{}, nil
	}
	return sql.NullInt64{Int64: id, Valid: err == nil}, err
}

// applyCopyEvent: copy.add inserts an unknown uid and ignores a known one;
// copy.update rewrites what can change when its key wins. An update of a uid
// not seen yet inserts the copy (model and scan date unknown), so the later
// add is the one ignored and every device converges on the update.
func applyCopyEvent(tx *sql.Tx, s syncSourced, resolveGame func(string) string) (bool, error) {
	e := s.Event
	var (
		id           int64
		rowAt, rowBy string
		foil         int
		lang         string
		collectionID sql.NullInt64
		found        = true
	)
	err := tx.QueryRow(`SELECT id, sync_at, sync_by, foil, lang, collection_id FROM cards WHERE uid = ?`, e.UID).
		Scan(&id, &rowAt, &rowBy, &foil, &lang, &collectionID)
	if err == sql.ErrNoRows {
		found, foil, lang = false, 0, "en"
	} else if err != nil {
		return false, err
	}
	if found && (e.Op == opCopyAdd || !syncKeyAfter(e.At, s.Device, rowAt, rowBy)) {
		return false, nil
	}

	game := ""
	if resolveGame != nil {
		game = resolveGame(e.CardID)
	}
	if game == "" {
		game = e.Game
	}
	if e.Foil != nil {
		foil = 0
		if *e.Foil {
			foil = 1
		}
	}
	if e.Lang != "" {
		lang = e.Lang
	}
	if e.Collection != nil || !found {
		uid := ""
		if e.Collection != nil {
			uid = *e.Collection
		}
		if collectionID, err = syncCollectionID(tx, uid); err != nil {
			return false, err
		}
	}

	if found {
		_, err = tx.Exec(`UPDATE cards SET card_id = ?, game = ?, foil = ?, lang = ?, collection_id = ?, sync_at = ?, sync_by = ? WHERE id = ?`,
			e.CardID, game, foil, lang, collectionID, e.At, s.Device, id)
		return err == nil, err
	}
	model, scannedAt := "", e.At
	if e.Op == opCopyAdd {
		model = e.Model
		if _, err := parseSyncAt(e.ScannedAt); err == nil {
			scannedAt = e.ScannedAt
		}
	}
	_, err = tx.Exec(`INSERT INTO cards (collection_id, card_id, game, foil, model_name, lang, uid, scanned_at, sync_at, sync_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		collectionID, e.CardID, game, foil, model, lang, e.UID, syncAtToSQLite(scannedAt), e.At, s.Device)
	return err == nil, err
}

// applyCopyRemove deletes the copy and tombstones its uid, whatever the keys:
// a removal is final.
func applyCopyRemove(tx *sql.Tx, e syncEvent) (bool, error) {
	res, err := tx.Exec(`DELETE FROM cards WHERE uid = ?`, e.UID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, syncTombstone(tx, e.UID, e.At)
}

// priceRefKey is the last-writer-wins key of the reference of cardID: its
// row's when it exists, else its last clear's (sync_ref_clears), else empty.
func priceRefKey(tx syncDB, cardID string) (at, by string, rowFound bool, err error) {
	err = tx.QueryRow(`SELECT sync_at, sync_by FROM price_refs WHERE card_id = ?`, cardID).Scan(&at, &by)
	if err == nil {
		return at, by, true, nil
	} else if err != sql.ErrNoRows {
		return "", "", false, err
	}
	err = tx.QueryRow(`SELECT at, device FROM sync_ref_clears WHERE card_id = ?`, cardID).Scan(&at, &by)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	return at, by, false, err
}

// applyPriceRefSet writes the reference when its key wins, and forgets the
// clear it outranks.
func applyPriceRefSet(tx *sql.Tx, s syncSourced) (bool, error) {
	e := s.Event
	if e.CardID == "" {
		return false, nil
	}
	rowAt, rowBy, _, err := priceRefKey(tx, e.CardID)
	if err != nil {
		return false, err
	}
	if !syncKeyAfter(e.At, s.Device, rowAt, rowBy) {
		return false, nil
	}
	t, _ := parseSyncAt(e.At) // checked by applySyncEvents
	if _, err := tx.Exec(`INSERT INTO price_refs (card_id, product, non_english, label, chosen_at, sync_at, sync_by) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(card_id) DO UPDATE SET product = excluded.product, non_english = excluded.non_english, label = excluded.label, chosen_at = excluded.chosen_at, sync_at = excluded.sync_at, sync_by = excluded.sync_by`,
		e.CardID, e.Product, e.NonEnglish, e.Label, t.UTC().Format(time.RFC3339), e.At, s.Device); err != nil {
		return false, err
	}
	_, err = tx.Exec(`DELETE FROM sync_ref_clears WHERE card_id = ?`, e.CardID)
	return err == nil, err
}

// applyPriceRefClear deletes the reference when its key wins and remembers
// the clear's key, so an older set arriving later stays out.
func applyPriceRefClear(tx *sql.Tx, s syncSourced) (bool, error) {
	e := s.Event
	if e.CardID == "" {
		return false, nil
	}
	rowAt, rowBy, rowFound, err := priceRefKey(tx, e.CardID)
	if err != nil {
		return false, err
	}
	if !syncKeyAfter(e.At, s.Device, rowAt, rowBy) {
		return false, nil
	}
	if rowFound {
		if _, err := tx.Exec(`DELETE FROM price_refs WHERE card_id = ?`, e.CardID); err != nil {
			return false, err
		}
	}
	if err := upsertRefClear(tx, e.CardID, e.At, s.Device); err != nil {
		return false, err
	}
	return rowFound, nil
}

// upsertRefClear remembers the key of the last clear of cardID's reference.
func upsertRefClear(q syncDB, cardID, at, device string) error {
	_, err := q.Exec(`INSERT INTO sync_ref_clears (card_id, at, device) VALUES (?, ?, ?)
		ON CONFLICT(card_id) DO UPDATE SET at = excluded.at, device = excluded.device`, cardID, at, device)
	return err
}
