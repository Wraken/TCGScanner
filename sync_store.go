package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The database side of the collection sync: stable uids, the event log of
// this device, the device identity. See the collection sync design.

const (
	settingSyncDeviceID     = "sync_device_id"
	settingSyncDeviceName   = "sync_device_name"
	settingSyncBootstrapped = "sync_bootstrapped"
	settingSyncUploadedSeq  = "sync_uploaded_seq"
)

// syncDB is what *sql.DB and *sql.Tx share, so the helpers work in both.
type syncDB interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func newUID() string { return uuid.NewString() }

// 2026-10-19: sync schema. (sync_at, sync_by) on a row is the last-writer-wins
// key: the at and device id of the event that last wrote it.
const syncTables = `
	CREATE TABLE IF NOT EXISTS sync_events (
		seq  INTEGER PRIMARY KEY AUTOINCREMENT,
		at   TEXT NOT NULL,
		line TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS sync_sources (
		device_id     TEXT PRIMARY KEY,
		name          TEXT NOT NULL DEFAULT '',
		app           TEXT NOT NULL DEFAULT '',
		file_id       TEXT NOT NULL DEFAULT '',
		applied_seq   INTEGER NOT NULL DEFAULT 0,
		modified_time TEXT NOT NULL DEFAULT '',
		last_change   TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS sync_tombstones (
		uid        TEXT PRIMARY KEY,
		deleted_at TEXT NOT NULL
	);
	-- The key (at, device) of the last clear of a price reference with no row:
	-- an older price_ref.set that arrives later must not bring it back.
	CREATE TABLE IF NOT EXISTS sync_ref_clears (
		card_id TEXT PRIMARY KEY,
		at      TEXT NOT NULL,
		device  TEXT NOT NULL
	);
`

