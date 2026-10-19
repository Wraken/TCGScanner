package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/oauth2"
)

// The sync engine: one cycle pulls the other devices' journals from the Drive
// folder, folds their new events into the rows, and pushes this device's
// journal. See the collection sync design, "The sync cycle".

const (
	syncStatusEvent        = "sync:status"
	collectionChangedEvent = "collection:changed"
)

const (
	settingSyncFolderID = "sync_folder_id"
	settingSyncFileID   = "sync_file_id"
	// Set while the first-connection question is unanswered, so a restart
	// in between does not merge without asking.
	settingSyncMergePending = "sync_merge_pending"
)

const (
	syncStateDisconnected = "disconnected"
	syncStateMergePending = "merge_pending"
	syncStateSyncing      = "syncing"
	syncStateUpToDate     = "up_to_date"
	syncStateOffline      = "offline"
	syncStateError        = "error"
	syncStateSignedOut    = "signed_out"
	syncStateUnconfigured = "unconfigured"
)

type SyncDevice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	App        string `json:"app"`
	LastChange string `json:"last_change"`
	ThisDevice bool   `json:"this_device"`
}

type SyncMergeQuestion struct {
	RemoteCopies  int      `json:"remote_copies"`
	RemoteDevices []string `json:"remote_devices"`
	LocalCopies   int      `json:"local_copies"`
}

// SyncStatus is what the frontend shows. State is one of the syncState*
// values; Message is empty when up to date.
type SyncStatus struct {
	Connected bool               `json:"connected"`
	Email     string             `json:"email"`
	State     string             `json:"state"`
	Message   string             `json:"message"`
	LastSync  string             `json:"last_sync"`
	Devices   []SyncDevice       `json:"devices"`
	Merge     *SyncMergeQuestion `json:"merge"`
}

// syncer runs one cycle at a time: a cycle asked for while one runs is
// coalesced into a single rerun after it. Every field is guarded by mu.
type syncer struct {
	app *App

	mu         sync.Mutex
	status     SyncStatus
	configured bool // a real Google client id is compiled in
	stopped    bool

	// The running slot is held by a cycle or by the connect flow
	// (connecting), from before the token is saved until the question is
	// asked or the first cycle may run: no cycle runs meanwhile, and a
	// disconnect cancels and waits for either alike.
	running, again bool
	connecting     bool
	cancel         context.CancelFunc // the slot holder's
	done           chan struct{}      // closed when the slot is released
	lastErr        error

	debounce, retry   time.Duration
	timer, retryTimer *time.Timer

	// Seams: newDrive builds the Drive client from the signed-in HTTP client
	// (tests: the fake Drive); open opens the browser (tests: a fake redirect).
	newDrive func(ctx context.Context) (*driveClient, error)
	open     func(url string) error

	// onChange, when set (tests), is called by changed() before it arms the timer.
	onChange func()
	// onStatus, when set (tests), sees every status emitted, under mu: it
	// must not call back into the syncer.
	onStatus func(SyncStatus)
	// onRelease, when set (tests), runs as the connect flow is about to give
	// the slot back, outside mu.
	onRelease func()
}

func newSyncer(a *App) *syncer {
	s := &syncer{app: a, debounce: 5 * time.Second, retry: 5 * time.Minute, configured: googleConfigured()}
	s.newDrive = func(ctx context.Context) (*driveClient, error) {
		c, err := googleHTTPClient(ctx, a.db)
		if err != nil {
			return nil, err
		}
		return &driveClient{HTTP: c, Base: driveBase, Upload: driveUpload}, nil
	}
	s.open = func(url string) error {
		runtime.BrowserOpenURL(a.ctx, url)
		return nil
	}
	s.status.State = syncStateDisconnected
	if !s.configured {
		s.status.State = syncStateUnconfigured
	}
	return s
}

func (s *syncer) baseCtx() context.Context {
	if s.app.ctx != nil {
		return s.app.ctx
	}
	return context.Background()
}

// ── Status ─────────────────────────────────────────────────────────────────

func (s *syncer) snapshotLocked() SyncStatus {
	st := s.status
	st.Devices = slices.Clone(st.Devices)
	if st.Merge != nil {
		m := *st.Merge
		m.RemoteDevices = slices.Clone(m.RemoteDevices)
		st.Merge = &m
	}
	return st
}

