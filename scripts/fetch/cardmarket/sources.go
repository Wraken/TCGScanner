package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Every upstream read by the builder goes through fetch(): downloaded into
// cache/ (gitignored) and re-read from there under -offline, like the One
// Piece fetcher does, so a build can be replayed without touching the
// network and a flaky source never blocks a rerun.

const cacheDir = "cache"

var client = &http.Client{Timeout: 120 * time.Second}

func fetch(name, url string, offline bool, headers map[string]string) ([]byte, error) {
	path := filepath.Join(cacheDir, name)
	if offline {
		return os.ReadFile(path)
	}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "TCGScanner cardmarket mapper (personal ML project)")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	return data, os.WriteFile(path, data, 0o644)
}

// ── Cardmarket public catalog ────────────────────────────────────────────

const cmBase = "https://downloads.s3.cardmarket.com/productCatalog"

// cmGames maps our game names to Cardmarket's idGame, read off the public
// product lists on 2026-10-03.
var cmGames = map[string]int{"pokemon": 6, "onepiece": 18, "lorcana": 19, "riftbound": 22}

type cmProduct struct {
	IDProduct   int    `json:"idProduct"`
	Name        string `json:"name"`
	IDExpansion int    `json:"idExpansion"`
}

type cmCatalog struct {
	CreatedAt string      `json:"createdAt"`
	Products  []cmProduct `json:"products"`
}

func loadCardmarketCatalog(game string, offline bool) (map[int]cmProduct, string, error) {
	id := cmGames[game]
	data, err := fetch(fmt.Sprintf("products_singles_%d.json", id),
		fmt.Sprintf("%s/productList/products_singles_%d.json", cmBase, id), offline, nil)
	if err != nil {
		return nil, "", err
	}
	var cat cmCatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, "", fmt.Errorf("products_singles_%d.json: %w", id, err)
	}
	byID := make(map[int]cmProduct, len(cat.Products))
	for _, p := range cat.Products {
		byID[p.IDProduct] = p
	}
	return byID, cat.CreatedAt, nil
}

// shelfLang is the language of a Cardmarket expansion ("shelf"), read off
// the names of its sealed products in products_nonsingles: a shelf selling
// a "Romance Dawn Booster (Non-English)" is the non-English print of
// Romance Dawn. A shelf with no sealed product takes the opposite language
// of its twin (the shelf sharing most of its single names) when the twin
// is known: duplicate shelves come in language pairs. Anything else stays
// unknown, and an unknown shelf never wins a tie.
type shelfLang int

// shelfEnglishSealed counts the plain-English sealed products per shelf,
// for the one tie rule B cannot settle by language alone: two English
// shelves of one set (EB-03 has a normal and an "Asia Region Legal" run,
// both English). The run with the booster box is the plain one.
var shelfEnglishSealed = map[int]int{}

const (
	shelfUnknown shelfLang = iota
	shelfEnglish
	shelfForeign // the set's other full print: another language, or the "Asia Region Legal" run
	shelfEvent   // pre-release, tournament and anniversary packs: stamped or foil variants
)

// foreignShelfSealed are the sealed-product words that mark a shelf as the
// set's other full print, the one a non-English copy is priced on: another
// language ("Non-English", "Japanese"), or the "Asia Region Legal" run,
// which Cardmarket files on the same shelf as the non-English boosters
// (OP-06, OP-13) and alone from OP-14 on, where it is the only second
// shelf. "Exchange Meeting" is a Japanese event: the non-English promo
// shelf sells nothing else (measured 2026-10-07; Cardmarket's site sits
// behind a bot check, so the shelf names themselves were not read).
var foreignShelfSealed = []string{
	"non-english", "japanese", "chinese", "korean", "asian", "(jp)", "(cn)", "(kr)",
	"asia region", "exchange meeting",
}

// eventShelfSealed mark the pre-release / release-event / anniversary /
// tournament shelves, whose singles are stamped or foil variants of the
// set's cards: never the English shelf, never a non-English twin.
var eventShelfSealed = []string{
	"pre-release", "prerelease", "release event", "anniversary", "tournament pack",
	"pre-errata", "event pack", "celebration pack", "participation",
}

func hasAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func loadShelfLanguages(game string, offline bool, catalog map[int]cmProduct) (map[int]shelfLang, error) {
	id := cmGames[game]
	data, err := fetch(fmt.Sprintf("products_nonsingles_%d.json", id),
		fmt.Sprintf("%s/productList/products_nonsingles_%d.json", cmBase, id), offline, nil)
	if err != nil {
		return nil, err
	}
	var sealed cmCatalog
	if err := json.Unmarshal(data, &sealed); err != nil {
		return nil, fmt.Errorf("products_nonsingles_%d.json: %w", id, err)
	}
	en, foreign, event := map[int]int{}, map[int]int{}, map[int]int{}
	for _, p := range sealed.Products {
		name := strings.ToLower(p.Name)
		switch {
		case hasAny(name, eventShelfSealed):
			event[p.IDExpansion]++
		case hasAny(name, foreignShelfSealed):
			foreign[p.IDExpansion]++
		default:
			en[p.IDExpansion]++
		}
	}
	names := map[int]map[string]bool{}
	for _, p := range catalog {
		if names[p.IDExpansion] == nil {
			names[p.IDExpansion] = map[string]bool{}
		}
		names[p.IDExpansion][p.Name] = true
	}
	lang := map[int]shelfLang{}
	shelfEnglishSealed = en
	for exp := range names {
		other := foreign[exp] + event[exp]
		otherLang := shelfForeign
		if event[exp] > foreign[exp] {
			otherLang = shelfEvent
		}
		switch {
		case en[exp] > 0 && other == 0:
			lang[exp] = shelfEnglish
		case other > 0 && en[exp] == 0:
			lang[exp] = otherLang
		case en[exp] >= 3*other && en[exp] > 0:
			// A mostly-English shelf that also sells an "Asia Region Legal"
			// case or two.
			lang[exp] = shelfEnglish
		case other >= 3*en[exp] && other > 0:
			lang[exp] = otherLang
		}
	}
	for exp, ns := range names {
		if lang[exp] != shelfUnknown {
			continue
		}
		bestExp, best := 0, 0
		for twin, ts := range names {
			if twin == exp {
				continue
			}
			n := 0
			for name := range ns {
				if ts[name] {
					n++
				}
			}
			if n > best {
				best, bestExp = n, twin
			}
		}
		// Twins share at least half of their names both ways: a 12-card
		// McDonald's shelf has half its names in the base set without
		// being its twin.
		if bestExp == 0 || best*2 < len(ns) || best*2 < len(names[bestExp]) {
			continue
		}
		// Duplicate shelves come in language pairs: the twin of an English
		// shelf is the foreign one, and back. An event shelf has no twin.
		switch lang[bestExp] {
		case shelfEnglish:
			lang[exp] = shelfForeign
		case shelfForeign:
			lang[exp] = shelfEnglish
		}
	}
	return lang, nil
}

// ── Source: visual tie-break (visual_tiebreak.py) ────────────────────────
//
// visual.json holds the picks of the embedding comparison run on the
// training PC: card id -> {product, score, lead}. Read as a source for the
// cards the blueprint step left ambiguous; a pick for a card that is no
// longer ambiguous (a later source resolved it) is ignored, so a stale file
// never overrides a surer answer.

// ── Source: manual.json (the review tool's hand picks) ────────────────────
//
// {"picks": {"<card id>": <product id>}, "none": ["<card id>", ...]}
// A pick names a printing by any of its Cardmarket products: the mapper
// chooses the English shelf, as for a visual pick. "none" is the verdict
// that no candidate matches: the card is neither auto-picked nor listed as
// ambiguous again. "_<id>" keys are comments.

const manualFile = "manual.json"

type manualPicks struct {
	Picks map[string]int
	None  map[string]bool
}

func loadManual() (manualPicks, error) {
	data, err := os.ReadFile(manualFile)
	if errors.Is(err, fs.ErrNotExist) {
		return manualPicks{}, nil
	}
	if err != nil {
		return manualPicks{}, err
	}
	m, err := parseManual(data)
	if err != nil {
		return manualPicks{}, fmt.Errorf("%s: %w", manualFile, err)
	}
	return m, nil
}

