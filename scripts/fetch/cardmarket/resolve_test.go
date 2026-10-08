package main

import (
	"fmt"
	"strings"
	"testing"
)

// Two shelves of one set (10 English, 11 foreign), a third English shelf
// (12, the "Asia Region Legal" run that reads as English) and an event
// shelf (13). Products 1/2/3 are one printing on three shelves.
func shelfFixture() (map[int]cmProduct, map[int]shelfLang) {
	catalog := map[int]cmProduct{
		1: {IDProduct: 1, Name: "Monkey.D.Luffy (OP05-119)", IDExpansion: 10},
		2: {IDProduct: 2, Name: "Monkey.D.Luffy (OP05-119)", IDExpansion: 11},
		3: {IDProduct: 3, Name: "Monkey.D.Luffy (OP05-119)", IDExpansion: 12},
		5: {IDProduct: 5, Name: "Roronoa Zoro (OP05-001)", IDExpansion: 10},
		6: {IDProduct: 6, Name: "Roronoa Zoro (OP05-001)", IDExpansion: 13},
		7: {IDProduct: 7, Name: "Nami (OP05-002)", IDExpansion: 10},
		8: {IDProduct: 8, Name: "Nami (OP05-002)", IDExpansion: 11},
		9: {IDProduct: 9, Name: "Nami (OP05-002)", IDExpansion: 11},
	}
	shelf := map[int]shelfLang{10: shelfEnglish, 11: shelfForeign, 12: shelfEnglish, 13: shelfEvent}
	return catalog, shelf
}

func TestPickShelf(t *testing.T) {
	catalog, shelf := shelfFixture()
	shelfEnglishSealed = map[int]int{10: 15, 12: 3}
	cases := []struct {
		ids  []int
		want int
		ok   bool
	}{
		{[]int{2}, 2, true},       // a lone id is taken as is, whatever its shelf
		{[]int{0, 2}, 2, true},    // zeros are not ids
		{[]int{2, 1}, 1, true},    // English over foreign
		{[]int{1, 2, 3}, 1, true}, // two English shelves: the one with the booster box
		{[]int{2, 4}, 0, false},   // 4 is on no known shelf, 2 is foreign: nothing English
		{[]int{2, 6}, 0, false},   // foreign and event: nothing English
		{[]int{1, 3, 2}, 1, true}, // order does not matter
		{[]int{}, 0, false},       // nothing to pick from
		{[]int{1, 11}, 1, true},   // an id missing from the catalog is ignored
	}
	for _, c := range cases {
		got, ok := pickShelf(c.ids, shelf, catalog)
		if got != c.want || ok != c.ok {
			t.Errorf("pickShelf(%v) = %d, %v; want %d, %v", c.ids, got, ok, c.want, c.ok)
		}
	}
	shelfEnglishSealed = map[int]int{10: 3, 12: 3}
	if got, ok := pickShelf([]int{1, 2, 3}, shelf, catalog); ok {
		t.Errorf("pickShelf with two equal English shelves = %d, want unsettled", got)
	}
}

func TestNonEnglishTwins(t *testing.T) {
	catalog, shelf := shelfFixture()
	shelfEnglishSealed = map[int]int{10: 15, 12: 3}
	catalog[30] = cmProduct{IDProduct: 30, Name: "Usopp (OP05-004)", IDExpansion: 11}
	catalog[31] = cmProduct{IDProduct: 31, Name: "Usopp (OP05-004)", IDExpansion: 11}
	bps := []ctBlueprint{
		{ID: 1, ExpansionID: 100, CardMarketIDs: []int{1, 2, 3}}, // English + foreign + second English
		{ID: 2, ExpansionID: 100, CardMarketIDs: []int{5, 6}},    // English + event: no twin
		{ID: 3, ExpansionID: 100, CardMarketIDs: []int{7, 8, 9}}, // two foreign ids: twin not settled
		{ID: 4, ExpansionID: 100, CardMarketIDs: []int{20}},      // lone id
		{ID: 5, ExpansionID: 100, CardMarketIDs: []int{30, 31}},  // foreign shelves only
	}
	ix := indexBlueprints(map[int]ctExpansion{100: {ID: 100, Code: "op05"}}, bps)
	r := gameResult{Mapped: map[string]resolved{
		"OP05-119": {1, srcBlueprint},
		"OP05-001": {5, srcBlueprint},
		"OP05-002": {7, srcVisual},
		"OP05-003": {20, srcOverride},
	}}
	nonEnglishTwins(&r, ix, shelf, catalog)
	want := map[string]int{"OP05-119": 2}
	if len(r.NonEnglish) != len(want) {
		t.Errorf("twins = %v, want %v", r.NonEnglish, want)
	}
	for id, p := range want {
		if r.NonEnglish[id] != p {
			t.Errorf("twin of %s = %d, want %d", id, r.NonEnglish[id], p)
		}
	}

	// The visual pick lands on whichever shelf sorted first; rule B moves
	// it to the English one.
	if p, ok := visualShelf(2, ix, shelf, catalog); !ok || p != 1 {
		t.Errorf("visualShelf(2) = %d, %v; want 1, true", p, ok)
	}
	if p, ok := visualShelf(8, ix, shelf, catalog); !ok || p != 7 {
		t.Errorf("visualShelf(8) = %d, %v; want 7, true", p, ok)
	}
	if _, ok := visualShelf(30, ix, shelf, catalog); ok {
		t.Error("visualShelf(30) settled a printing with no English shelf")
	}
	if _, ok := visualShelf(99, ix, shelf, catalog); ok {
		t.Error("visualShelf(99) settled a product no blueprint lists")
	}
}