func (s *syncer) snapshot() SyncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// emitLocked tells the frontend the status changed; the caller holds mu, so
// the events go out in the order of the changes.
func (s *syncer) emitLocked() {
	if s.onStatus != nil {
		s.onStatus(s.snapshotLocked())
	}
	if s.app.ctx != nil {
		runtime.EventsEmit(s.app.ctx, syncStatusEvent, s.snapshotLocked())
	}
}

// devices is this device, then every other device whose journal was read.
func (s *syncer) devices() []SyncDevice {
	db := s.app.db
	id, name, err := syncDevice(db)
	if err != nil {
		return nil
	}
	var last string
	_ = db.QueryRow(`SELECT at FROM sync_events ORDER BY seq DESC LIMIT 1`).Scan(&last)
	out := []SyncDevice{{ID: id, Name: name, App: "desktop", LastChange: last, ThisDevice: true}}
	sources, err := listSyncSources(db) // ordered by device id
	if err != nil {
		return out
	}
	for _, src := range sources {
		if src.DeviceID == id {
			continue
		}
		out = append(out, SyncDevice{ID: src.DeviceID, Name: src.Name, App: src.App, LastChange: src.LastChange})
	}
	return out
}

func (s *syncer) signedIn() bool {
	_, _, ok, err := loadGoogleToken(s.app.db)
	return err == nil && ok
}

func (s *syncer) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// classifySyncError maps a failed cycle to its state and message.
func classifySyncError(err error) (state, message string) {
	if errors.Is(err, errSignedOut) {
		return syncStateSignedOut, "Disconnected, sign in again"
	}
	var de *driveError
	if errors.As(err, &de) {
		if de.Status >= 500 || de.Status == 429 {
			return syncStateOffline, "Offline, will retry"
		}
		return syncStateError, "Sync failed: " + err.Error()
	}
	var ne net.Error // *url.Error included
	if errors.As(err, &ne) {
		return syncStateOffline, "Offline, will retry"
	}
	return syncStateError, "Sync failed: " + err.Error()
}

// ── Triggers ───────────────────────────────────────────────────────────────

// start runs at app start: a cycle when signed in.
func (s *syncer) start() {
	db := s.app.db
	s.mu.Lock()
	if !s.configured {
		s.status = SyncStatus{State: syncStateUnconfigured}
		s.emitLocked()
		s.mu.Unlock()
		return
	}
	if _, pending, _ := getSetting(db, settingSyncMergePending); pending {
		// The app was closed on an unanswered first-connection question:
		// back to disconnected rather than merging without the answer.
		if err := errors.Join(clearGoogleToken(db), deleteSettings(db, settingSyncMergePending)); err != nil {
			fmt.Printf("sync: %v\n", err)
		}
	}
	_, email, ok, err := loadGoogleToken(db)
	if err != nil {
		fmt.Printf("sync: %v\n", err)
	}
	if !ok {
		s.status = SyncStatus{State: syncStateDisconnected}
		s.emitLocked()
		s.mu.Unlock()
		return
	}
	s.status = SyncStatus{Connected: true, Email: email, State: syncStateSyncing, Devices: s.devices()}
	s.emitLocked()
	s.mu.Unlock()
	go s.runCycle(s.baseCtx())
}

// changed runs after a local write: (re)arm the debounce timer.
func (s *syncer) changed() {
	if s.onChange != nil {
		s.onChange()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.debounce, s.timerCycle)
}

// timerCycle is the debounce and retry timers' cycle: only when signed in
// and not waiting for the first-connection answer.
func (s *syncer) timerCycle() {
	s.mu.Lock()
	skip := s.stopped || !s.configured || s.connecting || s.status.State == syncStateMergePending
	s.mu.Unlock()
	if skip || !s.signedIn() {
		return
	}
	s.runCycle(s.baseCtx())
}

func (s *syncer) stopTimersLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.retryTimer != nil {
		s.retryTimer.Stop()
		s.retryTimer = nil
	}
}

// cancelAndWaitLocked cancels the running cycle and waits for it to end;
// mu is held on entry and on return, released while waiting.
func (s *syncer) cancelAndWaitLocked() {
	for s.running {
		if s.cancel != nil {
			s.cancel()
		}
		done := s.done
		s.mu.Unlock()
		<-done
		s.mu.Lock()
	}
}

// stop ends the syncer (app shutdown, tests): no more timers, the running
// cycle cancelled and waited for.
func (s *syncer) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.stopTimersLocked()
	s.cancelAndWaitLocked()
}

// ── The cycle ──────────────────────────────────────────────────────────────

