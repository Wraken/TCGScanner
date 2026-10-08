package main

import (
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Cardmarket prices (docs/superpowers/specs/2026-10-07-cardmarket-prices-design.md).
//
// Two inputs. The mapping, our card id -> Cardmarket product, ships in the
// model bundle as models/<model>/metadata/cardmarket.json and is built once
// by scripts/fetch/cardmarket; users need no account. The prices are
// Cardmarket's public daily guides, one JSON per game, fetched into
// ./prices/ next to collection.db, once a day, and kept across restarts so
// the app stays usable offline with the last ones. A refresh can be turned
// off (settings table, key prices_enabled); the GET is then the only thing
// the app ever sends to the network.
//
// A price is one product's "trend" column (and "trend-foil" for foil
// copies). One Piece cards carry a second product, the same printing on
// Cardmarket's non-English shelf, read for copies whose language is not
// English; every other game has one product for every language.

const (
	defaultPricesDir     = "./prices"
	priceGuideURLPattern = "https://downloads.s3.cardmarket.com/productCatalog/priceGuide/price_guide_%d.json"
	priceMappingFile     = "cardmarket.json"
	pricesStateFile      = "state.json"
	pricesEnabledKey     = "prices_enabled"
	// A guide fetched less than this long ago is not fetched again on start.
	priceRefreshAge = 24 * time.Hour
	// Past this age the UI says so ("Prices from 3 Oct") instead of showing
	// the prices as current.
	priceStaleAge = 3 * 24 * time.Hour
	// The frontend listens for this to reload prices after a background
	// refresh, so they appear without a restart.
	pricesUpdatedEvent = "prices:updated"
)

// priceMapping is scripts/fetch/cardmarket/cardmarket.json.
type priceMapping struct {
	Version         int                         `json:"version"`
	BuiltAt         string                      `json:"built_at"`
	Games           map[string]int              `json:"games"` // our game name -> Cardmarket game id
	Cards           map[string]int              `json:"cards"`
	CardsNonEnglish map[string]int              `json:"cards_non_english"`
	Candidates      map[string][]PriceCandidate `json:"candidates"`
}

// PriceCandidate is one selectable printing of a card the mapper could not
// settle: the product the app would price on, its non-English twin, a
// label and CardTrader's scan. Shipped in cardmarket.json under "candidates".
type PriceCandidate struct {
	Product    int    `json:"product"`
	NonEnglish int    `json:"non_english"`
	Label      string `json:"label"`
	Image      string `json:"image"`
}

// priceRow is one product of a Cardmarket price guide. The guide has more
// columns (low, avg, 1/7/30-day averages); only the trend ones are kept.
// Cardmarket writes null for a column it cannot compute, which decodes to 0.
type priceRow struct {
	IDProduct int     `json:"idProduct"`
	Trend     float64 `json:"trend"`
	TrendFoil float64 `json:"trend-foil"`
}

type priceGuideFile struct {
	CreatedAt   string     `json:"createdAt"`
	PriceGuides []priceRow `json:"priceGuides"`
}

type priceGuide struct {
	asOf string // createdAt, ISO date
	rows map[int]priceRow
}

// Guides and the mapping are parsed once and re-read when the file changes,
// like the card metadata.
type pricesFileCache[T any] struct {
	modTime time.Time
	size    int64
	value   T
}

var (
	pricesCacheMu   sync.Mutex
	priceGuideFiles = map[string]*pricesFileCache[*priceGuide]{}
	priceMapFiles   = map[string]*pricesFileCache[*priceMapping]{}
)

// gamePriceState is what the app remembers about one game's guide between
// runs: when it last got a fresh copy, the ETag to ask for a cheaper 304
// next time, and whether the last attempt failed.
type gamePriceState struct {
	FetchedAt string `json:"fetched_at"` // UTC, RFC 3339; last 200 or 304
	ETag      string `json:"etag,omitempty"`
	TriedAt   string `json:"tried_at,omitempty"` // UTC, RFC 3339; last attempt
	Error     string `json:"error,omitempty"`    // last attempt's failure, "" when it succeeded
}

type pricesState struct {
	Games map[string]gamePriceState `json:"games"`
}

// CardPrice is what the frontend gets per card: the English (or only)
// product's trend and foil trend, and the non-English product's when the
// card has one. 0 means "no such price".
type CardPrice struct {
	Trend               float64 `json:"trend"`
	TrendFoil           float64 `json:"trend_foil"`
	TrendNonEnglish     float64 `json:"trend_non_english"`
	TrendNonEnglishFoil float64 `json:"trend_non_english_foil"`
	AsOf                string  `json:"as_of"` // the guide's date, ISO
	// Ref: priced through the owner's reference (price_refs), not the bundle.
	Ref bool `json:"ref"`
	// Candidates: printings the owner can choose from in the price reference
	// selector; 0 when the bundle settled the id or has nothing for it.
	Candidates int `json:"candidates"`
}

type GamePriceStatus struct {
	AsOf      string `json:"as_of"`      // guide on disk: its date; "" when none
	FetchedAt string `json:"fetched_at"` // last successful fetch, RFC 3339; "" when never
	Error     string `json:"error"`      // last attempt's failure, "" when fine
}

type PricesStatus struct {
	Enabled    bool                       `json:"enabled"`
	Available  bool                       `json:"available"` // a mapping is installed
	BuiltAt    string                     `json:"built_at"`  // the mapping's build date
	Refreshing bool                       `json:"refreshing"`
	Stale      bool                       `json:"stale"` // some mapped game has no guide, or one older than priceStaleAge
	Games      map[string]GamePriceStatus `json:"games"`
}

// GameValue is the worth of every copy of one game, from the prices on disk.
type GameValue struct {
	Value  float64 `json:"value"`
	Priced int     `json:"priced"` // copies that have a price
	Copies int     `json:"copies"`
}

// ── Settings ─────────────────────────────────────────────────────────────

func getSetting(db *sql.DB, key string) (string, bool, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return v, err == nil, err
}

func setSetting(db *sql.DB, key, value string) error {
	_, err := db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// pricesEnabled is the user's choice; on by default.
func (a *App) pricesEnabled() bool {
	if a.db == nil {
		return false
	}
	v, ok, err := getSetting(a.db, pricesEnabledKey)
	if err != nil || !ok {
		return true
	}
	return v != "0"
}

// ── Files ────────────────────────────────────────────────────────────────

func (a *App) pricesDirPath() string {
	if a.pricesDir != "" {
		return a.pricesDir
	}
	return defaultPricesDir
}

func (a *App) priceGuidePath(game string) string {
	return filepath.Join(a.pricesDirPath(), "price_guide_"+game+".json")
}

func (a *App) priceMappingDirs() []string {
	if a.priceMappingDirsFn != nil {
		return a.priceMappingDirsFn()
	}
	return allModelMetadataDirs()
}

func (a *App) priceGuideURL(gameID int) string {
	if a.priceGuideURLFn != nil {
		return a.priceGuideURLFn(gameID)
	}
	return fmt.Sprintf(priceGuideURLPattern, gameID)
}

// readCached parses path through cache when the file changed since the
// last read. A missing or unreadable file yields the zero value and false.
func readCached[T any](cache map[string]*pricesFileCache[T], path string, parse func([]byte) (T, error)) (T, bool) {
	var zero T
	info, err := os.Stat(path)
	if err != nil {
		return zero, false
	}
	pricesCacheMu.Lock()
	defer pricesCacheMu.Unlock()
	if c := cache[path]; c != nil && c.modTime.Equal(info.ModTime()) && c.size == info.Size() {
		return c.value, true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, false
	}
	v, err := parse(data)
	if err != nil {
		fmt.Printf("bad prices file %s: %v\n", path, err)
		return zero, false
	}
	cache[path] = &pricesFileCache[T]{modTime: info.ModTime(), size: info.Size(), value: v}
	return v, true
}

func parsePriceMapping(data []byte) (*priceMapping, error) {
	m := &priceMapping{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, err
	}
	if m.Cards == nil {
		m.Cards = map[string]int{}
	}
	if m.CardsNonEnglish == nil {
		m.CardsNonEnglish = map[string]int{}
	}
	if m.Games == nil {
		m.Games = map[string]int{}
	}
	if m.Candidates == nil {
		m.Candidates = map[string][]PriceCandidate{}
	}
	return m, nil
}

func parsePriceGuide(data []byte) (*priceGuide, error) {
	var f priceGuideFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	g := &priceGuide{asOf: f.CreatedAt, rows: make(map[int]priceRow, len(f.PriceGuides))}
	if len(g.asOf) >= 10 {
		g.asOf = g.asOf[:10]
	}
	for _, r := range f.PriceGuides {
		g.rows[r.IDProduct] = r
	}
	return g, nil
}

// priceMapping is the first cardmarket.json found in the installed bundles.
// nil when no bundle ships one: the app then has no prices and says so.
func (a *App) priceMapping() *priceMapping {
	for _, dir := range a.priceMappingDirs() {
		if m, ok := readCached(priceMapFiles, filepath.Join(dir, priceMappingFile), parsePriceMapping); ok {
			return m
		}
	}
	return nil
}

func (a *App) priceGuide(game string) *priceGuide {
	g, _ := readCached(priceGuideFiles, a.priceGuidePath(game), parsePriceGuide)
	return g
}

func (a *App) loadPricesState() pricesState {
	st := pricesState{Games: map[string]gamePriceState{}}
	data, err := os.ReadFile(filepath.Join(a.pricesDirPath(), pricesStateFile))
	if err == nil {
		if err := json.Unmarshal(data, &st); err != nil {
			fmt.Printf("bad prices state: %v\n", err)
		}
	}
	if st.Games == nil {
		st.Games = map[string]gamePriceState{}
	}
	return st
}

func (a *App) savePricesState(st pricesState) error {
	data, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(a.pricesDirPath(), pricesStateFile), data)
}

// writeFileAtomic writes to a temp file in the same directory, then renames:
// a cut connection or a crash never leaves a half-written file at path.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ── Lookup ───────────────────────────────────────────────────────────────

// priceOf finds a product in the guides of the mapped games. Cardmarket
// product ids are unique across games, so the first guide holding it is
// the right one.
func (a *App) priceOf(m *priceMapping, product int) (priceRow, string, bool) {
	if product == 0 {
		return priceRow{}, "", false
	}
	for _, game := range sortedKeys(m.Games) {
		g := a.priceGuide(game)
		if g == nil {
			continue
		}
		if r, ok := g.rows[product]; ok {
			return r, g.asOf, true
		}
	}
	return priceRow{}, "", false
}

func (a *App) priceRefs() map[string]PriceRef {
	if a.db == nil {
		return map[string]PriceRef{}
	}
	refs, err := getPriceRefs(a.db)
	if err != nil {
		return map[string]PriceRef{}
	}
	return refs
}

// cardPrice prices one card id: the owner's reference when there is one,
// else the bundle's product and non-English twin. An id without a price
// but with candidates is reported (Candidates > 0, every column 0) so the
// UI can offer the selector; an id with neither is absent.
func (a *App) cardPrice(m *priceMapping, refs map[string]PriceRef, cardID string) (CardPrice, bool) {
	p := CardPrice{Candidates: len(m.Candidates[cardID])}
	product, twin := m.Cards[cardID], m.CardsNonEnglish[cardID]
	if r, ok := refs[cardID]; ok {
		product, twin, p.Ref = r.Product, r.NonEnglish, true
	}
	found := false
	if r, asOf, ok := a.priceOf(m, product); ok {
		p.Trend, p.TrendFoil, p.AsOf = r.Trend, r.TrendFoil, asOf
		found = true
	}
	if r, asOf, ok := a.priceOf(m, twin); ok {
		p.TrendNonEnglish, p.TrendNonEnglishFoil = r.Trend, r.TrendFoil
		if p.AsOf == "" {
			p.AsOf = asOf
		}
		found = true
	}
	if !found || (p.Trend == 0 && p.TrendFoil == 0 && p.TrendNonEnglish == 0 && p.TrendNonEnglishFoil == 0) {
		if p.Candidates > 0 {
			return CardPrice{Ref: p.Ref, Candidates: p.Candidates}, true
		}
		return CardPrice{}, false
	}
	return p, true
}

// copyValue is what one copy is worth: the non-English product for a copy
// in another language when the card has one, else the English (or only)
// product; the foil column for a foil copy when the product has one, else
// the normal column. false when the copy has no price at all. A card
// present on the non-English shelf only has an empty English pair, so its
// English copies are unpriced rather than priced on the wrong shelf.
func copyValue(c CollectionCard, p CardPrice) (float64, bool) {
	trend, foil := p.Trend, p.TrendFoil
	if c.Lang != defaultLanguage && (p.TrendNonEnglish > 0 || p.TrendNonEnglishFoil > 0) {
		trend, foil = p.TrendNonEnglish, p.TrendNonEnglishFoil
	}
	if c.Foil && foil > 0 {
		return foil, true
	}
	if trend > 0 {
		return trend, true
	}
	return 0, false
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ── Bindings ─────────────────────────────────────────────────────────────

// GetCardPrices returns the prices of the given cards, from the guides on
// disk. A card without a mapping or without a price is absent from the map;
// no mapping at all yields an empty map, not an error.
func (a *App) GetCardPrices(cardIDs []string) (map[string]CardPrice, error) {
	out := map[string]CardPrice{}
	m := a.priceMapping()
	if m == nil {
		return out, nil
	}
	refs := a.priceRefs()
	for _, id := range cardIDs {
		if p, ok := a.cardPrice(m, refs, id); ok {
			out[id] = p
		}
	}
	return out, nil
}

func (a *App) GetPricesStatus() (PricesStatus, error) {
	st := PricesStatus{Enabled: a.pricesEnabled(), Refreshing: a.pricesRefreshing.Load(), Games: map[string]GamePriceStatus{}}
	m := a.priceMapping()
	if m == nil {
		return st, nil
	}
	st.Available = true
	st.BuiltAt = m.BuiltAt
	state := a.loadPricesState()
	now := time.Now()
	for _, game := range sortedKeys(m.Games) {
		gs := GamePriceStatus{FetchedAt: state.Games[game].FetchedAt, Error: state.Games[game].Error}
		if g := a.priceGuide(game); g != nil {
			gs.AsOf = g.asOf
		}
		fetched, err := time.Parse(time.RFC3339, gs.FetchedAt)
		if gs.AsOf == "" || err != nil || now.Sub(fetched) > priceStaleAge {
			st.Stale = true
		}
		st.Games[game] = gs
	}
	return st, nil
}

// RefreshPrices fetches every mapped game's guide now, whatever its age,
// and returns the failures (a game that fails keeps its previous guide).
func (a *App) RefreshPrices() error {
	err := a.refreshPrices(true)
	a.emitPricesUpdated()
	return err
}

func (a *App) SetPricesEnabled(enabled bool) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	v := "1"
	if !enabled {
		v = "0"
	}
	if err := setSetting(a.db, pricesEnabledKey, v); err != nil {
		return err
	}
	if enabled {
		go a.autoRefreshPrices()
	}
	return nil
}

// GetGameValues sums the worth of every copy, per game ("" for the
// unknown-game bucket), from the prices on disk.
func (a *App) GetGameValues() (map[string]GameValue, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	out := map[string]GameValue{}
	refs := a.priceRefs() // before the rows cursor below holds the connection
	rows, err := a.db.Query(`SELECT card_id, game, foil, lang FROM cards`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := a.priceMapping()
	prices := map[string]CardPrice{}
	for rows.Next() {
		var c CollectionCard
		var foil int
		if err := rows.Scan(&c.CardID, &c.Game, &foil, &c.Lang); err != nil {
			return nil, err
		}
		c.Foil = foil != 0
		gv := out[c.Game]
		gv.Copies++
		if m != nil {
			p, ok := prices[c.CardID]
			if !ok {
				p, _ = a.cardPrice(m, refs, c.CardID)
				prices[c.CardID] = p
			}
			if v, ok := copyValue(c, p); ok {
				gv.Value += v
				gv.Priced++
			}
		}
		out[c.Game] = gv
	}
	return out, rows.Err()
}

// ── Refresh ──────────────────────────────────────────────────────────────

// autoRefreshPrices is the start-of-day routine: fetch what is older than a
// day, in the background, and tell the frontend when done. Nothing happens
// when prices are off or no bundle ships a mapping.
func (a *App) autoRefreshPrices() {
	if err := a.refreshPrices(false); err != nil {
		fmt.Printf("prices refresh: %v\n", err)
	}
	a.emitPricesUpdated()
}

func (a *App) emitPricesUpdated() {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, pricesUpdatedEvent)
	}
}

