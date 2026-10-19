package main

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMigrateSyncGivesEveryRowAUidOnce(t *testing.T) {
	db := testDB(t) // openDB migrates, so uids already exist: insert rows the old way, then migrate again
	db.Exec(`INSERT INTO collections (name, uid) VALUES ('To sell', '')`)
	db.Exec(`INSERT INTO cards (card_id, game, uid) VALUES ('OP01-006_p4', 'onepiece', '')`)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var uids []string
	rows, _ := db.Query(`SELECT uid FROM collections UNION ALL SELECT uid FROM cards`)
	for rows.Next() {
		var u string
		rows.Scan(&u)
		uids = append(uids, u)
	}
	rows.Close()
	for _, u := range uids {
		if len(u) != 36 {
			t.Errorf("uid %q", u)
		}
	}
	first := uids[0]
	migrate(db)
	var again string
	db.QueryRow(`SELECT uid FROM collections`).Scan(&again)
	if again != first {
		t.Error("a second migration must keep uids")
	}
	id, name, err := syncDevice(db)
	if err != nil || len(id) != 36 || name == "" {
		t.Errorf("device %q %q %v", id, name, err)
	}
	id2, _, _ := syncDevice(db)
	if id2 != id {
		t.Error("device id must be stable")
	}
}

func TestBootstrapEventsAreWrittenOnceInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, _ := sql.Open("sqlite", path) // a pre-sync database: schema without the sync additions
	raw.Exec(`CREATE TABLE collections (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
	raw.Exec(`CREATE TABLE cards (id INTEGER PRIMARY KEY AUTOINCREMENT, collection_id INTEGER, card_id TEXT NOT NULL, game TEXT NOT NULL DEFAULT '', foil INTEGER NOT NULL DEFAULT 0, lang TEXT NOT NULL DEFAULT 'en', model_name TEXT NOT NULL DEFAULT '', scanned_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
	raw.Exec(`INSERT INTO collections (name, created_at) VALUES ('To sell', '2026-10-01 10:00:00')`)
	raw.Exec(`INSERT INTO cards (collection_id, card_id, game, foil, lang, model_name, scanned_at) VALUES (1, 'OP01-006_p4', 'onepiece', 1, 'ja', 'tcg', '2026-09-30 18:41:10'), (NULL, 'sv01-020', 'pokemon', 0, 'en', 'tcg', '2026-10-02 09:00:00')`)
	raw.Exec(`CREATE TABLE price_refs (card_id TEXT PRIMARY KEY, product INTEGER NOT NULL, non_english INTEGER NOT NULL DEFAULT 0, label TEXT NOT NULL DEFAULT '', chosen_at TEXT NOT NULL)`)
	raw.Exec(`INSERT INTO price_refs (card_id, product, non_english, label, chosen_at) VALUES ('OP01-001', 111, 1, 'Old pick', '2026-10-08T20:00:00Z')`)
	raw.Close()
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setPriceRef(db, PriceRef{CardID: "OP01-006_p4", Product: 799296, Label: "Jolly Roger Foil", ChosenAt: "2026-10-08T20:00:00Z"}) // after migration: recorded as a normal event
	content, last, err := renderJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	_, events, skipped, err := decodeJournal(strings.NewReader(content))
	if err != nil || skipped != 0 {
		t.Fatal(err, skipped)
	}
	ops := []string{}
	for _, e := range events {
		ops = append(ops, e.Op)
	}
	want := []string{opCollectionCreate, opCopyAdd, opCopyAdd, opPriceRefSet, opPriceRefSet}
	if !slices.Equal(ops, want) || last != 5 {
		t.Fatalf("ops %v last %d, want %v 5", ops, last, want)
	}
	if e := events[3]; e.CardID != "OP01-001" || e.At != "2026-10-08T20:00:00.000Z" || e.Product != 111 || e.NonEnglish != 1 || e.Label != "Old pick" {
		t.Errorf("bootstrap price ref: %+v", e)
	}
	deviceID, _, _ := syncDevice(db)
	var syncAt, syncBy string
	db.QueryRow(`SELECT sync_at, sync_by FROM price_refs WHERE card_id = 'OP01-001'`).Scan(&syncAt, &syncBy)
	if syncAt != "2026-10-08T20:00:00.000Z" || syncBy != deviceID {
		t.Errorf("price ref sync key: %q %q", syncAt, syncBy)
	}
	db.QueryRow(`SELECT sync_at, sync_by FROM cards WHERE card_id = 'OP01-006_p4'`).Scan(&syncAt, &syncBy)
	if syncAt != "2026-09-30T18:41:10.000Z" || syncBy != deviceID {
		t.Errorf("card sync key: %q %q", syncAt, syncBy)
	}
	if events[0].At != "2026-10-01T10:00:00.000Z" || events[1].At != "2026-09-30T18:41:10.000Z" || events[1].ScannedAt != events[1].At {
		t.Errorf("bootstrap dates: %+v %+v", events[0], events[1])
	}
	if *events[1].Foil != true || events[1].Lang != "ja" || *events[1].Collection != events[0].UID || *events[2].Collection != "" {
		t.Errorf("bootstrap copy fields: %+v %+v", events[1], events[2])
	}
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Errorf("seq %d at %d", e.Seq, i)
		}
	}
	migrate(db)
	if _, last2, _ := renderJournal(db); last2 != 5 {
		t.Errorf("a second migration re-bootstrapped: %d events", last2)
	}
}