// runCycle runs the sync cycle; a call made while one runs waits for the
// coalesced rerun and returns its result. The outcome is in the status.
func (s *syncer) runCycle(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.again = true
		done := s.done
		s.mu.Unlock()
		<-done
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.lastErr
	}
	s.running = true
	s.done = make(chan struct{})
	defer func() {
		s.running = false
		s.cancel = nil
		close(s.done)
		s.mu.Unlock()
	}()
	for {
		s.again = false
		if s.stopped {
			s.lastErr = nil
			return nil
		}
		_, email, ok, err := loadGoogleToken(s.app.db)
		if err != nil || !ok { // disconnected meanwhile: nothing to do
			s.lastErr = err
			return err
		}
		cctx, cancel := context.WithCancel(ctx)
		s.cancel = cancel
		s.status.Connected = true
		s.status.Email = email
		s.status.State = syncStateSyncing
		s.status.Message = ""
		s.status.Merge = nil
		s.emitLocked()
		s.mu.Unlock()

		applied, err := s.cycle(cctx)
		cancelled := cctx.Err() != nil
		cancel()

		s.mu.Lock()
		s.cancel = nil
		if cancelled && err == nil {
			err = context.Canceled
		}
		s.lastErr = err
		if applied > 0 && s.app.ctx != nil {
			runtime.EventsEmit(s.app.ctx, collectionChangedEvent)
		}
		if cancelled {
			// Cut short by a disconnect or shutdown: whoever cancelled
			// sets the status.
			return err
		}
		s.finishLocked(err)
		if err != nil || !s.again {
			return err
		}
	}
}

// finishLocked records the outcome of a cycle in the status.
func (s *syncer) finishLocked(err error) {
	if err == nil {
		s.status.State = syncStateUpToDate
		s.status.Message = ""
		s.status.LastSync = time.Now().Format("15:04")
		s.status.Devices = s.devices()
		if s.retryTimer != nil {
			s.retryTimer.Stop()
			s.retryTimer = nil
		}
		s.emitLocked()
		return
	}
	fmt.Printf("sync: %v\n", err)
	state, message := classifySyncError(err)
	s.status.State = state
	s.status.Message = message
	switch state {
	case syncStateSignedOut:
		if cerr := clearGoogleToken(s.app.db); cerr != nil {
			fmt.Printf("sync: %v\n", cerr)
		}
		s.status.Connected = false
		s.status.Devices = nil
	case syncStateOffline:
		if s.retryTimer != nil {
			s.retryTimer.Stop()
		}
		s.retryTimer = time.AfterFunc(s.retry, s.timerCycle)
		s.status.Devices = s.devices()
	default:
		s.status.Devices = s.devices()
	}
	s.emitLocked()
}

// cycle is one pass: folder, the other journals' new events applied in one
// batch, this device's journal uploaded. applied counts the changed rows.
func (s *syncer) cycle(ctx context.Context) (applied int, err error) {
	db := s.app.db
	drive, err := s.newDrive(ctx)
	if err != nil {
		return 0, err
	}
	ourID, _, err := syncDevice(db)
	if err != nil {
		return 0, err
	}
	folder, err := s.ensureFolder(ctx, drive)
	if err != nil {
		return 0, err
	}
	files, err := drive.listJournals(ctx, folder)
	if err != nil {
		return 0, err
	}
	sources, err := listSyncSources(db)
	if err != nil {
		return 0, err
	}
	known := map[string]syncSource{}
	for _, src := range sources {
		known[src.DeviceID] = src
	}

	// Every changed journal's new events go into ONE batch: a copy added on
	// one device into a collection created on another needs both.
	var batch []syncSourced
	var seen []syncSource
	ownListed := ""
	for _, f := range files {
		dev, ok := syncJournalDevice(f.Name)
		if !ok {
			continue
		}
		if dev == ourID {
			if ownListed == "" {
				ownListed = f.ID
			}
			continue
		}
		src := known[dev]
		if src.FileID == f.ID && src.ModifiedTime == f.ModifiedTime {
			continue // unchanged since it was last read
		}
		data, err := drive.download(ctx, f.ID)
		if err != nil {
			var de *driveError
			if errors.As(err, &de) && de.Status == 404 {
				continue // deleted since the listing
			}
			return 0, err
		}
		device, events, _, err := decodeJournal(bytes.NewReader(data))
		if err != nil {
			fmt.Printf("sync: journal %s ignored: %v\n", f.Name, err)
			continue
		}
		from := src.AppliedSeq
		var maxSeq int64
		for _, e := range events {
			maxSeq = max(maxSeq, e.Seq)
		}
		if maxSeq < from {
			from = 0 // the device was reset: its journal starts over
		}
		for _, e := range events {
			if e.Seq > from {
				batch = append(batch, syncSourced{Device: dev, Event: e})
			}
		}
		lastChange := src.LastChange
		if len(events) > 0 {
			lastChange = events[len(events)-1].At
		}
		seen = append(seen, syncSource{DeviceID: dev, Name: device.Name, App: device.App, FileID: f.ID, ModifiedTime: f.ModifiedTime, LastChange: lastChange})
	}
	if len(batch) > 0 {
		if applied, err = applySyncEvents(db, batch, gameResolver(allModelImagesDirs())); err != nil {
			return 0, err
		}
	}
	// Recorded after the apply: a failed apply leaves the journal to be
	// downloaded again next cycle.
	for _, src := range seen {
		if err := upsertSyncSource(db, src); err != nil {
			return applied, err
		}
	}
	return applied, s.upload(ctx, drive, folder, ourID, ownListed)
}

