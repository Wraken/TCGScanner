package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// fakeGoogle serves Google's token and userinfo endpoints and returns the
// "browser": open answers the loopback redirect with a code and the state,
// as Google would after the user consents.
func fakeGoogle(t *testing.T) (open func(authURL string) error) {
	t.Helper()
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`)
		case "/userinfo":
			fmt.Fprint(w, `{"email":"florent@example.com"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(auth.Close)
	googleEndpoint = oauth2.Endpoint{AuthURL: auth.URL + "/auth", TokenURL: auth.URL + "/token"}
	googleUserinfoURL = auth.URL + "/userinfo"
	t.Cleanup(func() {
		googleEndpoint = google.Endpoint
		googleUserinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	})
	return func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=abc&state=" + url.QueryEscape(q.Get("state")))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

// syncApp is an App on a test database whose syncer talks to a fake Drive
// (through a plain http.Client, no OAuth) and signs in against a fake Google.
func syncApp(t *testing.T) (*App, *fakeDrive) {
	t.Helper()
	app := &App{db: testDB(t)}
	f := newFakeDrive(t)
	app.sync = newSyncer(app)
	app.sync.configured = true
	app.sync.newDrive = func(ctx context.Context) (*driveClient, error) { return f.client(), nil }
	app.sync.open = fakeGoogle(t)
	app.sync.debounce = 50 * time.Millisecond
	app.sync.retry = time.Hour
	t.Cleanup(app.sync.stop) // runs before the database closes
	return app, f
}

// phoneJournal is another device's journal: its device record ("Pixel 8",
// the mobile app) followed by the given event lines.
func phoneJournal(deviceID string, events ...string) string {
	lines := []string{fmt.Sprintf(`{"v":1,"op":"device","at":"2026-10-19T09:00:00.000Z","device":%q,"name":"Pixel 8","app":"mobile"}`, deviceID)}
	lines = append(lines, events...)
	return strings.Join(lines, "\n") + "\n"
}

// waitFor polls cond every 10 ms for up to 2 s.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 2 s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFirstConnectionWithEmptyDrivePushes(t *testing.T) {
	app, f := syncApp(t)
	createCollection(app.db, "To sell")
	insertCard(app.db, 0, "OP01-006_p4", "onepiece", "tcg", false, "en")
	st, err := app.ConnectGoogleDrive()
	if err != nil || st.State != "up_to_date" || !st.Connected || st.Email == "" || st.Merge != nil {
		t.Fatalf("%+v %v", st, err)
	}
	if st.LastSync == "" || st.Message != "" {
		t.Errorf("up to date: last sync %q, message %q", st.LastSync, st.Message)
	}
	id, _, _ := syncDevice(app.db)
	files, _ := f.client().listJournals(context.Background(), f.folders[syncFolderName])
	if len(files) != 1 || files[0].Name != syncJournalName(id) {
		t.Fatalf("files %+v", files)
	}
	_, events, _, _ := decodeJournal(bytes.NewReader(f.files[files[0].ID].Content))
	if len(events) != 2 {
		t.Errorf("pushed %d events, want the 2 bootstrap ones", len(events))
	}
	if v, _, _ := getSetting(app.db, settingSyncUploadedSeq); v != "2" {
		t.Errorf("uploaded_seq %q", v)
	}
	if len(st.Devices) != 1 || !st.Devices[0].ThisDevice || st.Devices[0].App != "desktop" || st.Devices[0].LastChange == "" {
		t.Errorf("devices %+v", st.Devices)
	}
	// A change later: debounce → cycle → the file is replaced.
	insertCard(app.db, 0, "OP01-007", "onepiece", "tcg", true, "en")
	app.sync.changed()
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		_, ev, _, _ := decodeJournal(bytes.NewReader(f.files[files[0].ID].Content))
		return len(ev) == 3
	})
}