// One Piece OP05-119 on CardTrader: the plain print (products 1 English,
// 2 foreign), an alternate art (3 English, 4 foreign) and a manga art
// (5 English, 6 foreign). Our ids: the plain card, _p1 and _p2.
func manualFixture() (map[string]ourCard, map[int]ctExpansion, []ctBlueprint, map[int]cmProduct, map[int]shelfLang) {
	cards := map[string]ourCard{
		"OP05-119":    {Name: "Monkey.D.Luffy", Set: "OP05", Number: "119"},
		"OP05-119_p1": {Name: "Monkey.D.Luffy", Set: "OP05", Number: "119"},
		"OP05-119_p2": {Name: "Monkey.D.Luffy", Set: "OP05", Number: "119"},
	}
	exps := map[int]ctExpansion{100: {ID: 100, GameID: 15, Code: "op05"}}
	bp := func(id int, version string, cm ...int) ctBlueprint {
		b := ctBlueprint{ID: id, Version: version, ExpansionID: 100, CardMarketIDs: cm}
		b.FixedProperties.CollectorNumber = "OP05-119"
		return b
	}
	bps := []ctBlueprint{bp(1, "", 1, 2), bp(2, "Alternate Art", 3, 4), bp(3, "Manga", 5, 6)}
	catalog := map[int]cmProduct{}
	for p := 1; p <= 6; p++ {
		exp := 10
		if p%2 == 0 {
			exp = 11
		}
		catalog[p] = cmProduct{IDProduct: p, Name: "Monkey.D.Luffy (OP05-119)", IDExpansion: exp}
	}
	shelf := map[int]shelfLang{10: shelfEnglish, 11: shelfForeign}
	shelfEnglishSealed = map[int]int{10: 15}
	return cards, exps, bps, catalog, shelf
}

func TestResolveGameManualPickSettlesAmbiguity(t *testing.T) {
	cards, exps, bps, catalog, shelf := manualFixture()
	r := resolveGame("onepiece", cards, nil, exps, bps, nil, manualPicks{}, nil, catalog, shelf, "2026-10-08")
	if len(r.Ambiguous) != 2 || r.Mapped["OP05-119"].Product != 1 {
		t.Fatalf("without manual picks: mapped %v, ambiguous %v", r.Mapped, r.Ambiguous)
	}

	// The hand pick names a printing by any of its products (here the
	// foreign shelf's); the English shelf is chosen, as for a visual pick.
	manual := manualPicks{Picks: map[string]int{"OP05-119_p1": 4}}
	r = resolveGame("onepiece", cards, nil, exps, bps, nil, manual, nil, catalog, shelf, "2026-10-08")
	if got := r.Mapped["OP05-119_p1"]; got.Product != 3 || got.Source != srcManual {
		t.Errorf("OP05-119_p1 = %+v, want product 3 from manual (dropped: %v)", got, r.Dropped)
	}
	if len(r.Ambiguous) != 1 || r.Ambiguous[0].Card != "OP05-119_p2" {
		t.Errorf("ambiguous = %v, want OP05-119_p2 alone", r.Ambiguous)
	}
	if r.NonEnglish["OP05-119_p1"] != 4 {
		t.Errorf("non-English twin of the pick = %d, want 4", r.NonEnglish["OP05-119_p1"])
	}
}