// ensureFolder returns the Drive folder's id: the oldest folder found by
// name (one listing per cycle: a folder another device created first wins),
// else a new one. An id other than the cached one drops the cached journal
// file id: this device's journal is not in that folder.
func (s *syncer) ensureFolder(ctx context.Context, drive *driveClient) (string, error) {
	db := s.app.db
	cached, _, err := getSetting(db, settingSyncFolderID)
	if err != nil {
		return "", err
	}
	id, err := drive.findFolder(ctx, syncFolderName)
	if err != nil {
		return "", err
	}
	if id == "" {
		if id, err = drive.createFolder(ctx, syncFolderName); err != nil {
			return "", err
		}
	}
	if id != cached {
		if err := deleteSettings(db, settingSyncFileID, settingSyncUploadedSeq); err != nil {
			return "", err
		}
		if err := setSetting(db, settingSyncFolderID, id); err != nil {
			return "", err
		}
	}
	return id, nil
}

// upload pushes this device's whole journal when it has events not yet
// uploaded, or when its file is missing from the folder. ownListed is the
// id of the file with this device's journal name in the listing, "" if none.
func (s *syncer) upload(ctx context.Context, drive *driveClient, folder, ourID, ownListed string) error {
	db := s.app.db
	lastSeq, err := lastSyncEventSeq(db)
	if err != nil {
		return err
	}
	uploadedRaw, _, err := getSetting(db, settingSyncUploadedSeq)
	if err != nil {
		return err
	}
	uploaded, _ := strconv.ParseInt(uploadedRaw, 10, 64)
	fileID, _, err := getSetting(db, settingSyncFileID)
	if err != nil {
		return err
	}
	switch {
	case ownListed == "" && fileID != "":
		// Not in the listing: deleted, or in the Drive trash, where an
		// update would still succeed and no other device would see it.
		fileID = ""
		if err := deleteSettings(db, settingSyncFileID); err != nil {
			return err
		}
	case fileID == "" && ownListed != "": // e.g. connected again after a disconnect
		fileID = ownListed
		if err := setSetting(db, settingSyncFileID, fileID); err != nil {
			return err
		}
	}
	if ownListed != "" && lastSeq <= uploaded {
		return nil
	}
	content, lastSeq, err := renderJournal(db)
	if err != nil {
		return err
	}
	if fileID != "" {
		err := drive.update(ctx, fileID, []byte(content))
		var de *driveError
		if errors.As(err, &de) && de.Status == 404 {
			fileID = "" // deleted on Drive: re-created below with the full journal
		} else if err != nil {
			return err
		}
	}
	if fileID == "" {
		if fileID, err = drive.create(ctx, folder, syncJournalName(ourID), []byte(content)); err != nil {
			return err
		}
		if err := setSetting(db, settingSyncFileID, fileID); err != nil {
			return err
		}
	}
	return setSetting(db, settingSyncUploadedSeq, strconv.FormatInt(lastSeq, 10))
}

// ── First connection ───────────────────────────────────────────────────────