func parseManual(data []byte) (manualPicks, error) {
	m := manualPicks{Picks: map[string]int{}, None: map[string]bool{}}
	if len(data) == 0 {
		return m, nil
	}
	var f struct {
		Picks map[string]any `json:"picks"`
		None  []string       `json:"none"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return manualPicks{}, err
	}
	for id, v := range f.Picks {
		if strings.HasPrefix(id, "_") {
			continue
		}
		p, ok := v.(float64)
		if !ok || p <= 0 || p != float64(int(p)) {
			return manualPicks{}, fmt.Errorf("%q must be a positive product id", id)
		}
		m.Picks[id] = int(p)
	}
	for _, id := range f.None {
		m.None[id] = true
	}
	return m, nil
}

const visualFile = "visual.json"

type visualPick struct {
	Product int     `json:"product"`
	Score   float64 `json:"score"`
	Lead    float64 `json:"lead"`
}

func loadVisual() (map[string]visualPick, error) {
	data, err := os.ReadFile(visualFile)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]visualPick{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Margin float64               `json:"margin"`
		Floor  float64               `json:"floor"`
		Picks  map[string]visualPick `json:"picks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", visualFile, err)
	}
	// A pick is only as good as the margin it cleared. The file records the
	// thresholds its run used; every pick is re-checked against them here,
	// so a hand-edited or loosely-run file cannot slip a close call in.
	picks := map[string]visualPick{}
	for id, p := range f.Picks {
		if p.Product == 0 || p.Score < f.Floor || p.Lead < f.Margin {
			continue
		}
		picks[id] = p
	}
	if n := len(f.Picks) - len(picks); n > 0 {
		fmt.Printf("  visual: %d pick(s) under the file's own thresholds ignored\n", n)
	}
	return picks, nil
}

// ── Our card metadata (the fetchers' cards.json) ─────────────────────────

type ourCard struct {
	Name   string `json:"name"`
	Set    string `json:"set"`
	Number string `json:"number"`
	// Pack is the product a One Piece printing comes from ("PRB-02",
	// "ST-34", "OP14-EB04", "PROMO"; "" when unknown), see rule C.
	Pack string `json:"pack"`
	// Riftbound ids end with the set's card count ("ogn-001-298"): the
	// number a showcase is counted past. 0 for the other games.
	setSize int
}

func loadOurCards(game string) (map[string]ourCard, error) {
	data, err := os.ReadFile(filepath.Join("..", game, "cards.json"))
	if err != nil {
		return nil, err
	}
	cards := map[string]ourCard{}
	if err := json.Unmarshal(data, &cards); err != nil {
		return nil, fmt.Errorf("%s/cards.json: %w", game, err)
	}
	if game == "riftbound" {
		for id, c := range cards {
			if i := strings.LastIndex(id, "-"); i >= 0 {
				fmt.Sscanf(id[i+1:], "%d", &c.setSize)
				cards[id] = c
			}
		}
	}
	return cards, nil
}

// isPocket reports whether a Pokémon id belongs to Pokémon TCG Pocket (the
// mobile game: A1-001, A2a-080, B1-019, P-A-001). Those cards are never
// printed and never priced, so they are left out of the build and of the
// coverage count (user decision, 2026-10-07).
func isPocket(id string) bool {
	if strings.HasPrefix(id, "P-A-") {
		return true
	}
	if len(id) < 2 || (id[0] != 'A' && id[0] != 'B') || id[1] < '0' || id[1] > '9' {
		return false
	}
	set, _, _ := strings.Cut(id, "-")
	return len(set) <= 3
}

// ── Source: TCGdex (Pokémon) ─────────────────────────────────────────────
//
// TCGdex publishes pricing.cardmarket.idProduct per card, one request each;
// ids are kept in tcgdex_ids.json (committed next to this file) so the
// ~21k-request walk only ever runs for ids not seen before.

const tcgdexBase = "https://api.tcgdex.net/v2/en/cards"
const tcgdexIDsFile = "tcgdex_ids.json"
const tcgdexDelay = 80 * time.Millisecond