func TestResolveGameManualPickOverridesBlueprint(t *testing.T) {
	cards, exps, bps, catalog, shelf := manualFixture()
	// The check pass found the plain card's auto pick wrong: the hand pick wins.
	manual := manualPicks{Picks: map[string]int{"OP05-119": 5}}
	r := resolveGame("onepiece", cards, nil, exps, bps, nil, manual, nil, catalog, shelf, "2026-10-08")
	if got := r.Mapped["OP05-119"]; got.Product != 5 || got.Source != srcManual {
		t.Errorf("OP05-119 = %+v, want product 5 from manual (dropped: %v)", got, r.Dropped)
	}
}

func TestResolveGameManualPickOutsideBlueprintsIsDropped(t *testing.T) {
	cards, exps, bps, catalog, shelf := manualFixture()
	catalog[99] = cmProduct{IDProduct: 99, Name: "Monkey.D.Luffy (OP05-119)", IDExpansion: 10}
	manual := manualPicks{Picks: map[string]int{"OP05-119_p1": 99}}
	r := resolveGame("onepiece", cards, nil, exps, bps, nil, manual, nil, catalog, shelf, "2026-10-08")
	if _, ok := r.Mapped["OP05-119_p1"]; ok {
		t.Errorf("a product no blueprint lists was mapped: %+v", r.Mapped["OP05-119_p1"])
	}
	if why := r.Dropped["OP05-119_p1"]; why == "" {
		t.Errorf("the stray pick is not listed as dropped: %v", r.Dropped)
	}
}

func TestResolveGameManualNone(t *testing.T) {
	cards, exps, bps, catalog, shelf := manualFixture()
	// "None of the candidates" is a verdict too: the card is neither
	// auto-picked nor listed as ambiguous again.
	manual := manualPicks{None: map[string]bool{"OP05-119": true, "OP05-119_p2": true}}
	r := resolveGame("onepiece", cards, nil, exps, bps, nil, manual, nil, catalog, shelf, "2026-10-08")
	if _, ok := r.Mapped["OP05-119"]; ok {
		t.Errorf("OP05-119 was mapped despite the none verdict: %+v", r.Mapped["OP05-119"])
	}
	if len(r.Ambiguous) != 1 || r.Ambiguous[0].Card != "OP05-119_p1" {
		t.Errorf("ambiguous = %v, want OP05-119_p1 alone", r.Ambiguous)
	}
	if !r.ManualNone["OP05-119"] || !r.ManualNone["OP05-119_p2"] || len(r.Unresolved) != 2 {
		t.Errorf("none verdicts = %v, unresolved %v", r.ManualNone, r.Unresolved)
	}
}