// migrateSync is idempotent: it fills uids, creates the device identity and
// writes the bootstrap events once, all in one transaction.
func migrateSync(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`ALTER TABLE collections ADD COLUMN uid TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collections ADD COLUMN sync_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collections ADD COLUMN sync_by TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cards ADD COLUMN uid TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cards ADD COLUMN sync_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cards ADD COLUMN sync_by TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE price_refs ADD COLUMN sync_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE price_refs ADD COLUMN sync_by TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = tx.Exec(stmt) // already present on a migrated database
	}
	if _, err := tx.Exec(syncTables); err != nil {
		return err
	}

	deviceID, _, err := ensureDevice(tx)
	if err != nil {
		return err
	}
	now := syncNow()
	for _, table := range []string{"collections", "cards"} {
		if err := fillUIDs(tx, table, now, deviceID); err != nil {
			return err
		}
	}
	// Partial unique indexes, created after the uids are filled: an old
	// database has every row at '' right after the ALTER.
	for _, table := range []string{"collections", "cards"} {
		if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ` + table + `_uid ON ` + table + `(uid) WHERE uid <> ''`); err != nil {
			return err
		}
	}

	if _, done, err := txSetting(tx, settingSyncBootstrapped); err != nil {
		return err
	} else if !done {
		if err := bootstrapEvents(tx, deviceID, now); err != nil {
			return err
		}
		if err := txSetSetting(tx, settingSyncBootstrapped, "1"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func fillUIDs(tx *sql.Tx, table, now, deviceID string) error {
	rows, err := tx.Query(`SELECT id FROM ` + table + ` WHERE uid = ''`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE `+table+` SET uid = ?, sync_at = ?, sync_by = ? WHERE id = ?`, newUID(), now, deviceID, id); err != nil {
			return err
		}
	}
	return nil
}

// sqliteToSyncAt converts SQLite's UTC "YYYY-MM-DD HH:MM:SS" to the journal
// format. A value that does not parse (hand-edited rows) gets fallback, the
// migration's own syncNow().
func sqliteToSyncAt(s, fallback string) string {
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return formatSyncAt(t)
	}
	return fallback
}

// priceRefSyncAt converts price_refs.chosen_at (RFC 3339) to the journal
// format; fallback when it does not parse.
func priceRefSyncAt(chosenAt, fallback string) string {
	if t, err := time.Parse(time.RFC3339, chosenAt); err == nil {
		return formatSyncAt(t)
	}
	return fallback
}

func priceRefEvent(r PriceRef, at string) syncEvent {
	return syncEvent{Op: opPriceRefSet, At: at, CardID: r.CardID, Product: r.Product, NonEnglish: r.NonEnglish, Label: r.Label}
}

// bootstrapEvents records the rows that exist before sync as events, once.
// Collections come first, then copies, then price refs, each kind in date
// order, so a copy's collection exists when a reader applies the journal in
// seq order.
func bootstrapEvents(tx *sql.Tx, deviceID, now string) error {
	type collection struct{ uid, name, createdAt string }
	rows, err := tx.Query(`SELECT uid, name, COALESCE(created_at, '') FROM collections ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	var cols []collection
	for rows.Next() {
		var c collection
		if err := rows.Scan(&c.uid, &c.name, &c.createdAt); err != nil {
			rows.Close()
			return err
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, c := range cols {
		at := sqliteToSyncAt(c.createdAt, now)
		if err := appendSyncEvent(tx, syncEvent{Op: opCollectionCreate, At: at, UID: c.uid, Name: c.name}); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE collections SET sync_at = ?, sync_by = ? WHERE uid = ?`, at, deviceID, c.uid); err != nil {
			return err
		}
	}

	rows, err = tx.Query(`SELECT c.uid, c.card_id, c.game, c.lang, c.model_name, COALESCE(c.scanned_at, ''), c.foil, COALESCE(col.uid, '')
		FROM cards c LEFT JOIN collections col ON col.id = c.collection_id ORDER BY c.scanned_at, c.id`)
	if err != nil {
		return err
	}
	var copies []copyRow
	for rows.Next() {
		var c copyRow
		var foil int
		if err := rows.Scan(&c.uid, &c.cardID, &c.game, &c.lang, &c.model, &c.scannedAt, &foil, &c.collection); err != nil {
			rows.Close()
			return err
		}
		c.foil = foil != 0
		copies = append(copies, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, c := range copies {
		at := sqliteToSyncAt(c.scannedAt, now)
		if err := appendSyncEvent(tx, syncEvent{
			Op: opCopyAdd, At: at, UID: c.uid, CardID: c.cardID, Game: c.game, Lang: c.lang,
			ScannedAt: at, Model: c.model, Foil: boolPtr(c.foil), Collection: strPtr(c.collection),
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE cards SET sync_at = ?, sync_by = ? WHERE uid = ?`, at, deviceID, c.uid); err != nil {
			return err
		}
	}

	rows, err = tx.Query(`SELECT card_id, product, non_english, label, chosen_at FROM price_refs ORDER BY chosen_at, card_id`)
	if err != nil {
		return err
	}
	var refs []PriceRef
	for rows.Next() {
		var r PriceRef
		if err := rows.Scan(&r.CardID, &r.Product, &r.NonEnglish, &r.Label, &r.ChosenAt); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, r := range refs {
		at := priceRefSyncAt(r.ChosenAt, now)
		if err := appendSyncEvent(tx, priceRefEvent(r, at)); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE price_refs SET sync_at = ?, sync_by = ? WHERE card_id = ?`, at, deviceID, r.CardID); err != nil {
			return err
		}
	}
	return nil
}

// copyRow is a card row as the journal sees it, its collection by uid.
type copyRow struct {
	uid, cardID, game, lang, model, scannedAt, collection string
	foil                                                  bool
}

// event builds the copy event of op at the instant at: copy.add carries the
// whole row, copy.update only what can change.
func (c copyRow) event(op, at string) syncEvent {
	e := syncEvent{Op: op, At: at, UID: c.uid, CardID: c.cardID, Game: c.game, Lang: c.lang,
		Foil: boolPtr(c.foil), Collection: strPtr(c.collection)}
	if op == opCopyAdd {
		e.ScannedAt = at
		e.Model = c.model
	}
	return e
}

// copyEvent reads the card row rowID and builds its copy.add or copy.update
// event, stamped at the row's sync_at (the instant of the write in progress).
func copyEvent(tx *sql.Tx, rowID int, op string) (syncEvent, error) {
	var c copyRow
	var foil int
	var at string
	err := tx.QueryRow(`SELECT c.uid, c.card_id, c.game, c.lang, c.model_name, c.foil, COALESCE(col.uid, ''), c.sync_at
		FROM cards c LEFT JOIN collections col ON col.id = c.collection_id WHERE c.id = ?`, rowID).
		Scan(&c.uid, &c.cardID, &c.game, &c.lang, &c.model, &foil, &c.collection, &at)
	if err != nil {
		return syncEvent{}, err
	}
	c.foil = foil != 0
	return c.event(op, at), nil
}

// syncAtToSQLite converts a journal instant to SQLite's UTC
// "YYYY-MM-DD HH:MM:SS", the form scanned_at has always had.
func syncAtToSQLite(at string) string {
	if t, err := parseSyncAt(at); err == nil {
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	return at
}

func txSetting(q syncDB, key string) (string, bool, error) {
	var v string
	err := q.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return v, err == nil, err
}

func txSetSetting(q syncDB, key, value string) error {
	_, err := q.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ensureDevice reads the device identity, creating it on first use. The name
// is the host name, "this device" when it cannot be read.
func ensureDevice(q syncDB) (id, name string, err error) {
	id, ok, err := txSetting(q, settingSyncDeviceID)
	if err != nil {
		return "", "", err
	}
	if !ok || id == "" {
		id = newUID()
		if err := txSetSetting(q, settingSyncDeviceID, id); err != nil {
			return "", "", err
		}
	}
	name, ok, err = txSetting(q, settingSyncDeviceName)
	if err != nil {
		return "", "", err
	}
	if !ok || name == "" {
		name, _ = os.Hostname()
		if name == "" {
			name = "this device"
		}
		if err := txSetSetting(q, settingSyncDeviceName, name); err != nil {
			return "", "", err
		}
	}
	return id, name, nil
}

func syncDevice(db *sql.DB) (id, name string, err error) { return ensureDevice(db) }

// appendSyncEvent records an event of this device in the same transaction as
// the change it describes. The line is stored without seq: the row's seq is
// the sequence number.
func appendSyncEvent(tx *sql.Tx, e syncEvent) error {
	if e.At == "" {
		e.At = syncNow()
	}
	e.Seq = 0
	line, err := encodeEvent(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO sync_events (at, line) VALUES (?, ?)`, e.At, string(line))
	return err
}

// lastSyncEventSeq is the seq of this device's last event, 0 when none: the
// cheap check before rendering the whole journal.
func lastSyncEventSeq(db *sql.DB) (int64, error) {
	var seq int64
	err := db.QueryRow(`SELECT COALESCE(max(seq), 0) FROM sync_events`).Scan(&seq)
	return seq, err
}

// renderJournal is this device's whole journal: the device record, then every
// event with its seq. lastSeq is the seq of the last event (0 when none).
func renderJournal(db *sql.DB) (content string, lastSeq int64, err error) {
	id, name, err := syncDevice(db)
	if err != nil {
		return "", 0, err
	}
	var b strings.Builder
	head, err := encodeEvent(syncEvent{Op: opDevice, At: syncNow(), Device: id, Name: name, App: "desktop"})
	if err != nil {
		return "", 0, err
	}
	b.Write(head)
	b.WriteByte('\n')

	rows, err := db.Query(`SELECT seq, line FROM sync_events ORDER BY seq`)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var line string
		if err := rows.Scan(&seq, &line); err != nil {
			return "", 0, err
		}
		var e syncEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return "", 0, err
		}
		e.Seq = seq
		out, err := encodeEvent(e)
		if err != nil {
			return "", 0, err
		}
		b.Write(out)
		b.WriteByte('\n')
		lastSeq = seq
	}
	return b.String(), lastSeq, rows.Err()
}

// syncSourced is one event of another device's journal, with that device's id.
type syncSourced struct {
	Device string
	Event  syncEvent
}

// syncSource is what this device knows of another device's journal: its
// device record, its Drive file, and how far it has been applied.
type syncSource struct {
	DeviceID, Name, App, FileID string
	AppliedSeq                  int64
	ModifiedTime, LastChange    string
}

// upsertSyncSource records a device's journal file. applied_seq is written on
// the first insert only: applySyncEvents owns it afterwards.
func upsertSyncSource(db *sql.DB, s syncSource) error {
	_, err := db.Exec(`INSERT INTO sync_sources (device_id, name, app, file_id, applied_seq, modified_time, last_change) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET name = excluded.name, app = excluded.app, file_id = excluded.file_id,
			modified_time = excluded.modified_time, last_change = excluded.last_change`,
		s.DeviceID, s.Name, s.App, s.FileID, s.AppliedSeq, s.ModifiedTime, s.LastChange)
	return err
}

func listSyncSources(db *sql.DB) ([]syncSource, error) {
	rows, err := db.Query(`SELECT device_id, name, app, file_id, applied_seq, modified_time, last_change FROM sync_sources ORDER BY device_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []syncSource
	for rows.Next() {
		var s syncSource
		if err := rows.Scan(&s.DeviceID, &s.Name, &s.App, &s.FileID, &s.AppliedSeq, &s.ModifiedTime, &s.LastChange); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// bumpSyncAt is the at of a local write on an existing row: now, or 1 ms
// after the row's key when that key is not earlier (a peer's clock ahead of
// ours). An edit made after seeing a change thus always outranks it. An
// empty or unparsable key is ignored.
func bumpSyncAt(now, rowSyncAt string) string {
	row, err := parseSyncAt(rowSyncAt)
	if err != nil {
		return now
	}
	if t, err := parseSyncAt(now); err == nil && t.After(row) {
		return now
	}
	return formatSyncAt(row.Add(time.Millisecond))
}

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

// clearSyncSources forgets every other device's journal (another Google
// account connects): its journals are folded from their first event.
func clearSyncSources(db *sql.DB) error {
	_, err := db.Exec(`DELETE FROM sync_sources`)
	return err
}

// detachSyncSources forgets where the other devices' journals live on the
// Drive but keeps how far each was applied (on disconnect): connecting the
// same account again lists them anew and resumes after applied_seq.
func detachSyncSources(db *sql.DB) error {
	_, err := db.Exec(`UPDATE sync_sources SET file_id = '', modified_time = ''`)
	return err
}

// deleteSettings removes settings rows; absent keys are fine.
func deleteSettings(db *sql.DB, keys ...string) error {
	for _, key := range keys {
		if _, err := db.Exec(`DELETE FROM settings WHERE key = ?`, key); err != nil {
			return err
		}
	}
	return nil
}