type tcgdexCard struct {
	ID      string `json:"id"`
	Pricing struct {
		Cardmarket *struct {
			IDProduct int `json:"idProduct"`
		} `json:"cardmarket"`
	} `json:"pricing"`
}

// tcgdexIDs resolves every id through the saved file first, then asks TCGdex
// for the rest (unless offline). A card TCGdex knows but has no Cardmarket
// product for is saved as 0 so it is not asked again; -retry-missing lifts
// that. Progress is flushed every 200 lookups, atomically.
func tcgdexIDs(ids []string, offline, retryMissing bool) (map[string]int, error) {
	known := map[string]int{}
	if data, err := os.ReadFile(tcgdexIDsFile); err == nil {
		if err := json.Unmarshal(data, &known); err != nil {
			return nil, fmt.Errorf("%s: %w", tcgdexIDsFile, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var pending []string
	for _, id := range ids {
		if v, ok := known[id]; !ok || (retryMissing && v == 0) {
			pending = append(pending, id)
		}
	}
	if len(pending) == 0 || offline {
		if len(pending) > 0 {
			fmt.Printf("  tcgdex: offline, %d ids not looked up\n", len(pending))
		}
		return known, nil
	}
	fmt.Printf("  tcgdex: %d ids to look up (~%s)\n", len(pending),
		(time.Duration(len(pending)) * tcgdexDelay).Round(time.Minute))
	failed, sinceFlush := 0, 0
	for i, id := range pending {
		resp, err := client.Get(tcgdexBase + "/" + id)
		if err != nil {
			failed++
			time.Sleep(tcgdexDelay)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound:
			known[id] = 0
		case resp.StatusCode != http.StatusOK:
			failed++
		default:
			var c tcgdexCard
			if err := json.Unmarshal(body, &c); err != nil {
				failed++
				break
			}
			if c.Pricing.Cardmarket != nil && c.Pricing.Cardmarket.IDProduct != 0 {
				known[id] = c.Pricing.Cardmarket.IDProduct
			} else {
				known[id] = 0
			}
		}
		sinceFlush++
		if sinceFlush >= 200 || i == len(pending)-1 {
			if err := writeJSON(tcgdexIDsFile, known); err != nil {
				return nil, err
			}
			sinceFlush = 0
			fmt.Printf("  tcgdex: %d/%d\n", i+1, len(pending))
		}
		time.Sleep(tcgdexDelay)
	}
	if failed > 0 {
		fmt.Printf("  tcgdex: %d request(s) failed, re-run to retry them\n", failed)
	}
	return known, nil
}

// ── Source: LorcanaJSON (Lorcana) ────────────────────────────────────────
//
// allCards.json carries externalLinks.cardmarketId per card. Our ids are
// <set>-<number><variant>, where promos use the promo grouping as the set
// ("P1-4"): the same key is rebuilt from LorcanaJSON's fields.

const lorcanaJSONURL = "https://lorcanajson.org/files/current/en/allCards.json.zip"

type lorcanaJSONCard struct {
	SetCode       string `json:"setCode"`
	Number        int    `json:"number"`
	Variant       string `json:"variant"`
	PromoGrouping string `json:"promoGrouping"`
	ExternalLinks struct {
		CardmarketID int `json:"cardmarketId"`
	} `json:"externalLinks"`
}

func lorcanaJSONIDs(offline bool) (map[string]int, error) {
	data, err := fetch("lorcanajson-allCards.json.zip", lorcanaJSONURL, offline, nil)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("lorcanajson zip: %w", err)
	}
	var raw []byte
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".json") {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			raw, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			break
		}
	}
	var file struct {
		Cards []lorcanaJSONCard `json:"cards"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("lorcanajson allCards.json: %w", err)
	}
	ids := map[string]int{}
	for _, c := range file.Cards {
		if c.ExternalLinks.CardmarketID == 0 {
			continue
		}
		set := c.SetCode
		if c.PromoGrouping != "" {
			set = c.PromoGrouping
		}
		key := set + "-" + strconv.Itoa(c.Number) + c.Variant
		// Two LorcanaJSON entries can share a key (Moana/Vaiana, P2-26) and
		// then share the product too; a real disagreement is a bug to see.
		if prev, ok := ids[key]; ok && prev != c.ExternalLinks.CardmarketID {
			return nil, fmt.Errorf("lorcanajson: %s maps to both %d and %d", key, prev, c.ExternalLinks.CardmarketID)
		}
		ids[key] = c.ExternalLinks.CardmarketID
	}
	return ids, nil
}

// ── Source: CardTrader (every game; needs CARDTRADER_TOKEN) ──────────────
//
// One request per expansion lists its blueprints, each carrying the
// Cardmarket ids of the printing. Blueprints are matched to our cards by
// collector number in the next step; this only downloads them.

const cardTraderBase = "https://api.cardtrader.com/api/v2"

// cardTraderGames maps our game names to CardTrader's game ids, read off
// -list-games on 2026-10-07: the ids are not documented, only listed by the
// API, and a wrong one would silently fetch another game's cards.
var cardTraderGames = map[string]int{"pokemon": 5, "onepiece": 15, "lorcana": 18, "riftbound": 22}

type ctExpansion struct {
	ID     int    `json:"id"`
	GameID int    `json:"game_id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
}

type ctBlueprint struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	ExpansionID     int    `json:"expansion_id"`
	ImageURL        string `json:"image_url"`
	CardMarketIDs   []int  `json:"card_market_ids"`
	FixedProperties struct {
		CollectorNumber string `json:"collector_number"`
	} `json:"fixed_properties"`
}

