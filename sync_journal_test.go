package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestEncodeEventOnlyCarriesTheOpsFields(t *testing.T) {
	line, err := encodeEvent(syncEvent{Op: opCopyAdd, At: "2026-10-19T14:02:11.120Z", UID: "c0ff", CardID: "OP01-006_p4", Game: "onepiece",
		Foil: boolPtr(false), Lang: "en", Collection: strPtr(""), ScannedAt: "2026-10-19T14:02:11.120Z", Model: "tcg"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"op":"copy.add","at":"2026-10-19T14:02:11.120Z","uid":"c0ff","card_id":"OP01-006_p4","game":"onepiece","lang":"en","scanned_at":"2026-10-19T14:02:11.120Z","model":"tcg","foil":false,"collection":""}`
	var got, exp map[string]any
	json.Unmarshal(line, &got)
	json.Unmarshal([]byte(want), &exp)
	if !reflect.DeepEqual(got, exp) || bytes.Contains(line, []byte("\n")) {
		t.Errorf("got %s", line)
	}
	line, _ = encodeEvent(syncEvent{Op: opCollectionRename, At: "2026-10-19T14:02:11.120Z", UID: "9a1e", Name: "For trade"})
	if bytes.Contains(line, []byte("foil")) || bytes.Contains(line, []byte("seq")) {
		t.Errorf("rename carries foreign fields: %s", line)
	}
	if _, err := encodeEvent(syncEvent{Op: "copy.teleport", At: "2026-10-19T14:02:11.120Z"}); err == nil {
		t.Error("an unknown op must not be encoded")
	}
}

func TestDecodeJournalSkipsWhatItCannotRead(t *testing.T) {
	long := strings.Repeat("x", 200_000) // longer than bufio.Scanner's default buffer
	in := `{"v":1,"op":"device","device":"d1","name":"PC","app":"desktop","at":"2026-10-19T14:00:00.000Z"}
{"v":1,"seq":1,"at":"2026-10-19T14:01:00.000Z","op":"collection.create","uid":"9a1e","name":"To sell","future_field":3}
not json at all
{"v":1,"seq":2,"at":"2026-10-19T14:02:00.000Z","op":"copy.teleport","uid":"zz"}
{"v":1,"seq":3,"at":"2026-10-19T14:03:00.000Z","op":"price_ref.set","card_id":"OP01-006_p4","product":799296,"non_english":799297,"label":"` + long + `"}
`
	device, events, skipped, err := decodeJournal(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if device.Device != "d1" || device.Name != "PC" || device.App != "desktop" {
		t.Errorf("device %+v", device)
	}
	if len(events) != 2 || events[0].Op != opCollectionCreate || events[0].Seq != 1 || events[1].Seq != 3 || len(events[1].Label) != 200_000 {
		t.Errorf("events %+v", events)
	}
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2 (garbage line, unknown op)", skipped)
	}
	if _, _, _, err := decodeJournal(strings.NewReader(`{"v":1,"seq":1,"op":"copy.remove","uid":"x","at":"2026-10-19T14:00:00.000Z"}` + "\n")); err == nil {
		t.Error("a journal must start with a device record")
	}
	if _, _, _, err := decodeJournal(strings.NewReader("")); err == nil {
		t.Error("an empty journal is an error")
	}
}

func TestJournalNamesAndTimes(t *testing.T) {
	if syncJournalName("3f2c") != "journal-3f2c.jsonl" {
		t.Error(syncJournalName("3f2c"))
	}
	if id, ok := syncJournalDevice("journal-3f2c.jsonl"); !ok || id != "3f2c" {
		t.Error(id, ok)
	}
	if _, ok := syncJournalDevice("notes.txt"); ok {
		t.Error("not a journal")
	}
	at := formatSyncAt(time.Date(2026, 10, 19, 14, 2, 11, 120_000_000, time.FixedZone("CEST", 7200)))
	if at != "2026-10-19T12:02:11.120Z" {
		t.Error(at)
	}
	if tm, err := parseSyncAt(at); err != nil || tm.Nanosecond() != 120_000_000 {
		t.Error(tm, err)
	}
	if _, err := parseSyncAt("yesterday"); err == nil {
		t.Error("unparsable at must error")
	}
	if !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`).MatchString(syncNow()) {
		t.Error(syncNow())
	}
}
