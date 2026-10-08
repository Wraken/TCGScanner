package main

import "testing"

func TestPriceRefsRoundTrip(t *testing.T) {
	db := testDB(t)
	refs, err := getPriceRefs(db)
	if err != nil || len(refs) != 0 {
		t.Fatalf("fresh db: %v, %v", refs, err)
	}
	if err := setPriceRef(db, PriceRef{CardID: "OP01-006_p4", Product: 799296, NonEnglish: 799297, Label: "Jolly Roger Foil (OP01-006j) · PRB-01"}); err != nil {
		t.Fatal(err)
	}
	// A second choice replaces the first.
	if err := setPriceRef(db, PriceRef{CardID: "OP01-006_p4", Product: 799298, Label: "Full Art (OP01-006f) · PRB-01"}); err != nil {
		t.Fatal(err)
	}
	refs, _ = getPriceRefs(db)
	got := refs["OP01-006_p4"]
	if got.Product != 799298 || got.NonEnglish != 0 || got.Label != "Full Art (OP01-006f) · PRB-01" || got.ChosenAt == "" {
		t.Errorf("after two sets: %+v", got)
	}
	if err := clearPriceRef(db, "OP01-006_p4"); err != nil {
		t.Fatal(err)
	}
	if err := clearPriceRef(db, "never-set"); err != nil {
		t.Errorf("clearing an unset id must be a no-op, got %v", err)
	}
	refs, _ = getPriceRefs(db)
	if len(refs) != 0 {
		t.Errorf("after clear: %v", refs)
	}
}

func TestPriceRefsMigrationIsIdempotent(t *testing.T) {
	db := testDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'price_refs'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("price_refs table: %d, %v", n, err)
	}
}
