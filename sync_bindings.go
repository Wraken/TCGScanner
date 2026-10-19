package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"golang.org/x/oauth2"
)

// The sync bindings and the connect flow: the first-connection question,
// the cycle slot the connect flow holds, connecting and disconnecting.

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