// mergeQuestion is the question to ask before the first cycle, nil when
// there is nothing to ask: this device has no copies, it synced before, or
// no other device has written anything.
func (s *syncer) mergeQuestion(ctx context.Context) (*SyncMergeQuestion, error) {
	db := s.app.db
	var local int
	if err := db.QueryRow(`SELECT count(*) FROM cards`).Scan(&local); err != nil {
		return nil, err
	}
	if local == 0 {
		return nil, nil
	}
	sources, err := listSyncSources(db)
	if err != nil || len(sources) > 0 {
		return nil, err
	}
	drive, err := s.newDrive(ctx)
	if err != nil {
		return nil, err
	}
	folder, err := s.ensureFolder(ctx, drive)
	if err != nil {
		return nil, err
	}
	files, err := drive.listJournals(ctx, folder)
	if err != nil {
		return nil, err
	}
	ourID, _, err := syncDevice(db)
	if err != nil {
		return nil, err
	}
	added, removed := map[string]bool{}, map[string]bool{}
	var names []string
	events := 0
	for _, f := range files {
		dev, ok := syncJournalDevice(f.Name)
		if !ok || dev == ourID {
			continue
		}
		data, err := drive.download(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		device, evs, _, err := decodeJournal(bytes.NewReader(data))
		if err != nil {
			fmt.Printf("sync: journal %s ignored: %v\n", f.Name, err)
			continue
		}
		name := device.Name
		if name == "" {
			name = dev
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
		events += len(evs)
		for _, e := range evs {
			switch e.Op {
			case opCopyAdd:
				added[e.UID] = true
			case opCopyRemove: // removals are final
				removed[e.UID] = true
			}
		}
	}
	if events == 0 {
		return nil, nil
	}
	remote := 0
	for uid := range added {
		if !removed[uid] {
			remote++
		}
	}
	return &SyncMergeQuestion{RemoteCopies: remote, RemoteDevices: names, LocalCopies: local}, nil
}

// ── Bindings ───────────────────────────────────────────────────────────────

var errNoSyncer = errors.New("database not initialized")

func (a *App) GetSyncStatus() SyncStatus {
	if a.sync == nil {
		return SyncStatus{State: syncStateDisconnected}
	}
	return a.sync.snapshot()
}

// ConnectGoogleDrive signs in, then either runs the first cycle or, when
// both this device and the Drive hold copies, asks before merging them.
// A failed cycle is reported in the status, not as an error.
func (a *App) ConnectGoogleDrive() (SyncStatus, error) {
	if a.sync == nil {
		return SyncStatus{State: syncStateDisconnected}, errNoSyncer
	}
	s := a.sync
	if !s.configured {
		return s.snapshot(), errors.New("Google client id not configured")
	}
	ctx, err := s.acquireForConnect()
	if err != nil {
		return s.snapshot(), err
	}
	errCancelled := errors.New("Google Drive connection cancelled")

	tok, email, err := connectGoogle(ctx, s.open)
	if err != nil {
		cancelled := ctx.Err() != nil // read before release cancels ctx
		s.release(nil)
		if cancelled {
			return s.snapshot(), errCancelled
		}
		return s.snapshot(), err
	}
	s.mu.Lock()
	if ctx.Err() != nil { // disconnected meanwhile: the canceller set the status
		s.mu.Unlock()
		s.release(nil)
		return s.snapshot(), errCancelled
	}
	if err := connectAccount(a.db, tok, email); err != nil {
		s.mu.Unlock()
		s.release(nil)
		return s.snapshot(), err
	}
	s.status = SyncStatus{Connected: true, Email: email, State: syncStateSyncing, Devices: s.devices()}
	s.emitLocked()
	s.mu.Unlock()

	q, err := s.mergeQuestion(ctx)
	if ctx.Err() != nil {
		s.release(nil)
		return s.snapshot(), errCancelled
	}
	if err != nil {
		// Keeping the sign-in would let a later cycle merge without asking:
		// forgotten while the slot is still held, so none can run between.
		fmt.Printf("sync: %v\n", err)
		if derr := s.forgetDrive(); derr != nil {
			fmt.Printf("sync: %v\n", derr)
		}
		s.release(func() bool {
			s.status = SyncStatus{State: syncStateDisconnected}
			return true
		})
		return s.snapshot(), fmt.Errorf("could not read Google Drive: %w", err)
	}
	if q == nil {
		s.release(nil)
		s.runCycle(s.baseCtx())
		return s.snapshot(), nil
	}
	if err := setSetting(a.db, settingSyncMergePending, "1"); err != nil {
		s.release(nil)
		return s.snapshot(), err
	}
	// The question is published as the slot is released, under one lock: an
	// answer can only come once a cycle may run.
	var st SyncStatus
	cancelled := false
	s.release(func() bool {
		if ctx.Err() != nil { // disconnected meanwhile: the canceller sets the status
			cancelled = true
			return false
		}
		s.status.State = syncStateMergePending
		s.status.Merge = q
		st = s.snapshotLocked()
		return true
	})
	if cancelled {
		return s.snapshot(), errCancelled
	}
	return st, nil
}

// connectAccount saves the new sign-in. Another account than the last one
// starts the sync afresh: the other devices' journals and this device's
// file it knew of are in another Drive.
func connectAccount(db *sql.DB, tok *oauth2.Token, email string) error {
	changed, err := googleAccountChanged(db, email)
	if err != nil {
		return err
	}
	if changed {
		if err := errors.Join(
			clearSyncSources(db),
			deleteSettings(db, settingSyncFolderID, settingSyncFileID, settingSyncUploadedSeq),
		); err != nil {
			return err
		}
	}
	return saveGoogleToken(db, tok, email)
}

// acquireForConnect takes the running slot for the connect flow, after the
// running cycle if any, and returns the flow's cancellable context.
func (s *syncer) acquireForConnect() (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connecting {
		return nil, errors.New("already connecting to Google Drive")
	}
	if s.stopped {
		return nil, errors.New("sync stopped")
	}
	s.connecting = true
	for s.running {
		done := s.done
		s.mu.Unlock()
		<-done
		s.mu.Lock()
	}
	ctx, cancel := context.WithCancel(s.baseCtx())
	s.running = true
	s.cancel = cancel
	s.done = make(chan struct{})
	return ctx, nil
}

// release gives the running slot back after the connect flow. set, when
// not nil, updates the status under the same lock (before the flow's
// context is cancelled, so it can still tell a disconnect apart): nobody sees
// the new status with the slot held, nor the slot free with the old status.
// The status is emitted when set returns true.
func (s *syncer) release(set func() bool) {
	if s.onRelease != nil {
		s.onRelease()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	emit := set != nil && set()
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = nil
	s.running = false
	s.connecting = false
	close(s.done)
	if emit {
		s.emitLocked()
	}
}

// ConfirmMerge answers the first-connection question: merge runs the
// cycle, not now disconnects.
func (a *App) ConfirmMerge(merge bool) (SyncStatus, error) {
	if a.sync == nil {
		return SyncStatus{State: syncStateDisconnected}, errNoSyncer
	}
	s := a.sync
	s.mu.Lock()
	pending := s.status.State == syncStateMergePending
	s.mu.Unlock()
	if !pending {
		return s.snapshot(), errors.New("no merge question pending")
	}
	if !merge {
		err := a.DisconnectGoogleDrive()
		return s.snapshot(), err
	}
	if err := deleteSettings(a.db, settingSyncMergePending); err != nil {
		return s.snapshot(), err
	}
	s.runCycle(s.baseCtx())
	return s.snapshot(), nil
}

// DisconnectGoogleDrive stops the running cycle, forgets the sign-in and
// where this device's journal lives. Local events keep being recorded, and
// how far the other devices' journals were applied is kept: the same account
// connected again resumes without the question.
func (a *App) DisconnectGoogleDrive() error {
	if a.sync == nil {
		return errNoSyncer
	}
	s := a.sync
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopTimersLocked()
	// Wait for the running cycle: a token refresh in flight would write
	// the token back after the clear.
	s.cancelAndWaitLocked()
	err := s.forgetDrive()
	s.status = SyncStatus{State: syncStateDisconnected}
	s.emitLocked()
	return err
}

// forgetDrive is the database side of a disconnect; the caller holds the
// running slot or has waited for it, so no cycle runs meanwhile. The last
// account's email (settingSyncGoogleLastEmail) is kept.
func (s *syncer) forgetDrive() error {
	db := s.app.db
	return errors.Join(
		clearGoogleToken(db),
		deleteSettings(db, settingSyncFolderID, settingSyncFileID, settingSyncUploadedSeq, settingSyncMergePending),
		detachSyncSources(db),
	)
}

// SyncNow runs a cycle now; its outcome is in the status.
func (a *App) SyncNow() (SyncStatus, error) {
	if a.sync == nil {
		return SyncStatus{State: syncStateDisconnected}, errNoSyncer
	}
	s := a.sync
	s.mu.Lock()
	pending := s.status.State == syncStateMergePending
	connecting := s.connecting
	s.mu.Unlock()
	if connecting {
		return s.snapshot(), errors.New("connecting to Google Drive")
	}
	if pending {
		return s.snapshot(), errors.New("answer the merge question first")
	}
	if !s.signedIn() {
		return s.snapshot(), errors.New("not connected to Google Drive")
	}
	s.runCycle(s.baseCtx())
	return s.snapshot(), nil
}
