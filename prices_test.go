package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testMapping is a bundle mapping with one card per game and a One Piece
// card on two shelves. Product ids are made up but unique across games.
const testMapping = `{
 "version": 1, "built_at": "2026-10-07",
 "games": {"pokemon": 6, "onepiece": 18},
 "cards": {"sv01-020": 1001, "OP05-119": 2001, "OP01-001": 2003, "unpriced-1": 1009},
 "cards_non_english": {"OP05-119": 2002, "P-041": 2004}
}`

func testGuide(createdAt string, rows ...priceRow) string {
	data, _ := json.Marshal(priceGuideFile{CreatedAt: createdAt, PriceGuides: rows})
	return string(data)
}

// pricesApp is an App whose mapping and guides live under a temp dir.
func pricesApp(t *testing.T, mapping string) (*App, string) {
	t.Helper()
	root := t.TempDir()
	meta := filepath.Join(root, "models", "tcg", "metadata")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	if mapping != "" {
		if err := os.WriteFile(filepath.Join(meta, priceMappingFile), []byte(mapping), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{
		db:                 testDB(t),
		pricesDir:          filepath.Join(root, "prices"),
		priceMappingDirsFn: func() []string { return []string{meta} },
	}
	return app, root
}

func writeGuide(t *testing.T, app *App, game, body string) {
	t.Helper()
	if err := writeFileAtomic(app.priceGuidePath(game), []byte(body)); err != nil {
		t.Fatal(err)
	}
}

func TestCopyValue(t *testing.T) {
	both := CardPrice{Trend: 8.33, TrendFoil: 35.36, TrendNonEnglish: 4.56, TrendNonEnglishFoil: 20}
	englishOnly := CardPrice{Trend: 0.08, TrendFoil: 0.54}
	foilOnly := CardPrice{TrendFoil: 0.83}
	nonEnglishOnly := CardPrice{TrendNonEnglish: 1.5}
	cases := []struct {
		name string
		copy CollectionCard
		p    CardPrice
		want float64
		ok   bool
	}{
		{"en normal", CollectionCard{Lang: "en"}, both, 8.33, true},
		{"en foil", CollectionCard{Lang: "en", Foil: true}, both, 35.36, true},
		{"ja normal: non-English shelf", CollectionCard{Lang: "ja"}, both, 4.56, true},
		{"ja foil", CollectionCard{Lang: "ja", Foil: true}, both, 20, true},
		{"fr copy of a one-shelf card: the only product", CollectionCard{Lang: "fr"}, englishOnly, 0.08, true},
		{"fr foil of a one-shelf card", CollectionCard{Lang: "fr", Foil: true}, englishOnly, 0.54, true},
		{"normal copy of a foil-only product: unpriced", CollectionCard{Lang: "en"}, foilOnly, 0, false},
		{"foil copy of a foil-only product", CollectionCard{Lang: "en", Foil: true}, foilOnly, 0.83, true},
		{"foil copy where the product has no foil column: the normal price", CollectionCard{Lang: "en", Foil: true}, CardPrice{Trend: 2}, 2, true},
		{"en copy of a non-English-only card: unpriced", CollectionCard{Lang: "en"}, nonEnglishOnly, 0, false},
		{"ja copy of a non-English-only card", CollectionCard{Lang: "ja"}, nonEnglishOnly, 1.5, true},
		{"no price", CollectionCard{Lang: "en"}, CardPrice{}, 0, false},
	}
	for _, c := range cases {
		got, ok := copyValue(c.copy, c.p)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: copyValue = %v, %v; want %v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestGetCardPrices(t *testing.T) {
	app, _ := pricesApp(t, testMapping)
	writeGuide(t, app, "pokemon", testGuide("2026-10-07T02:00:00+0200",
		priceRow{IDProduct: 1001, Trend: 0.08, TrendFoil: 0.54},
		priceRow{IDProduct: 1009}, // mapped, but Cardmarket computed no trend
	))
	writeGuide(t, app, "onepiece", testGuide("2026-10-06T02:00:00+0200",
		priceRow{IDProduct: 2001, Trend: 8.33, TrendFoil: 35.36},
		priceRow{IDProduct: 2002, Trend: 4.56},
		priceRow{IDProduct: 2004, Trend: 1.5},
	))

	got, err := app.GetCardPrices([]string{"sv01-020", "OP05-119", "OP01-001", "unpriced-1", "P-041", "nope"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]CardPrice{
		"sv01-020": {Trend: 0.08, TrendFoil: 0.54, AsOf: "2026-10-07"},
		"OP05-119": {Trend: 8.33, TrendFoil: 35.36, TrendNonEnglish: 4.56, AsOf: "2026-10-06"},
		"P-041":    {TrendNonEnglish: 1.5, AsOf: "2026-10-06"},
	}
	if len(got) != len(want) {
		t.Errorf("got %d prices %v, want %d", len(got), got, len(want))
	}
	for id, p := range want {
		if got[id] != p {
			t.Errorf("%s = %+v, want %+v", id, got[id], p)
		}
	}

	// A bundle without a mapping: no prices, no error.
	bare, _ := pricesApp(t, "")
	if got, err := bare.GetCardPrices([]string{"sv01-020"}); err != nil || len(got) != 0 {
		t.Errorf("no mapping: %v, %v; want empty, nil", got, err)
	}
	st, _ := bare.GetPricesStatus()
	if st.Available || !st.Enabled {
		t.Errorf("no mapping: status = %+v, want unavailable, enabled", st)
	}
}

func TestGetGameValues(t *testing.T) {
	app, _ := pricesApp(t, testMapping)
	writeGuide(t, app, "pokemon", testGuide("2026-10-07",
		priceRow{IDProduct: 1001, Trend: 0.08, TrendFoil: 0.54}))
	writeGuide(t, app, "onepiece", testGuide("2026-10-07",
		priceRow{IDProduct: 2001, Trend: 8.33, TrendFoil: 35.36},
		priceRow{IDProduct: 2002, Trend: 4.56}))
	for _, c := range []struct {
		id, game, lang string
		foil           bool
	}{
		{"sv01-020", "pokemon", "en", false},
		{"sv01-020", "pokemon", "en", true},
		{"sv01-020", "pokemon", "fr", true}, // one product for every language
		{"OP05-119", "onepiece", "en", false},
		{"OP05-119", "onepiece", "ja", false},
		{"OP05-119", "onepiece", "ja", true},  // no foil price on the non-English shelf: its normal price
		{"OP01-001", "onepiece", "en", false}, // mapped, no row in the guide
		{"zz99-999", "", "en", false},         // unknown game, unmapped
	} {
		if _, err := insertCard(app.db, 0, c.id, c.game, "tcg", c.foil, c.lang); err != nil {
			t.Fatal(err)
		}
	}
	got, err := app.GetGameValues()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]GameValue{
		"pokemon":  {Value: 0.08 + 0.54 + 0.54, Priced: 3, Copies: 3},
		"onepiece": {Value: 8.33 + 4.56 + 4.56, Priced: 3, Copies: 4},
		"":         {Copies: 1},
	}
	for game, w := range want {
		g := got[game]
		if g.Priced != w.Priced || g.Copies != w.Copies || abs(g.Value-w.Value) > 1e-9 {
			t.Errorf("%q = %+v, want %+v", game, g, w)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestRefreshPrices(t *testing.T) {
	app, _ := pricesApp(t, `{"version":1,"built_at":"2026-10-07","games":{"pokemon":6},"cards":{"sv01-020":1001}}`)

	var requests atomic.Int32
	var lastIfNoneMatch atomic.Value
	lastIfNoneMatch.Store("")
	status := atomic.Int32{}
	status.Store(http.StatusOK)
	body := testGuide("2026-10-07", priceRow{IDProduct: 1001, Trend: 0.08})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		lastIfNoneMatch.Store(r.Header.Get("If-None-Match"))
		if !strings.HasSuffix(r.URL.Path, "/price_guide_6.json") {
			http.NotFound(w, r)
			return
		}
		switch status.Load() {
		case http.StatusNotModified:
			w.WriteHeader(http.StatusNotModified)
		case http.StatusInternalServerError:
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			w.Header().Set("ETag", `"v1"`)
			w.Write([]byte(body))
		}
	}))
	defer srv.Close()
	app.priceGuideURLFn = func(id int) string { return srv.URL + "/price_guide_" + itoa(id) + ".json" }

	// 1. First refresh: a full download, the file lands, the ETag is kept.
	if err := app.refreshPrices(false); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	if got, _ := app.GetCardPrices([]string{"sv01-020"}); got["sv01-020"].Trend != 0.08 {
		t.Errorf("after refresh: %+v", got)
	}
	st := app.loadPricesState()
	if st.Games["pokemon"].ETag != `"v1"` || st.Games["pokemon"].FetchedAt == "" || st.Games["pokemon"].Error != "" {
		t.Errorf("state after refresh = %+v", st.Games["pokemon"])
	}

	// 2. Not forced, fetched a moment ago: nothing is asked.
	if err := app.refreshPrices(false); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Errorf("fresh guide refetched: requests = %d", requests.Load())
	}

	// 3. Forced: the ETag goes out, a 304 comes back, the file stays.
	status.Store(http.StatusNotModified)
	before := st.Games["pokemon"].FetchedAt
	time.Sleep(1100 * time.Millisecond) // RFC 3339 has second resolution
	if err := app.refreshPrices(true); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || lastIfNoneMatch.Load().(string) != `"v1"` {
		t.Errorf("forced refresh: requests = %d, If-None-Match = %q", requests.Load(), lastIfNoneMatch.Load())
	}
	st = app.loadPricesState()
	if st.Games["pokemon"].FetchedAt == before {
		t.Error("304 did not bump fetched_at")
	}
	if got, _ := app.GetCardPrices([]string{"sv01-020"}); got["sv01-020"].Trend != 0.08 {
		t.Errorf("after 304: %+v", got)
	}

	// 4. The server fails: the error is recorded, the old guide is kept.
	status.Store(http.StatusInternalServerError)
	if err := app.refreshPrices(true); err == nil {
		t.Error("server error not reported")
	}
	st = app.loadPricesState()
	if st.Games["pokemon"].Error == "" || st.Games["pokemon"].ETag != `"v1"` {
		t.Errorf("state after failure = %+v", st.Games["pokemon"])
	}
	if got, _ := app.GetCardPrices([]string{"sv01-020"}); got["sv01-020"].Trend != 0.08 {
		t.Errorf("failed refresh lost the guide: %+v", got)
	}
	pst, _ := app.GetPricesStatus()
	if pst.Games["pokemon"].Error == "" || pst.Games["pokemon"].AsOf != "2026-10-07" || pst.Stale {
		t.Errorf("status after failure = %+v", pst)
	}

	// 5. A broken body never replaces the good file.
	status.Store(http.StatusOK)
	body = "<html>maintenance</html>"
	if err := app.refreshPrices(true); err == nil {
		t.Error("broken guide accepted")
	}
	if got, _ := app.GetCardPrices([]string{"sv01-020"}); got["sv01-020"].Trend != 0.08 {
		t.Errorf("broken guide replaced the good one: %+v", got)
	}

	// 6. Turned off: no request at all, even forced.
	n := requests.Load()
	if err := app.SetPricesEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := app.refreshPrices(true); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != n {
		t.Errorf("disabled prices still fetched: %d requests", requests.Load()-n)
	}
	pst, _ = app.GetPricesStatus()
	if pst.Enabled {
		t.Error("status says enabled after SetPricesEnabled(false)")
	}
}

func TestPricesStale(t *testing.T) {
	app, _ := pricesApp(t, `{"version":1,"games":{"pokemon":6},"cards":{}}`)
	st, _ := app.GetPricesStatus()
	if !st.Stale {
		t.Error("no guide on disk: not stale")
	}
	writeGuide(t, app, "pokemon", testGuide("2026-10-01"))
	old := time.Now().Add(-4 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if err := app.savePricesState(pricesState{Games: map[string]gamePriceState{"pokemon": {FetchedAt: old}}}); err != nil {
		t.Fatal(err)
	}
	if st, _ = app.GetPricesStatus(); !st.Stale || st.Games["pokemon"].AsOf != "2026-10-01" {
		t.Errorf("4-day-old guide: %+v", st)
	}
	recent := time.Now().UTC().Format(time.RFC3339)
	if err := app.savePricesState(pricesState{Games: map[string]gamePriceState{"pokemon": {FetchedAt: recent}}}); err != nil {
		t.Fatal(err)
	}
	if st, _ = app.GetPricesStatus(); st.Stale {
		t.Errorf("fresh guide reported stale: %+v", st)
	}
}

func TestSettings(t *testing.T) {
	db := testDB(t)
	if _, ok, err := getSetting(db, "x"); ok || err != nil {
		t.Errorf("missing key: ok = %v, err = %v", ok, err)
	}
	if err := setSetting(db, "x", "1"); err != nil {
		t.Fatal(err)
	}
	if err := setSetting(db, "x", "2"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := getSetting(db, "x"); !ok || v != "2" {
		t.Errorf("after two sets: %q, %v", v, ok)
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

// testMappingWithCandidates adds an ambiguous One Piece card with two printings.
const testMappingWithCandidates = `{
 "version": 1, "built_at": "2026-10-08",
 "games": {"onepiece": 18},
 "cards": {"OP05-119": 2001},
 "cards_non_english": {"OP05-119": 2002},
 "candidates": {"OP01-006_p4": [
   {"product": 3001, "non_english": 3002, "label": "Full Art (OP01-006f) · PRB-01: The Best", "image": "https://cardtrader.com/uploads/blueprints/image/1/f.jpg"},
   {"product": 3003, "non_english": 0, "label": "Jolly Roger Foil (OP01-006j) · PRB-01: The Best", "image": ""}
 ]}
}`

func TestCardPriceReferenceOutranksTheBundle(t *testing.T) {
	app, _ := pricesApp(t, testMappingWithCandidates)
	writeGuide(t, app, "onepiece", testGuide("2026-10-08T02:00:00+0200",
		priceRow{IDProduct: 2001, Trend: 8.33}, priceRow{IDProduct: 2005, Trend: 1.11},
		priceRow{IDProduct: 3001, Trend: 3.2, TrendFoil: 3.5}, priceRow{IDProduct: 3002, Trend: 2.0},
		priceRow{IDProduct: 3003, Trend: 15.2},
	))
	got, err := app.GetCardPrices([]string{"OP05-119", "OP01-006_p4"})
	if err != nil {
		t.Fatal(err)
	}
	// No reference yet: the ambiguous id is reported with its candidate count and no price.
	if p := got["OP01-006_p4"]; p.Trend != 0 || p.Candidates != 2 || p.Ref {
		t.Errorf("unset reference: %+v", p)
	}
	if err := setPriceRef(app.db, PriceRef{CardID: "OP01-006_p4", Product: 3001, NonEnglish: 3002, Label: "Full Art"}); err != nil {
		t.Fatal(err)
	}
	got, _ = app.GetCardPrices([]string{"OP05-119", "OP01-006_p4"})
	want := CardPrice{Trend: 3.2, TrendFoil: 3.5, TrendNonEnglish: 2.0, AsOf: "2026-10-08", Ref: true, Candidates: 2}
	if got["OP01-006_p4"] != want {
		t.Errorf("with a reference: %+v, want %+v", got["OP01-006_p4"], want)
	}
	// A reference outranks the bundle for a mapped id too.
	if err := setPriceRef(app.db, PriceRef{CardID: "OP05-119", Product: 2005}); err != nil {
		t.Fatal(err)
	}
	got, _ = app.GetCardPrices([]string{"OP05-119"})
	if p := got["OP05-119"]; p.Trend != 1.11 || !p.Ref || p.TrendNonEnglish != 0 {
		t.Errorf("reference over bundle: %+v", p)
	}
	// A reference to a product the day's guide lacks is unpriced, not wrong.
	if err := setPriceRef(app.db, PriceRef{CardID: "OP01-006_p4", Product: 4444}); err != nil {
		t.Fatal(err)
	}
	got, _ = app.GetCardPrices([]string{"OP01-006_p4"})
	if p := got["OP01-006_p4"]; p.Trend != 0 || !p.Ref || p.Candidates != 2 {
		t.Errorf("stale reference: %+v", p)
	}
}

func TestParsePriceMappingWithoutCandidates(t *testing.T) {
	m, err := parsePriceMapping([]byte(testMapping))
	if err != nil || m.Candidates == nil || len(m.Candidates) != 0 {
		t.Errorf("an older file must read as no candidates: %v, %v", m.Candidates, err)
	}
}

func TestPriceReferenceBindings(t *testing.T) {
	app, _ := pricesApp(t, testMappingWithCandidates)
	writeGuide(t, app, "onepiece", testGuide("2026-10-08T02:00:00+0200",
		priceRow{IDProduct: 3001, Trend: 3.2, TrendFoil: 3.5}, priceRow{IDProduct: 3002, Trend: 2.0}, priceRow{IDProduct: 3003, Trend: 15.2},
	))
	views, err := app.GetPriceCandidates("OP01-006_p4")
	if err != nil || len(views) != 2 {
		t.Fatalf("candidates: %v, %v", views, err)
	}
	if views[0].Label != "Full Art (OP01-006f) · PRB-01: The Best" || views[0].Price.Trend != 3.2 || views[0].Price.TrendNonEnglish != 2.0 || views[0].Chosen {
		t.Errorf("first view: %+v", views[0])
	}
	if views[1].Price.Trend != 15.2 || views[1].Image != "" {
		t.Errorf("second view: %+v", views[1])
	}
	if err := app.SetPriceRef("OP01-006_p4", 4444); err == nil {
		t.Error("a product outside the candidates was accepted")
	}
	if err := app.SetPriceRef("OP01-006_p4", 3003); err != nil {
		t.Fatal(err)
	}
	refs, _ := app.GetPriceRefs()
	if r := refs["OP01-006_p4"]; r.Product != 3003 || r.Label != "Jolly Roger Foil (OP01-006j) · PRB-01: The Best" || r.NonEnglish != 0 {
		t.Errorf("stored reference: %+v", r)
	}
	views, _ = app.GetPriceCandidates("OP01-006_p4")
	if views[0].Chosen || !views[1].Chosen {
		t.Errorf("chosen marks: %v %v", views[0].Chosen, views[1].Chosen)
	}
	if err := app.ClearPriceRef("OP01-006_p4"); err != nil {
		t.Fatal(err)
	}
	if refs, _ := app.GetPriceRefs(); len(refs) != 0 {
		t.Errorf("after clear: %v", refs)
	}
	if views, err := app.GetPriceCandidates("OP05-119"); err != nil || len(views) != 0 {
		t.Errorf("a settled id has no candidates: %v, %v", views, err)
	}
}

func TestGetPriceCandidateImage(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent")
		}
		if strings.HasSuffix(r.URL.Path, "missing.jpg") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte(strings.Repeat("J", 2000)))
	}))
	defer srv.Close()
	app, _ := pricesApp(t, testMappingWithCandidates)
	app.priceImageAllowFn = func(u string) bool { return strings.HasPrefix(u, srv.URL) }

	got, err := app.GetPriceCandidateImage(srv.URL + "/uploads/blueprints/image/1/f.jpg")
	if err != nil || !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Fatalf("first fetch: %q, %v", got[:min(len(got), 40)], err)
	}
	again, _ := app.GetPriceCandidateImage(srv.URL + "/uploads/blueprints/image/1/f.jpg")
	if again != got || atomic.LoadInt32(&hits) != 1 {
		t.Errorf("second call must come from the cache (hits %d)", hits)
	}
	if got, err := app.GetPriceCandidateImage(srv.URL + "/uploads/blueprints/image/1/missing.jpg"); got != "" || err != nil {
		t.Errorf("a missing image is empty and no error: %q, %v", got, err)
	}
	if _, err := app.GetPriceCandidateImage("https://example.com/x.jpg"); err == nil {
		t.Error("a URL outside CardTrader was accepted")
	}
	if _, err := os.Stat(filepath.Join(app.pricesDirPath(), "images")); err != nil {
		t.Errorf("cache dir: %v", err)
	}
}