func TestParseManual(t *testing.T) {
	m, err := parseManual([]byte(`{"picks": {"OP05-119_p1": 4, "_OP05-119_p1": "checked 2026-10-08"}, "none": ["OP05-119_p2"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Picks["OP05-119_p1"] != 4 || len(m.Picks) != 1 || !m.None["OP05-119_p2"] {
		t.Errorf("parsed %+v", m)
	}
	if _, err := parseManual([]byte(`{"picks": {"OP05-119_p1": 0}}`)); err == nil {
		t.Error("a zero product id was accepted")
	}
	if m, err := parseManual(nil); err != nil || len(m.Picks) != 0 || len(m.None) != 0 {
		t.Errorf("empty input: %+v, %v", m, err)
	}
}

func TestExpansionLabel(t *testing.T) {
	cases := []struct {
		exp  ctExpansion
		want string
	}{
		{ctExpansion{Code: "st04", Name: "ST-05: Starter Deck Animal Kingdom"}, "ST-05"}, // the code is off by one: the name rules
		{ctExpansion{Code: "prb02", Name: "PRB-02: Premium Booster vol.2"}, "PRB-02"},
		{ctExpansion{Code: "op05", Name: "OP-05: Awakening of the New Era"}, "OP-05"},
		{ctExpansion{Code: "eb-01", Name: "EB-01: Memorial Collection"}, "EB-01"},
		{ctExpansion{Code: "st-29", Name: "ST-29: Starter Deck Egghead"}, "ST-29"},
		{ctExpansion{Code: "op13", Name: "Legacy of the Master"}, "OP-13"}, // no label in the name: the code
		{ctExpansion{Code: "promo", Name: "One Piece Promos"}, "PROMO"},
		{ctExpansion{Code: "op-pcc", Name: "Premium Card Collection"}, ""},
		{ctExpansion{Code: "op05p1st", Name: "OP-05 1st Anniversary"}, ""}, // a stamped run, not the set
	}
	for _, c := range cases {
		if got := expansionLabel(c.exp); got != c.want {
			t.Errorf("expansionLabel(%q, %q) = %q, want %q", c.exp.Code, c.exp.Name, got, c.want)
		}
	}
}

func TestPackLabels(t *testing.T) {
	cases := map[string][]string{
		"PRB-02": {"PRB-02"}, "ST-34": {"ST-34"}, "OP-01": {"OP-01"}, "EB-01": {"EB-01"},
		"OP14-EB04": {"OP-14", "EB-04"}, "PROMO": {"PROMO"}, "": nil, "OP1": {"OP-01"},
	}
	for pack, want := range cases {
		got := packLabels(pack)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("packLabels(%q) = %v, want %v", pack, got, want)
		}
	}
}

func TestIsVariantPrinting(t *testing.T) {
	bp := func(number, version string) ctBlueprint {
		b := ctBlueprint{Version: version}
		b.FixedProperties.CollectorNumber = number
		return b
	}
	cases := []struct {
		b    ctBlueprint
		want bool
	}{
		{bp("EB01-009", ""), false},
		{bp("EB01-009", "Non-Foil | Reprint"), false},
		{bp("EB01-009", "Reprint | missing pen symbol next to the artist name"), false},
		{bp("EB01-009", "Secret Rare | Reprint"), false},
		{bp("EB01-009", "Special Rare"), true},
		{bp("EB01-009a", "Alternate Art"), true},
		{bp("EB01-009f", "Pirate Foil"), true},
		{bp("OP05-119m", "Manga Panel Alternate Art | Reprint"), true},
		{bp("EB01-009", "Jolly Roger Foil"), true},
		{bp("ST01-001", "Super Pre-Release Stamp"), true},
	}
	for _, c := range cases {
		if got := isVariantPrinting(c.b); got != c.want {
			t.Errorf("isVariantPrinting(%q, %q) = %v, want %v", c.b.FixedProperties.CollectorNumber, c.b.Version, got, c.want)
		}
	}
}

// EB01-009 on CardTrader: the plain card and an alternate art in EB-01
// (products 1/2 and 3/4), the reprint and a foil version in PRB-02 (11/12
// and 13/14). Odd products are on the English shelf.
func packFixture() (map[string]ourCard, map[int]ctExpansion, []ctBlueprint, map[int]cmProduct, map[int]shelfLang) {
	cards := map[string]ourCard{
		"EB01-009":    {Name: "Just Shut Up and Come with Us!!!!", Set: "EB01", Number: "009", Pack: "EB-01"},
		"EB01-009_p1": {Name: "Just Shut Up and Come with Us!!!!", Set: "EB01", Number: "009", Pack: "EB-01"},
		"EB01-009_r1": {Name: "Just Shut Up and Come with Us!!!!", Set: "EB01", Number: "009", Pack: "PRB-02"},
		"EB01-009_p2": {Name: "Just Shut Up and Come with Us!!!!", Set: "EB01", Number: "009", Pack: "PRB-02"},
	}
	exps := map[int]ctExpansion{
		100: {ID: 100, GameID: 15, Code: "eb-01", Name: "EB-01: Memorial Collection"},
		200: {ID: 200, GameID: 15, Code: "prb02", Name: "PRB-02: Premium Booster vol.2"},
	}
	bp := func(id, exp int, number, version string, cm ...int) ctBlueprint {
		b := ctBlueprint{ID: id, Version: version, ExpansionID: exp, CardMarketIDs: cm}
		b.FixedProperties.CollectorNumber = number
		return b
	}
	bps := []ctBlueprint{
		bp(1, 100, "EB01-009", "", 1, 2),
		bp(2, 100, "EB01-009a", "Alternate Art", 3, 4),
		bp(3, 200, "EB01-009", "Non-Foil | Reprint", 11, 12),
		bp(4, 200, "EB01-009f", "Pirate Foil", 13, 14),
	}
	catalog := map[int]cmProduct{}
	for _, p := range []int{1, 2, 3, 4, 11, 12, 13, 14} {
		exp := 10
		if p%2 == 0 {
			exp = 11
		}
		catalog[p] = cmProduct{IDProduct: p, Name: "Just Shut Up and Come with Us!!!! (EB01-009)", IDExpansion: exp}
	}
	shelf := map[int]shelfLang{10: shelfEnglish, 11: shelfForeign}
	shelfEnglishSealed = map[int]int{10: 15}
	return cards, exps, bps, catalog, shelf
}

func TestResolveGamePackRuleSettlesReprintsAndParallels(t *testing.T) {
	cards, exps, bps, catalog, shelf := packFixture()
	r := resolveGame("onepiece", cards, nil, exps, bps, nil, manualPicks{}, nil, catalog, shelf, "2026-10-08")
	want := map[string]resolved{
		"EB01-009":    {1, srcBlueprint},
		"EB01-009_p1": {3, srcPack},  // the one versioned print of EB-01, our one _p there
		"EB01-009_r1": {11, srcPack}, // the one plain print of PRB-02
		"EB01-009_p2": {13, srcPack}, // the one versioned print of PRB-02
	}
	for id, w := range want {
		if got := r.Mapped[id]; got != w {
			t.Errorf("%s = %+v, want %+v (dropped %v, ambiguous %v)", id, got, w, r.Dropped, r.Ambiguous)
		}
	}
	if len(r.Ambiguous) != 0 {
		t.Errorf("ambiguous = %v, want none", r.Ambiguous)
	}
}

func TestResolveGamePackRuleNarrowsOrFallsBack(t *testing.T) {
	cards, exps, bps, catalog, shelf := packFixture()
	// A second versioned print in PRB-02 for our one _p2: two candidates,
	// both from the pack, nothing from EB-01.
	b := ctBlueprint{ID: 5, Version: "Manga Panel Alternate Art | Reprint", ExpansionID: 200, CardMarketIDs: []int{15, 16}}
	b.FixedProperties.CollectorNumber = "EB01-009m"
	bps = append(bps, b)
	catalog[15] = cmProduct{IDProduct: 15, Name: "Just Shut Up and Come with Us!!!! (EB01-009)", IDExpansion: 10}
	catalog[16] = cmProduct{IDProduct: 16, Name: "Just Shut Up and Come with Us!!!! (EB01-009)", IDExpansion: 11}
	// A variant with no pack keeps today's candidates: every print anywhere.
	cards["EB01-009_p3"] = ourCard{Name: "Just Shut Up and Come with Us!!!!", Set: "EB01", Number: "009"}
	r := resolveGame("onepiece", cards, nil, exps, bps, nil, manualPicks{}, nil, catalog, shelf, "2026-10-08")
	if got := r.Mapped["EB01-009_r1"]; got != (resolved{11, srcPack}) {
		t.Errorf("EB01-009_r1 = %+v, want 11 from pack", got)
	}
	amb := map[string][]int{}
	for _, a := range r.Ambiguous {
		amb[a.Card] = a.Candidates
	}
	if fmt.Sprint(amb["EB01-009_p2"]) != "[13 14 15 16]" {
		t.Errorf("EB01-009_p2 candidates = %v, want the pack's two versioned prints", amb["EB01-009_p2"])
	}
	if fmt.Sprint(amb["EB01-009_p3"]) != "[1 2 3 4 11 12 13 14 15 16]" {
		t.Errorf("EB01-009_p3 candidates = %v, want every print", amb["EB01-009_p3"])
	}
}

func TestResolveGameManualPickDisagreeingWithPackRuleIsListed(t *testing.T) {
	cards, exps, bps, catalog, shelf := packFixture()
	// The hand pick names the foil by its foreign product; its English shelf ships.
	manual := manualPicks{Picks: map[string]int{"EB01-009_r1": 14}}
	r := resolveGame("onepiece", cards, nil, exps, bps, nil, manual, nil, catalog, shelf, "2026-10-08")
	if got := r.Mapped["EB01-009_r1"]; got != (resolved{13, srcManual}) {
		t.Errorf("EB01-009_r1 = %+v, want the manual pick to win", got)
	}
	if r.PackDisagree["EB01-009_r1"] != 11 {
		t.Errorf("disagreements = %v, want EB01-009_r1 -> 11", r.PackDisagree)
	}
}
