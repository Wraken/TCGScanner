package main

import "testing"

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