// refreshPrices fetches the guides of every mapped game: all of them when
// force, else only those fetched more than priceRefreshAge ago or missing.
// One refresh runs at a time; a second call waits for the first.
func (a *App) refreshPrices(force bool) error {
	a.pricesMu.Lock()
	defer a.pricesMu.Unlock()
	m := a.priceMapping()
	if m == nil || !a.pricesEnabled() {
		return nil
	}
	a.pricesRefreshing.Store(true)
	defer a.pricesRefreshing.Store(false)

	state := a.loadPricesState()
	var errs []error
	for _, game := range sortedKeys(m.Games) {
		gs := state.Games[game]
		path := a.priceGuidePath(game)
		if !force {
			fetched, err := time.Parse(time.RFC3339, gs.FetchedAt)
			if _, statErr := os.Stat(path); statErr == nil && err == nil && time.Since(fetched) < priceRefreshAge {
				continue
			}
		}
		gs.TriedAt = time.Now().UTC().Format(time.RFC3339)
		if err := a.fetchPriceGuide(a.priceGuideURL(m.Games[game]), path, &gs); err != nil {
			gs.Error = err.Error()
			errs = append(errs, fmt.Errorf("%s: %w", game, err))
		} else {
			gs.Error = ""
		}
		state.Games[game] = gs
		// Saved after every game, so a crash keeps what was fetched.
		if err := a.savePricesState(state); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

var priceHTTPClient = &http.Client{Timeout: 3 * time.Minute}

// fetchPriceGuide downloads one guide to path, atomically. With an ETag
// from a previous fetch and the file still on disk, it asks for a 304,
// which costs one round-trip and no transfer. On success gs is updated.
func (a *App) fetchPriceGuide(url, path string, gs *gamePriceState) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "TCGScanner desktop app (price guide refresh)")
	if _, statErr := os.Stat(path); statErr == nil && gs.ETag != "" {
		req.Header.Set("If-None-Match", gs.ETag)
	}
	resp, err := priceHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		gs.FetchedAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	case http.StatusOK:
	default:
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	// A cut transfer or an error page must not replace a good guide.
	if _, err := parsePriceGuide(data); err != nil {
		return fmt.Errorf("not a price guide: %w", err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return err
	}
	gs.ETag = resp.Header.Get("ETag")
	gs.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// PriceCandidateView is a candidate as the selector shows it: with today's
// price of its products and whether it is the owner's current choice.
type PriceCandidateView struct {
	PriceCandidate
	Price  CardPrice `json:"price"`
	Chosen bool      `json:"chosen"`
}

// GetPriceCandidates lists the printings the owner may choose for an id
// the bundle could not settle. Empty for a settled or unknown id.
func (a *App) GetPriceCandidates(cardID string) ([]PriceCandidateView, error) {
	m := a.priceMapping()
	if m == nil {
		return nil, nil
	}
	ref, hasRef := a.priceRefs()[cardID]
	var out []PriceCandidateView
	for _, c := range m.Candidates[cardID] {
		v := PriceCandidateView{PriceCandidate: c, Chosen: hasRef && ref.Product == c.Product}
		if r, asOf, ok := a.priceOf(m, c.Product); ok {
			v.Price.Trend, v.Price.TrendFoil, v.Price.AsOf = r.Trend, r.TrendFoil, asOf
		}
		if r, asOf, ok := a.priceOf(m, c.NonEnglish); ok {
			v.Price.TrendNonEnglish, v.Price.TrendNonEnglishFoil = r.Trend, r.TrendFoil
			if v.Price.AsOf == "" {
				v.Price.AsOf = asOf
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// SetPriceRef records the owner's choice: product must be one of the id's
// candidates, whose label and non-English twin are stored with it.
func (a *App) SetPriceRef(cardID string, product int) error {
	if a.db == nil {
		return fmt.Errorf("no database")
	}
	m := a.priceMapping()
	if m == nil {
		return fmt.Errorf("no price mapping loaded")
	}
	for _, c := range m.Candidates[cardID] {
		if c.Product == product {
			if err := setPriceRef(a.db, PriceRef{CardID: cardID, Product: c.Product, NonEnglish: c.NonEnglish, Label: c.Label}); err != nil {
				return err
			}
			a.emitPricesUpdated()
			return nil
		}
	}
	return fmt.Errorf("product %d is not a candidate of %s", product, cardID)
}

func (a *App) ClearPriceRef(cardID string) error {
	if a.db == nil {
		return fmt.Errorf("no database")
	}
	if err := clearPriceRef(a.db, cardID); err != nil {
		return err
	}
	a.emitPricesUpdated()
	return nil
}

func (a *App) GetPriceRefs() (map[string]PriceRef, error) {
	if a.db == nil {
		return map[string]PriceRef{}, nil
	}
	return getPriceRefs(a.db)
}

const (
	priceImageAllowedPrefix = "https://cardtrader.com/uploads/"
	priceImageMaxBytes      = 4 << 20
)

func (a *App) priceImageAllowed(url string) bool {
	if a.priceImageAllowFn != nil {
		return a.priceImageAllowFn(url)
	}
	return strings.HasPrefix(url, priceImageAllowedPrefix)
}

// GetPriceCandidateImage returns a candidate's CardTrader scan as a data
// URL for the price reference selector. Fetched once (an explicit
// User-Agent, one request per call) and cached under ./prices/images/ by
// the URL's hash; "" when the fetch fails, so the dialog shows "no image".
// Only CardTrader's upload URLs are accepted.
func (a *App) GetPriceCandidateImage(url string) (string, error) {
	if !a.priceImageAllowed(url) {
		return "", fmt.Errorf("not a CardTrader image URL")
	}
	sum := sha1.Sum([]byte(url))
	path := filepath.Join(a.pricesDirPath(), "images", hex.EncodeToString(sum[:])+".jpg")
	data, err := os.ReadFile(path)
	if err != nil {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return "", nil
		}
		req.Header.Set("User-Agent", "TCGScanner desktop app (price reference images)")
		resp, err := priceHTTPClient.Do(req)
		if err != nil {
			return "", nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", nil
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, priceImageMaxBytes))
		if err != nil || len(data) < 1000 {
			return "", nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			_ = writeFileAtomic(path, data)
		}
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data), nil
}
