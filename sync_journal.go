package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// The journal format shared by the desktop and the mobile app: one file per
// device, one JSON object per line, append-only. See the collection sync
// design, section "The journal format".

const syncFormatVersion = 1

// syncFolderName is the Drive folder that holds every device's journal.
const syncFolderName = "TCGScanner"

const (
	opDevice           = "device"
	opCollectionCreate = "collection.create"
	opCollectionRename = "collection.rename"
	opCollectionDelete = "collection.delete"
	opCopyAdd          = "copy.add"
	opCopyUpdate       = "copy.update"
	opCopyRemove       = "copy.remove"
	opPriceRefSet      = "price_ref.set"
	opPriceRefClear    = "price_ref.clear"
)

const (
	syncJournalPrefix = "journal-"
	syncJournalSuffix = ".jsonl"
	syncAtLayout      = "2006-01-02T15:04:05.000Z"
)

// syncEvent is one journal line. Pointer fields serialise their zero value
// ("foil": false, "collection": "") only on the ops that carry them; every
// other field is omitted when empty so an event holds just its op's fields.
type syncEvent struct {
	V   int    `json:"v"`
	Op  string `json:"op"`
	Seq int64  `json:"seq,omitempty"`
	At  string `json:"at"`

	// Device record (first line of a journal).
	Device string `json:"device,omitempty"`
	Name   string `json:"name,omitempty"`
	App    string `json:"app,omitempty"`

	// Collection and copy events.
	UID        string  `json:"uid,omitempty"`
	CardID     string  `json:"card_id,omitempty"`
	Game       string  `json:"game,omitempty"`
	Lang       string  `json:"lang,omitempty"`
	ScannedAt  string  `json:"scanned_at,omitempty"`
	Model      string  `json:"model,omitempty"`
	Foil       *bool   `json:"foil,omitempty"`
	Collection *string `json:"collection,omitempty"`

	// Price reference events.
	Product    int    `json:"product,omitempty"`
	NonEnglish int    `json:"non_english,omitempty"`
	Label      string `json:"label,omitempty"`
}

// isKnownSyncOp reports whether op is one of the nine ops of the format
// (the device record included).
func isKnownSyncOp(op string) bool {
	switch op {
	case opDevice, opCollectionCreate, opCollectionRename, opCollectionDelete,
		opCopyAdd, opCopyUpdate, opCopyRemove, opPriceRefSet, opPriceRefClear:
		return true
	}
	return false
}

// encodeEvent renders one journal line, without the trailing newline. The
// version is forced so callers cannot write a line other readers would
// misjudge, and an unknown op is refused: decoders skip unknown ops for
// forward compatibility, but an encoder must never produce one.
func encodeEvent(e syncEvent) ([]byte, error) {
	if !isKnownSyncOp(e.Op) {
		return nil, fmt.Errorf("sync: unknown op %q", e.Op)
	}
	e.V = syncFormatVersion
	return json.Marshal(e)
}

// decodeJournal reads a whole journal. The first non-blank line must be the
// device record. Later lines that are not valid JSON, carry an unknown op or
// an unparsable "at" are skipped and counted, never fatal: a journal written
// by a newer app must still be readable. Lines are read with no length limit
// since a bufio.Scanner would silently stop at 64 KiB.
func decodeJournal(r io.Reader) (device syncEvent, events []syncEvent, skipped int, err error) {
	reader := bufio.NewReader(r)
	haveDevice := false
	for lineNumber := 1; ; lineNumber++ {
		raw, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return device, events, skipped, readErr
		}
		line := strings.TrimSpace(strings.TrimRight(raw, "\r\n"))
		if line != "" {
			var e syncEvent
			if jsonErr := json.Unmarshal([]byte(line), &e); jsonErr != nil {
				if !haveDevice {
					return device, nil, skipped, fmt.Errorf("sync: journal does not start with a device record: %w", jsonErr)
				}
				skipped++
				fmt.Printf("sync: skipped line %d of journal: %v\n", lineNumber, jsonErr)
			} else if !haveDevice {
				if e.Op != opDevice || e.Device == "" {
					return device, nil, skipped, errors.New("sync: journal does not start with a device record")
				}
				device = e
				haveDevice = true
			} else if !isKnownSyncOp(e.Op) || e.Op == opDevice {
				skipped++
				fmt.Printf("sync: skipped line %d of journal: unexpected op %q\n", lineNumber, e.Op)
			} else if _, atErr := parseSyncAt(e.At); atErr != nil {
				skipped++
				fmt.Printf("sync: skipped line %d of journal: %v\n", lineNumber, atErr)
			} else {
				events = append(events, e)
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	if !haveDevice {
		return device, nil, skipped, errors.New("sync: empty journal, no device record")
	}
	return device, events, skipped, nil
}

// syncJournalName is the Drive file name of a device's journal.
func syncJournalName(deviceID string) string {
	return syncJournalPrefix + deviceID + syncJournalSuffix
}

// syncJournalDevice is the inverse of syncJournalName; false for any other
// file name in the folder.
func syncJournalDevice(name string) (string, bool) {
	if !strings.HasPrefix(name, syncJournalPrefix) || !strings.HasSuffix(name, syncJournalSuffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, syncJournalPrefix), syncJournalSuffix)
	return id, id != ""
}

// formatSyncAt renders a time as UTC with millisecond precision, the one
// timestamp shape of the format.
func formatSyncAt(t time.Time) string { return t.UTC().Format(syncAtLayout) }

func syncNow() string { return formatSyncAt(time.Now()) }

func parseSyncAt(s string) (time.Time, error) { return time.Parse(syncAtLayout, s) }

func boolPtr(b bool) *bool { return &b }

func strPtr(s string) *string { return &s }