func lastEvent(t *testing.T, db *sql.DB) syncEvent {
	t.Helper()
	var line string
	if err := db.QueryRow(`SELECT line FROM sync_events ORDER BY seq DESC LIMIT 1`).Scan(&line); err != nil {
		t.Fatal(err)
	}
	var e syncEvent
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestWritePathsRecordExactlyOneEventEach(t *testing.T) {
	db := testDB(t)
	count := func() int {
		var n int
		db.QueryRow(`SELECT count(*) FROM sync_events`).Scan(&n)
		return n
	}
	base := count()
	cid, _ := createCollection(db, "To sell")
	e := lastEvent(t, db)
	var cuid string
	db.QueryRow(`SELECT uid FROM collections WHERE id = ?`, cid).Scan(&cuid)
	if e.Op != opCollectionCreate || e.UID != cuid || e.Name != "To sell" || count() != base+1 {
		t.Errorf("create: %+v", e)
	}
	renameCollection(db, cid, "For trade")
	if e = lastEvent(t, db); e.Op != opCollectionRename || e.UID != cuid || e.Name != "For trade" {
		t.Errorf("rename: %+v", e)
	}
	rid, _ := insertCard(db, cid, "OP01-006_p4", "onepiece", "tcg", true, "ja")
	e = lastEvent(t, db)
	card, _ := getCard(db, rid)
	var ruid, scanned string
	db.QueryRow(`SELECT uid, scanned_at FROM cards WHERE id = ?`, rid).Scan(&ruid, &scanned)
	if e.Op != opCopyAdd || e.UID != ruid || e.CardID != "OP01-006_p4" || e.Game != "onepiece" || *e.Foil != true || e.Lang != "ja" || *e.Collection != cuid || e.Model != "tcg" || e.ScannedAt != e.At {
		t.Errorf("add: %+v (row scanned_at %q)", e, scanned)
	}
	moveCardToCustom(db, rid, 0)
	if e = lastEvent(t, db); e.Op != opCopyUpdate || *e.Collection != "" || *e.Foil != true || e.CardID != "OP01-006_p4" {
		t.Errorf("move: %+v", e)
	}
	card.Foil, card.Lang, card.CardID, card.Game = false, "en", "OP01-007", "onepiece"
	updateCard(db, card)
	if e = lastEvent(t, db); e.Op != opCopyUpdate || *e.Foil != false || e.Lang != "en" || e.CardID != "OP01-007" {
		t.Errorf("update: %+v", e)
	}
	deleteCard(db, rid)
	var tomb int
	db.QueryRow(`SELECT count(*) FROM sync_tombstones WHERE uid = ?`, ruid).Scan(&tomb)
	if e = lastEvent(t, db); e.Op != opCopyRemove || e.UID != ruid || tomb != 1 {
		t.Errorf("remove: %+v tomb %d", e, tomb)
	}
	setPriceRef(db, PriceRef{CardID: "OP01-006_p4", Product: 799296, NonEnglish: 799297, Label: "Jolly Roger Foil"})
	if e = lastEvent(t, db); e.Op != opPriceRefSet || e.Product != 799296 || e.NonEnglish != 799297 || e.Label != "Jolly Roger Foil" {
		t.Errorf("ref set: %+v", e)
	}
	clearPriceRef(db, "OP01-006_p4")
	if e = lastEvent(t, db); e.Op != opPriceRefClear || e.CardID != "OP01-006_p4" {
		t.Errorf("ref clear: %+v", e)
	}
	deleteCollection(db, cid)
	if e = lastEvent(t, db); e.Op != opCollectionDelete || e.UID != cuid {
		t.Errorf("delete: %+v", e)
	}
	if count() != base+9 {
		t.Errorf("%d events, want 9", count()-base)
	}
	if _, err := createCollection(db, "  "); err == nil || count() != base+9 {
		t.Error("a rejected write must record nothing")
	}
}

func ev(device string, seq int64, at, op string, f func(*syncEvent)) syncSourced {
	e := syncEvent{V: 1, Seq: seq, At: at, Op: op}
	if f != nil {
		f(&e)
	}
	return syncSourced{Device: device, Event: e}
}

func copyAdd(uid, cardID, coll string) func(*syncEvent) {
	return func(e *syncEvent) {
		e.UID, e.CardID, e.Game, e.Foil, e.Lang, e.Collection, e.ScannedAt, e.Model = uid, cardID, "", boolPtr(false), "en", strPtr(coll), e.At, "phone"
	}
}

// cardsByUID reads every card row keyed by uid, CollectionID 0 when NULL.
func cardsByUID(t *testing.T, db *sql.DB) map[string]CollectionCard {
	t.Helper()
	rows, err := db.Query(`SELECT id, card_id, game, collection_id, foil, lang, model_name, scanned_at, uid FROM cards`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]CollectionCard{}
	for rows.Next() {
		var c CollectionCard
		var cid sql.NullInt64
		var foil int
		var uid string
		if err := rows.Scan(&c.ID, &c.CardID, &c.Game, &cid, &foil, &c.Lang, &c.ModelName, &c.ScannedAt, &uid); err != nil {
			t.Fatal(err)
		}
		c.Foil = foil != 0
		if cid.Valid {
			c.CollectionID = int(cid.Int64)
		}
		out[uid] = c
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestApplyFoldsOtherDevicesEvents(t *testing.T) {
	db := testDB(t)
	resolve := func(id string) string {
		if strings.HasPrefix(id, "OP") {
			return "onepiece"
		}
		return ""
	}
	batch := []syncSourced{
		ev("phone", 1, "2026-10-19T10:00:00.000Z", opCollectionCreate, func(e *syncEvent) { e.UID, e.Name = "coll-1", "To sell" }),
		ev("phone", 2, "2026-10-19T10:01:00.000Z", opCopyAdd, copyAdd("copy-1", "OP01-006_p4", "coll-1")),
		ev("phone", 3, "2026-10-19T10:02:00.000Z", opCopyAdd, copyAdd("copy-2", "OP01-007", "nowhere")), // unknown collection: unfiled, never dropped
		ev("phone", 4, "2026-10-19T10:03:00.000Z", opPriceRefSet, func(e *syncEvent) { e.CardID, e.Product, e.Label = "OP01-006_p4", 799296, "Jolly" }),
	}
	n, err := applySyncEvents(db, batch, resolve)
	if err != nil || n != 4 {
		t.Fatal(n, err)
	}
	cards := cardsByUID(t, db)
	var collID int
	db.QueryRow(`SELECT id FROM collections WHERE uid = 'coll-1'`).Scan(&collID)
	if c := cards["copy-1"]; c.CollectionID != collID || c.Game != "onepiece" || c.ModelName != "phone" {
		t.Errorf("copy-1 %+v", c)
	}
	if c := cards["copy-2"]; c.CollectionID != 0 || c.CardID != "OP01-007" {
		t.Errorf("copy-2 %+v", c)
	}
	var events int
	db.QueryRow(`SELECT count(*) FROM sync_events`).Scan(&events)
	if events != 0 {
		t.Errorf("applying must not record events, got %d", events)
	}
	srcs, _ := listSyncSources(db)
	if len(srcs) != 1 || srcs[0].DeviceID != "phone" || srcs[0].AppliedSeq != 4 {
		t.Errorf("sources %+v", srcs)
	}
	// Re-applying the same batch is a no-op (idempotent adds).
	if n, _ = applySyncEvents(db, batch, resolve); n != 0 || len(cardsByUID(t, db)) != 2 {
		t.Error("re-apply changed rows")
	}
}

func TestApplyLastWriterWinsAndRemovalIsFinal(t *testing.T) {
	db := testDB(t)
	resolve := func(string) string { return "onepiece" }
	upd := func(uid, lang string, foil bool) func(*syncEvent) {
		return func(e *syncEvent) {
			e.UID, e.CardID, e.Foil, e.Lang, e.Collection = uid, "OP01-006_p4", boolPtr(foil), lang, strPtr("")
		}
	}
	// Event dates are in the past (2020) so a local edit made "now" is always the newest.
	applySyncEvents(db, []syncSourced{ev("phone", 1, "2020-10-19T10:00:00.000Z", opCopyAdd, copyAdd("copy-1", "OP01-006_p4", ""))}, resolve)
	// Two updates from two devices, the later at wins whatever the batch order.
	applySyncEvents(db, []syncSourced{
		ev("tablet", 1, "2020-10-19T10:05:00.000Z", opCopyUpdate, upd("copy-1", "ja", true)),
		ev("phone", 2, "2020-10-19T10:04:00.000Z", opCopyUpdate, upd("copy-1", "fr", false)),
	}, resolve)
	if c := cardsByUID(t, db)["copy-1"]; !c.Foil || c.Lang != "ja" {
		t.Errorf("later at must win: %+v", c)
	}
	// Re-applying the same events changes nothing (equal keys are not greater).
	if n, _ := applySyncEvents(db, []syncSourced{ev("tablet", 1, "2020-10-19T10:05:00.000Z", opCopyUpdate, upd("copy-1", "ja", true))}, resolve); n != 0 {
		t.Errorf("re-apply wrote %d", n)
	}
	// A newer local edit keeps against an older foreign one.
	var rowID int
	db.QueryRow(`SELECT id FROM cards WHERE uid = 'copy-1'`).Scan(&rowID)
	card, _ := getCard(db, rowID)
	card.Lang = "en"
	updateCard(db, card) // (sync_at, sync_by) = (now, this device)
	applySyncEvents(db, []syncSourced{ev("tablet", 2, "2020-10-19T10:06:00.000Z", opCopyUpdate, upd("copy-1", "de", true))}, resolve)
	if c := cardsByUID(t, db)["copy-1"]; c.Lang != "en" {
		t.Errorf("a newer local edit must keep: %+v", c)
	}
	// Removal is final: a later update does not resurrect.
	applySyncEvents(db, []syncSourced{ev("phone", 3, "2020-10-19T11:00:00.000Z", opCopyRemove, func(e *syncEvent) { e.UID = "copy-1" })}, resolve)
	applySyncEvents(db, []syncSourced{ev("tablet", 3, "2099-01-01T00:00:00.000Z", opCopyUpdate, upd("copy-1", "ja", true))}, resolve)
	if len(cardsByUID(t, db)) != 0 {
		t.Error("removed copy came back")
	}
	// Equal at: the higher device id wins, whatever the batch order.
	applySyncEvents(db, []syncSourced{
		ev("b", 1, "2020-10-19T13:01:00.000Z", opCopyUpdate, func(e *syncEvent) {
			e.UID, e.CardID, e.Foil, e.Lang, e.Collection = "copy-9", "OP01-009", boolPtr(false), "ja", strPtr("")
		}),
		ev("a", 1, "2020-10-19T13:00:00.000Z", opCopyAdd, copyAdd("copy-9", "OP01-009", "")),
		ev("a", 2, "2020-10-19T13:01:00.000Z", opCopyUpdate, func(e *syncEvent) {
			e.UID, e.CardID, e.Foil, e.Lang, e.Collection = "copy-9", "OP01-009", boolPtr(false), "en", strPtr("")
		}),
	}, resolve)
	if c := cardsByUID(t, db)["copy-9"]; c.Lang != "ja" {
		t.Errorf("tie must go to the higher device id: %+v", c)
	}
}

func TestApplyCollectionsAndRefs(t *testing.T) {
	db := testDB(t)
	resolve := func(string) string { return "onepiece" }
	applySyncEvents(db, []syncSourced{
		ev("phone", 1, "2026-10-19T10:00:00.000Z", opCollectionCreate, func(e *syncEvent) { e.UID, e.Name = "c-phone", "To sell" }),
		ev("tablet", 1, "2026-10-19T10:00:30.000Z", opCollectionCreate, func(e *syncEvent) { e.UID, e.Name = "c-tablet", "To sell" }), // same name, two collections
		ev("phone", 2, "2026-10-19T10:01:00.000Z", opCopyAdd, copyAdd("copy-1", "OP01-006_p4", "c-phone")),
		ev("phone", 3, "2026-10-19T10:02:00.000Z", opCollectionRename, func(e *syncEvent) { e.UID, e.Name = "c-phone", "For trade" }),
		ev("phone", 4, "2026-10-19T10:03:00.000Z", opCollectionDelete, func(e *syncEvent) { e.UID = "c-phone" }),
		ev("phone", 5, "2026-10-19T10:04:00.000Z", opPriceRefSet, func(e *syncEvent) {
			e.CardID, e.Product, e.NonEnglish, e.Label = "OP01-006_p4", 799296, 799297, "Jolly"
		}),
		ev("tablet", 2, "2026-10-19T10:05:00.000Z", opPriceRefClear, func(e *syncEvent) { e.CardID = "OP01-006_p4" }),
		ev("tablet", 3, "not a date", opCopyRemove, func(e *syncEvent) { e.UID = "copy-1" }), // skipped, the rest still applies
	}, resolve)
	colls, _ := listCollections(db)
	if len(colls) != 1 || colls[0].Name != "To sell" {
		t.Errorf("collections %+v", colls)
	}
	if c := cardsByUID(t, db)["copy-1"]; c.CollectionID != 0 {
		t.Errorf("copy must be unfiled after its collection's delete: %+v", c)
	}
	refs, _ := getPriceRefs(db)
	if len(refs) != 0 {
		t.Errorf("ref must be cleared: %+v", refs)
	}
	srcs, _ := listSyncSources(db)
	for _, s := range srcs {
		if (s.DeviceID == "phone" && s.AppliedSeq != 5) || (s.DeviceID == "tablet" && s.AppliedSeq != 3) {
			t.Errorf("source %+v", s)
		}
	}
}

// A clear remembers its key: an older set that arrives after it (its device
// was offline) must not bring the reference back.
func TestApplyPriceRefClearOutranksAnOlderLateSet(t *testing.T) {
	db := testDB(t)
	resolve := func(string) string { return "onepiece" }
	refCount := func() int {
		refs, err := getPriceRefs(db)
		if err != nil {
			t.Fatal(err)
		}
		return len(refs)
	}
	markers := func() int {
		var n int
		db.QueryRow(`SELECT count(*) FROM sync_ref_clears WHERE card_id = 'OP01-006_p4'`).Scan(&n)
		return n
	}
	set := func(e *syncEvent) { e.CardID, e.Product, e.Label = "OP01-006_p4", 799296, "Jolly" }
	clr := func(e *syncEvent) { e.CardID = "OP01-006_p4" }

	if _, err := applySyncEvents(db, []syncSourced{ev("tablet", 1, "2026-10-19T10:05:00.000Z", opPriceRefClear, clr)}, resolve); err != nil {
		t.Fatal(err)
	}
	if n, err := applySyncEvents(db, []syncSourced{ev("phone", 1, "2026-10-19T10:00:00.000Z", opPriceRefSet, set)}, resolve); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if refCount() != 0 {
		t.Error("an older set must not re-insert a reference cleared after it")
	}
	if n, err := applySyncEvents(db, []syncSourced{ev("phone", 2, "2026-10-19T10:10:00.000Z", opPriceRefSet, set)}, resolve); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if refCount() != 1 || markers() != 0 {
		t.Errorf("a newer set must win and drop the marker: refs %d markers %d", refCount(), markers())
	}

	// A local clear writes its marker too, keyed (now, this device).
	if err := clearPriceRef(db, "OP01-006_p4"); err != nil {
		t.Fatal(err)
	}
	deviceID, _, _ := syncDevice(db)
	var by string
	db.QueryRow(`SELECT device FROM sync_ref_clears WHERE card_id = 'OP01-006_p4'`).Scan(&by)
	if by != deviceID {
		t.Errorf("local clear marker device %q, want %q", by, deviceID)
	}
	applySyncEvents(db, []syncSourced{ev("phone", 3, "2026-10-19T10:20:00.000Z", opPriceRefSet, set)}, resolve)
	if refCount() != 0 {
		t.Error("an older foreign set must not undo a newer local clear")
	}
}
