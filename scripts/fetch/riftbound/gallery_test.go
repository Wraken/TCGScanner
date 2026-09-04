package main

import (
	"reflect"
	"strings"
	"testing"
)

const galleryFixture = `<html><head></head><body>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"page":{"blades":[
 {"_type":"hero","title":"Card Gallery"},
 {"_type":"cardGallery","cards":{"items":[
  {"id":"unl-131-219","name":"Abandon","publicCode":"UNL-131/219",
   "set":{"label":"Card Set","value":{"id":"UNL","label":"Unleashed"}},
   "cardType":{"label":"Card Type","type":[{"id":"spell","label":"Spell"}]},
   "rarity":{"label":"Rarity","value":{"id":"uncommon","label":"Uncommon"}},
   "domain":{"label":"Domain","values":[{"id":"chaos","label":"Chaos"}]},
   "cardImage":{"type":"image","url":"https://cdn.example/abandon-744x1039.png?accountingTag=RB"}},
  {"id":"ogs-001-024","name":"Ahri, Nine’Tailed","publicCode":"OGS-001/024",
   "set":{"label":"Card Set","value":{"id":"OGS","label":"Proving Grounds"}},
   "cardType":{"label":"Card Type","type":[{"id":"legend","label":"Legend"},{"id":"unit","label":"Unit"}]},
   "rarity":{"label":"Rarity","value":{"id":"rare","label":"Rare"}},
   "domain":{"label":"Domain","values":[{"id":"calm","label":"Calm"},{"id":"mind","label":"Mind"}]},
   "cardImage":{"type":"image","url":"https://cdn.example/ahri.png"}},
  {"id":"unl-t04","name":"Buff","publicCode":"UNL-T04",
   "set":{"label":"Card Set","value":{"id":"UNL","label":"Unleashed"}},
   "cardType":{"label":"Card Type","type":[]},
   "rarity":{"label":"Rarity","value":{"id":"common","label":"Common"}},
   "domain":{"label":"Domain"},
   "cardImage":{"type":"image","url":"https://cdn.example/buff.png"}}
 ]}}
]}}}}</script>
</body></html>`

func TestExtractGallery(t *testing.T) {
	cards, err := extractGallery([]byte(galleryFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := []Card{
		{ID: "ogs-001-024", PublicCode: "OGS-001/024", ImageURL: "https://cdn.example/ahri.png",
			Name: "Ahri, Nine’Tailed", SetName: "Proving Grounds", Rarity: "Rare",
			Domains: []string{"Calm", "Mind"}, Type: "Legend / Unit"},
		{ID: "unl-131-219", PublicCode: "UNL-131/219", ImageURL: "https://cdn.example/abandon-744x1039.png?accountingTag=RB",
			Name: "Abandon", SetName: "Unleashed", Rarity: "Uncommon",
			Domains: []string{"Chaos"}, Type: "Spell"},
		{ID: "unl-t04", PublicCode: "UNL-T04", ImageURL: "https://cdn.example/buff.png",
			Name: "Buff", SetName: "Unleashed", Rarity: "Common"},
	}
	if !reflect.DeepEqual(cards, want) {
		t.Errorf("extractGallery mismatch\n got: %+v\nwant: %+v", cards, want)
	}
}

func TestExtractGalleryBareJSON(t *testing.T) {
	// DevTools fallback: the __NEXT_DATA__ content saved without the page.
	start := strings.Index(galleryFixture, `{"props"`)
	end := strings.Index(galleryFixture, "</script>")
	cards, err := extractGallery([]byte("\n" + galleryFixture[start:end]))
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 3 || cards[0].ID != "ogs-001-024" {
		t.Errorf("bare JSON: %+v", cards)
	}
}

func TestExtractGalleryErrors(t *testing.T) {
	cases := map[string]string{
		"no next data": `<html><body>Just a moment...</body></html>`,
		"no cards":     `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"page":{"blades":[{"_type":"hero"}]}}}}</script>`,
		"duplicate id": strings.Replace(galleryFixture, `"id":"unl-131-219"`, `"id":"unl-t04"`, 1),
	}
	for name, html := range cases {
		if _, err := extractGallery([]byte(html)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestMetaOf(t *testing.T) {
	// The gallery label wins over the built-in set table; the table is the
	// fallback for entries extracted before setName existed.
	got := metaOf(Card{ID: "ogs-001-024", PublicCode: "OGS-001/024", Name: "Ahri, Nine’Tailed",
		SetName: "Proving Grounds", Rarity: "Rare", Domains: []string{"Calm", "Mind"}, Type: "Legend"})
	want := meta{Name: "Ahri, Nine'Tailed", Set: "OGS", SetName: "Proving Grounds", Number: "001",
		Rarity: "Rare", Type: "Legend", Colors: []string{"Calm", "Mind"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("metaOf\n got: %+v\nwant: %+v", got, want)
	}
	got = metaOf(Card{ID: "ogn-299-star-298", PublicCode: "OGN-299*/298", Name: "X"})
	if got.SetName != "Origins" || got.Number != "299*" || len(got.Colors) != 0 || got.Colors == nil {
		t.Errorf("fallback metaOf: %+v", got)
	}
}