func TestFirstConnectionPullsWhenThisDeviceIsEmpty(t *testing.T) {
	app, f := syncApp(t)
	folder := f.folder(syncFolderName)
	f.put(folder, "journal-phone.jsonl", phoneJournal("phone",
		`{"v":1,"seq":1,"at":"2026-10-19T10:00:00.000Z","op":"copy.add","uid":"p1","card_id":"OP01-006_p4","game":"onepiece","foil":true,"lang":"en","collection":"","scanned_at":"2026-10-19T10:00:00.000Z","model":"phone"}`))
	st, err := app.ConnectGoogleDrive()
	if err != nil || st.State != "up_to_date" || st.Merge != nil {
		t.Fatalf("%+v %v", st, err)
	}
	cards, _ := allCards(app.db)
	if len(cards) != 1 || cards[0].CardID != "OP01-006_p4" || !cards[0].Foil {
		t.Errorf("pulled %+v", cards)
	}
	if len(st.Devices) != 2 || st.Devices[0].Name == "" {
		t.Errorf("devices %+v", st.Devices)
	}
	if len(st.Devices) == 2 && (st.Devices[1].Name != "Pixel 8" || st.Devices[1].App != "mobile" || st.Devices[1].LastChange != "2026-10-19T10:00:00.000Z" || st.Devices[1].ThisDevice) {
		t.Errorf("phone %+v", st.Devices[1])
	}
	// Second cycle: nothing new, nothing re-applied, no echo of the phone's event in our journal.
	downloads := countCalls(f, "alt=media")
	app.SyncNow()
	if countCalls(f, "alt=media") != downloads {
		t.Error("an unchanged journal must not be downloaded again")
	}
	var n int
	app.db.QueryRow(`SELECT count(*) FROM sync_events`).Scan(&n)
	if n != 0 {
		t.Errorf("our journal must hold no echoed events, got %d", n)
	}
}

