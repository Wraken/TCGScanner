package main

import "testing"

func TestPackLabel(t *testing.T) {
	labelled := func(label, prefix string) pack {
		p := pack{ID: "1", RawTitle: "x"}
		p.TitleParts = &struct {
			Title  string `json:"title"`
			Label  string `json:"label"`
			Prefix string `json:"prefix"`
		}{Title: "t", Label: label, Prefix: prefix}
		return p
	}
	cases := []struct {
		name string
		p    pack
		want string
	}{
		{"premium booster", labelled("PRB-02", "PREMIUM BOOSTER"), "PRB-02"},
		{"starter deck", labelled("ST-34", "STARTER DECK"), "ST-34"},
		{"booster", labelled("OP-01", "BOOSTER PACK"), "OP-01"},
		{"combined booster keeps its raw label", labelled("OP14-EB04", "BOOSTER PACK"), "OP14-EB04"},
		{"promo bucket", pack{ID: "569901", RawTitle: "Promotion card"}, "PROMO"},
		{"other products have no pack", pack{ID: "569902", RawTitle: "Other Product Card"}, ""},
		{"unknown pack", pack{}, ""},
	}
	for _, c := range cases {
		if got := packLabel(c.p); got != c.want {
			t.Errorf("%s: packLabel = %q, want %q", c.name, got, c.want)
		}
	}
}