func cardTraderHeaders() (map[string]string, error) {
	tok := os.Getenv("CARDTRADER_TOKEN")
	if tok == "" {
		return nil, errors.New("CARDTRADER_TOKEN is not set")
	}
	return map[string]string{"Authorization": "Bearer " + tok}, nil
}

func cardTraderGamesList(offline bool) ([]struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}, error) {
	h, err := cardTraderHeaders()
	if err != nil && !offline {
		return nil, err
	}
	data, err := fetch("cardtrader_games.json", cardTraderBase+"/games", offline, h)
	if err != nil {
		return nil, err
	}
	var out struct {
		Array []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"array"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		// Older shape: a bare array.
		var arr []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		if err2 := json.Unmarshal(data, &arr); err2 != nil {
			return nil, fmt.Errorf("cardtrader games: %w", err)
		}
		return arr, nil
	}
	return out.Array, nil
}

// cardTraderBlueprints returns every blueprint of the game, across all its
// expansions, keyed by expansion for the matcher. CardTrader allows 200
// requests per 10 s; one per expansion with a short pause stays far under.
func cardTraderBlueprints(game string, offline bool) (map[int]ctExpansion, []ctBlueprint, error) {
	gameID, ok := cardTraderGames[game]
	if !ok {
		return nil, nil, fmt.Errorf("no CardTrader game id for %s (run -list-games and fill cardTraderGames)", game)
	}
	h, err := cardTraderHeaders()
	if err != nil && !offline {
		return nil, nil, err
	}
	data, err := fetch("cardtrader_expansions.json", cardTraderBase+"/expansions", offline, h)
	if err != nil {
		return nil, nil, err
	}
	var all []ctExpansion
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, nil, fmt.Errorf("cardtrader expansions: %w", err)
	}
	exps := map[int]ctExpansion{}
	var blueprints []ctBlueprint
	for _, e := range all {
		if e.GameID != gameID {
			continue
		}
		exps[e.ID] = e
		data, err := fetch(fmt.Sprintf("cardtrader_blueprints_%d.json", e.ID),
			fmt.Sprintf("%s/blueprints/export?expansion_id=%d", cardTraderBase, e.ID), offline, h)
		if err != nil {
			fmt.Printf("  cardtrader: expansion %d (%s): %v\n", e.ID, e.Name, err)
			continue
		}
		var bps []ctBlueprint
		if err := json.Unmarshal(data, &bps); err != nil {
			fmt.Printf("  cardtrader: expansion %d (%s): %v\n", e.ID, e.Name, err)
			continue
		}
		blueprints = append(blueprints, bps...)
		if !offline {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return exps, blueprints, nil
}

func writeJSON(path string, v any) error {
	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