func countCalls(f *fakeDrive, substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

func TestFirstConnectionAsksBeforeMerging(t *testing.T) {
	app, f := syncApp(t)
	insertCard(app.db, 0, "sv01-020", "pokemon", "tcg", false, "en")
	f.put(f.folder(syncFolderName), "journal-phone.jsonl", phoneJournal("phone",
		`{"v":1,"seq":1,"at":"2026-10-19T10:00:00.000Z","op":"copy.add","uid":"p1","card_id":"OP01-006_p4","game":"onepiece","foil":false,"lang":"en","collection":"","scanned_at":"2026-10-19T10:00:00.000Z","model":"phone"}`,
		`{"v":1,"seq":2,"at":"2026-10-19T10:01:00.000Z","op":"copy.add","uid":"p2","card_id":"OP01-007","game":"onepiece","foil":false,"lang":"en","collection":"","scanned_at":"2026-10-19T10:01:00.000Z","model":"phone"}`,
		`{"v":1,"seq":3,"at":"2026-10-19T10:02:00.000Z","op":"copy.remove","uid":"p2"}`))
	st, err := app.ConnectGoogleDrive()
	if err != nil || st.State != "merge_pending" || st.Merge == nil || st.Merge.RemoteCopies != 1 || st.Merge.LocalCopies != 1 || !slices.Equal(st.Merge.RemoteDevices, []string{"Pixel 8"}) {
		t.Fatalf("%+v %v", st, err)
	}
	if cards, _ := allCards(app.db); len(cards) != 1 {
		t.Error("nothing may be applied before the answer")
	}
	if len(f.files) != 1 {
		t.Error("nothing may be uploaded before the answer")
	}
	// A local change while the question is open does not start a cycle.
	app.sync.changed()
	time.Sleep(150 * time.Millisecond)
	if len(f.files) != 1 {
		t.Error("no cycle while the merge question is open")
	}
	st, _ = app.ConfirmMerge(true)
	cards, _ := allCards(app.db)
	if st.State != "up_to_date" || len(cards) != 2 || len(f.files) != 2 {
		t.Errorf("after merge: %+v, %d cards, %d files", st, len(cards), len(f.files))
	}
	if st.Merge != nil {
		t.Error("the question is answered")
	}
}

func TestNotNowDisconnects(t *testing.T) {
	app, f := syncApp(t)
	insertCard(app.db, 0, "sv01-020", "pokemon", "tcg", false, "en")
	f.put(f.folder(syncFolderName), "journal-phone.jsonl", phoneJournal("phone", `{"v":1,"seq":1,"at":"2026-10-19T10:00:00.000Z","op":"copy.add","uid":"p1","card_id":"OP01-006_p4","game":"onepiece","foil":false,"lang":"en","collection":"","scanned_at":"2026-10-19T10:00:00.000Z","model":"phone"}`))
	app.ConnectGoogleDrive()
	st, _ := app.ConfirmMerge(false)
	if st.Connected || st.State != "disconnected" {
		t.Errorf("%+v", st)
	}
	if _, _, ok, _ := loadGoogleToken(app.db); ok {
		t.Error("token must be forgotten")
	}
	if cards, _ := allCards(app.db); len(cards) != 1 {
		t.Error("local data untouched")
	}
}

func TestCycleOfflineAndRecovery(t *testing.T) {
	app, f := syncApp(t)
	app.ConnectGoogleDrive()
	insertCard(app.db, 0, "OP01-007", "onepiece", "tcg", false, "en")
	f.down = true
	st, _ := app.SyncNow()
	if st.State != "offline" {
		t.Errorf("%+v", st)
	}
	if st.Message != "Offline, will retry" {
		t.Errorf("message %q", st.Message)
	}
	if v, _, _ := getSetting(app.db, settingSyncUploadedSeq); v != "0" && v != "" {
		t.Errorf("nothing uploaded must be marked: %q", v)
	}
	f.down = false
	st, _ = app.SyncNow()
	if st.State != "up_to_date" {
		t.Errorf("%+v", st)
	}
	// The folder deleted on Drive while connected: the stale id is replaced, the journal re-created.
	delete(f.folders, syncFolderName)
	for id, file := range f.files {
		if file.Parent != "" {
			delete(f.files, id)
		}
	}
	insertCard(app.db, 0, "OP01-008", "onepiece", "tcg", false, "en")
	st, _ = app.SyncNow()
	if st.State != "up_to_date" || len(f.folders) != 1 || len(f.files) != 1 {
		t.Errorf("stale folder: %+v folders %d files %d", st, len(f.folders), len(f.files))
	}
	for _, file := range f.files {
		if _, ev, _, _ := decodeJournal(bytes.NewReader(file.Content)); len(ev) != 2 || file.Parent != f.folders[syncFolderName] {
			t.Errorf("re-created journal: %d events, parent %q", len(ev), file.Parent)
		}
	}
	// Our journal deleted alone: the cached file id answers 404, the journal is re-created.
	for id := range f.files {
		delete(f.files, id)
	}
	insertCard(app.db, 0, "OP01-009", "onepiece", "tcg", false, "en")
	st, _ = app.SyncNow()
	if st.State != "up_to_date" || len(f.files) != 1 {
		t.Errorf("stale file: %+v files %d", st, len(f.files))
	}
	for id, file := range f.files {
		if v, _, _ := getSetting(app.db, settingSyncFileID); v != id {
			t.Errorf("file id cache %q, want %q", v, id)
		}
		if _, ev, _, _ := decodeJournal(bytes.NewReader(file.Content)); len(ev) != 3 {
			t.Errorf("re-created journal holds %d events, want the full 3", len(ev))
		}
	}
}

func TestSignedOutWhenGoogleRefusesTheRefresh(t *testing.T) {
	app, _ := syncApp(t)
	app.ConnectGoogleDrive()
	app.sync.newDrive = func(ctx context.Context) (*driveClient, error) { return nil, fmt.Errorf("refresh: %w", errSignedOut) }
	st, _ := app.SyncNow()
	if st.State != "signed_out" || st.Message != "Disconnected, sign in again" {
		t.Errorf("%+v", st)
	}
	if _, _, ok, _ := loadGoogleToken(app.db); ok {
		t.Error("the refused token must be cleared")
	}
}

func TestDisconnectKeepsRecording(t *testing.T) {
	app, _ := syncApp(t)
	app.ConnectGoogleDrive()
	app.DisconnectGoogleDrive()
	insertCard(app.db, 0, "OP01-007", "onepiece", "tcg", false, "en")
	var n int
	app.db.QueryRow(`SELECT count(*) FROM sync_events`).Scan(&n)
	if n != 1 {
		t.Errorf("events after disconnect: %d", n)
	}
	if st := app.GetSyncStatus(); st.Connected || len(st.Devices) != 0 {
		t.Errorf("%+v", st)
	}
	for _, key := range []string{settingSyncFolderID, settingSyncFileID, settingSyncUploadedSeq} {
		if _, ok, _ := getSetting(app.db, key); ok {
			t.Errorf("%s must be forgotten", key)
		}
	}
}

func TestDisconnectWaitsForTheRunningCycle(t *testing.T) {
	app, f := syncApp(t)
	app.ConnectGoogleDrive()
	started := make(chan struct{})
	app.sync.newDrive = func(ctx context.Context) (*driveClient, error) {
		close(started)
		<-ctx.Done() // a slow Drive: only the cancellation ends this cycle
		return nil, ctx.Err()
	}
	go app.SyncNow()
	<-started
	if err := app.DisconnectGoogleDrive(); err != nil {
		t.Fatal(err)
	}
	if app.sync.busy() {
		t.Error("disconnect returned while a cycle runs")
	}
	if st := app.GetSyncStatus(); st.State != "disconnected" {
		t.Errorf("%+v", st)
	}
	_ = f
}

// allCards is every copy in the database. listCards wants exactly one filter
// (game, other bucket or collection), so CardFilter{} is an error there.
func allCards(db *sql.DB) ([]CollectionCard, error) {
	rows, err := db.Query(`SELECT ` + cardColumns + ` FROM cards ORDER BY id`)
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

// A copy added on one device into a collection created on another lands in
// that collection even when its journal is read first: every journal's new
// events go into one batch.
func TestCycleAppliesAllJournalsInOneBatch(t *testing.T) {
	app, f := syncApp(t)
	folder := f.folder(syncFolderName)
	f.put(folder, "journal-b.jsonl", phoneJournal("b",
		`{"v":1,"seq":1,"at":"2026-10-19T10:01:00.000Z","op":"copy.add","uid":"c1","card_id":"OP01-006_p4","game":"onepiece","foil":false,"lang":"en","collection":"col-1","scanned_at":"2026-10-19T10:01:00.000Z","model":"phone"}`))
	f.put(folder, "journal-a.jsonl", phoneJournal("a",
		`{"v":1,"seq":1,"at":"2026-10-19T10:00:00.000Z","op":"collection.create","uid":"col-1","name":"Binder"}`))
	if st, err := app.ConnectGoogleDrive(); err != nil || st.State != "up_to_date" {
		t.Fatalf("%+v %v", st, err)
	}
	cards, _ := allCards(app.db)
	if len(cards) != 1 || cards[0].CollectionID == 0 {
		t.Fatalf("the copy must be in the collection: %+v", cards)
	}
	var name string
	app.db.QueryRow(`SELECT name FROM collections WHERE id = ?`, cards[0].CollectionID).Scan(&name)
	if name != "Binder" {
		t.Errorf("collection %q", name)
	}
}

// blockDownloads holds the first download in flight until release is
// called; entered is closed once it is held.
func blockDownloads(t *testing.T, f *fakeDrive) (entered <-chan struct{}, release func()) {
	in, out := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	f.beforeDownload = func() { once.Do(func() { close(in); <-out }) }
	release = func() { releaseOnce.Do(func() { close(out) }) }
	t.Cleanup(release) // before the fake server closes
	return in, release
}

func seedMergeCase(t *testing.T, app *App, f *fakeDrive) {
	insertCard(app.db, 0, "sv01-020", "pokemon", "tcg", false, "en")
	f.put(f.folder(syncFolderName), "journal-phone.jsonl", phoneJournal("phone", `{"v":1,"seq":1,"at":"2026-10-19T10:00:00.000Z","op":"copy.add","uid":"p1","card_id":"OP01-006_p4","game":"onepiece","foil":false,"lang":"en","collection":"","scanned_at":"2026-10-19T10:00:00.000Z","model":"phone"}`))
}

type connectResult struct {
	st  SyncStatus
	err error
}

// No trigger may run a cycle while the first-connection question is being
// computed: it would merge before the user answers.
func TestConnectRefusesCyclesWhileAsking(t *testing.T) {
	app, f := syncApp(t)
	seedMergeCase(t, app, f)
	entered, release := blockDownloads(t, f)
	connected := make(chan connectResult, 1)
	go func() {
		st, err := app.ConnectGoogleDrive()
		connected <- connectResult{st, err}
	}()
	<-entered // the token is saved, the question's download is in flight
	app.sync.changed()
	syncNow := make(chan error, 1)
	go func() {
		_, err := app.SyncNow()
		syncNow <- err
	}()
	if _, err := app.ConnectGoogleDrive(); err == nil {
		t.Error("a second connect while one runs must be refused")
	}
	if err := <-syncNow; err == nil {
		t.Error("SyncNow while connecting must be refused")
	}
	time.Sleep(150 * time.Millisecond) // the debounce fires meanwhile
	release()
	r := <-connected
	if r.err != nil || r.st.State != "merge_pending" {
		t.Fatalf("%+v %v", r.st, r.err)
	}
	time.Sleep(150 * time.Millisecond)
	if cards, _ := allCards(app.db); len(cards) != 1 {
		t.Errorf("nothing may be applied before the answer: %d cards", len(cards))
	}
	if len(f.files) != 1 {
		t.Errorf("nothing may be uploaded before the answer: %d files", len(f.files))
	}
	st, _ := app.ConfirmMerge(true)
	if cards, _ := allCards(app.db); st.State != "up_to_date" || len(cards) != 2 || len(f.files) != 2 {
		t.Errorf("after merge: %+v, %d cards, %d files", st, len(cards), len(f.files))
	}
}

// A disconnect while the question is being computed wins: the pending
// connect gives up without asking.
func TestConnectCancelledByDisconnect(t *testing.T) {
	app, f := syncApp(t)
	seedMergeCase(t, app, f)
	entered, release := blockDownloads(t, f)
	connected := make(chan connectResult, 1)
	go func() {
		st, err := app.ConnectGoogleDrive()
		connected <- connectResult{st, err}
	}()
	<-entered
	if err := app.DisconnectGoogleDrive(); err != nil {
		t.Fatal(err)
	}
	r := <-connected // returned before the download is released: cancelled
	release()
	if r.err == nil {
		t.Error("the cancelled connect must return an error")
	}
	st := app.GetSyncStatus()
	if st.State != "disconnected" || st.Connected || st.Merge != nil {
		t.Errorf("%+v", st)
	}
	if _, _, ok, _ := loadGoogleToken(app.db); ok {
		t.Error("no token after the disconnect")
	}
	if _, ok, _ := getSetting(app.db, settingSyncMergePending); ok {
		t.Error("no merge question after the disconnect")
	}
	if _, err := app.ConfirmMerge(true); err == nil {
		t.Error("no question to answer")
	}
}

func TestWriteBindingsTriggerASync(t *testing.T) {
	app, _ := syncApp(t)
	armed := 0
	app.sync.onChange = func() { armed++ } // seam called by changed() before arming the timer
	app.NewCustomCollection("To sell")
	rid, _ := insertCard(app.db, 0, "OP01-006_p4", "onepiece", "tcg", false, "en")
	card, _ := getCard(app.db, rid)
	card.Foil = true
	app.UpdateCard(card)
	app.MoveCardToCustom(rid, 0)
	app.DeleteCard(rid)
	app.SetPriceRef("OP01-006_p4", 1) // fails (no mapping): must NOT arm
	if armed != 4 {
		t.Errorf("armed %d times, want 4 (create, update, move, delete)", armed)
	}
}
