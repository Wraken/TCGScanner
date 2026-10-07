package main

import (
	"fmt"
	"sort"
	"strings"
)

// How a card got its product, for the report. The order is the resolution
// order: a later step only runs for cards the earlier ones left unresolved.
type source string

const (
	srcPublished source = "published" // TCGdex / LorcanaJSON id, taken as is
	srcBlueprint source = "blueprint" // one CardTrader blueprint, one Cardmarket id
	srcVisual    source = "visual"    // visual.json, the embedding tie-break
	srcOverride  source = "override"  // overrides.json
)

type resolved struct {
	Product int
	Source  source
}

// ambiguity is a card the blueprint step could not settle: several
// blueprints share its number, or its blueprint lists several Cardmarket
// ids. Listed in the report with every candidate so overrides.json (or the
// visual tie-break, once it exists) can pick one.
type ambiguity struct {
	Card       string
	Candidates []int
}

type gameResult struct {
	Game       string
	CatalogDay string
	Mapped     map[string]resolved
	Dropped    map[string]string // card -> why validation refused it
	Ambiguous  []ambiguity
	Unresolved []string
	Skipped    int // Pokémon Pocket ids
	// NonEnglish is the same printing on the set's non-English shelf, for
	// the cards that have one (One Piece only, see nonEnglishTwins).
	NonEnglish map[string]int
	// ForeignShelf lists the cards whose only product sits on a foreign
	// shelf: CardTrader listed no English id for the printing. One Piece
	// ships them under NonEnglish alone (a copy in another language is
	// priced, an English one is not); elsewhere they stay mapped and are
	// listed for a look.
	ForeignShelf []string
}

// resolveGame runs the steps of the spec for one game. published holds the
// ids of step 1 (nil for games without such a source), blueprints the
// CardTrader export of step 2 (nil when the token is absent), overrides the
// hand-made picks. The catalog is the day's product list: every product
// must exist in it and carry a compatible name, or the card is dropped.
func resolveGame(game string, cards map[string]ourCard, published map[string]int,
	exps map[int]ctExpansion, blueprints []ctBlueprint, visual map[string]visualPick, overrides map[string]int,
	catalog map[int]cmProduct, shelf map[int]shelfLang, catalogDay string) gameResult {

	r := gameResult{Game: game, CatalogDay: catalogDay, Mapped: map[string]resolved{}, Dropped: map[string]string{}}

	ids := make([]string, 0, len(cards))
	for id := range cards {
		if game == "pokemon" && isPocket(id) {
			r.Skipped++
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	ix := indexBlueprints(exps, blueprints)

	for _, id := range ids {
		card := cards[id]
		var pick resolved
		var cands []int

		switch {
		case overrides[id] != 0:
			pick = resolved{overrides[id], srcOverride}
		case published[id] != 0:
			pick = resolved{published[id], srcPublished}
		case ix != nil:
			var product int
			product, cands = resolveBlueprint(game, id, card, ix, shelf, catalog)
			if product != 0 {
				pick = resolved{product, srcBlueprint}
			} else if v, ok := visual[id]; ok && len(cands) > 1 && containsInt(cands, v.Product) {
				// The tie-break only ever picks among today's candidates:
				// a pick that is no longer one of them is stale. It names a
				// printing, not a shelf (a printing's shelves share one
				// artwork), so rule B chooses the shelf here.
				if p, ok := visualShelf(v.Product, ix, shelf, catalog); ok {
					pick = resolved{p, srcVisual}
				}
			}
		}

		if pick.Product == 0 {
			if len(cands) > 1 {
				r.Ambiguous = append(r.Ambiguous, ambiguity{id, cands})
			} else {
				r.Unresolved = append(r.Unresolved, id)
			}
			continue
		}
		// An override is a hand-checked pick: it must still exist in today's
		// catalog, but its name is allowed to disagree (that is usually why
		// it needed an override in the first place).
		if why := validate(pick.Product, card, catalog, pick.Source == srcOverride); why != "" {
			r.Dropped[id] = fmt.Sprintf("%s (%s, product %d)", why, pick.Source, pick.Product)
			continue
		}
		r.Mapped[id] = pick
	}
	dropSharedProducts(&r, cards)
	if game == "onepiece" {
		nonEnglishTwins(&r, ix, shelf, catalog)
	}
	for _, id := range ids {
		m, ok := r.Mapped[id]
		if !ok || shelf[catalog[m.Product].IDExpansion] != shelfForeign {
			continue
		}
		r.ForeignShelf = append(r.ForeignShelf, id)
		if game == "onepiece" {
			r.NonEnglish[id] = m.Product
			delete(r.Mapped, id)
		}
	}
	return r
}

// dropSharedProducts refuses a product that two cards with different names
// both claim. The name check cannot see this case when one name contains
// the other ("Mew" in "Mewtwo", "Paras" in "Parasect", "Pikachu" in
// "Pikachu Star"): TCGdex stamped both cards with one id and the containment
// test waved the wrong one through. Measured on 2026-10-07: 43 products of
// Pokémon. Neither card is sure, so neither ships; same-name claims (a holo
// and its plain print in Jungle) are one product with two columns and stay.
func dropSharedProducts(r *gameResult, cards map[string]ourCard) {
	claims := map[int][]string{}
	for id, m := range r.Mapped {
		claims[m.Product] = append(claims[m.Product], id)
	}
	for product, ids := range claims {
		if len(ids) < 2 {
			continue
		}
		names := map[string]bool{}
		for _, id := range ids {
			names[normName(cards[id].Name)] = true
		}
		if len(names) < 2 {
			continue
		}
		sort.Strings(ids)
		for _, id := range ids {
			if r.Mapped[id].Source == srcOverride {
				continue
			}
			r.Dropped[id] = fmt.Sprintf("product %d also claimed by %s", product, strings.Join(others(ids, id), ", "))
			delete(r.Mapped, id)
		}
	}
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func others(ids []string, self string) []string {
	out := make([]string, 0, len(ids)-1)
	for _, id := range ids {
		if id != self {
			out = append(out, id)
		}
	}
	return out
}

// validate is the catalog check of the spec: the product exists today and
// its name agrees with the card's. Returns the reason to drop, or "".
func validate(product int, card ourCard, catalog map[int]cmProduct, skipName bool) string {
	p, ok := catalog[product]
	if !ok {
		return "not in today's catalog"
	}
	if !skipName && !namesAgree(p.Name, card.Name) {
		return fmt.Sprintf("name mismatch: catalog says %q", p.Name)
	}
	return ""
}

// ── CardTrader blueprints → our ids ──────────────────────────────────────
//
// A blueprint is one printing: it carries the collector number as printed,
// a version label for the non-base printings ("Alternate Art", "Pre-Release
// Stamp", null for the plain card), and the Cardmarket ids of that printing.
// Our ids name printings too, but by number: "OP01-025" is the plain card,
// "OP01-025_p1" its first parallel art, "_r1" a reprint rarity. The plain
// card is the only one the two sides name the same way, so it is the only
// one resolved here (rule A); the numbered variants cannot be told apart
// from a blueprint's letter suffix and go to the report as ambiguous, with
// every candidate, for the visual tie-break.
//
// Rule A: our plain id (no "_" suffix) ↔ the one blueprint in the set's home
// expansion whose collector number is exactly our code and whose version is
// null. Riftbound numbers carry their own variant letter ("007a", "299*"),
// so the exact-number match already names the printing there.
//
// Rule B (One Piece): a blueprint lists one Cardmarket id per language shelf
// (Cardmarket sells "Romance Dawn" and "Romance Dawn (Non-English)" as two
// expansions, same metacard, prices apart by ×1.6 median). The English one
// is the product whose Cardmarket expansion is English, read off the
// sealed-product names of products_nonsingles (every "Non-English" booster
// names its shelf). Dates do not tell them apart (half the pairs were added
// Japanese-first). A blueprint whose ids are all on unknown shelves stays
// ambiguous.

// homeExpansions maps our set code to the CardTrader expansion codes that
// hold its plain printings. One Piece codes are "op01" / "eb-01" / "st-29"
// style with no rule for the dash; both spellings are tried. Riftbound codes
// are our lower-cased set.
func homeExpansions(game, set string) []string {
	s := strings.ToLower(set)
	if game == "onepiece" {
		if s == "p" {
			return []string{"promo"}
		}
		if alt, ok := onePieceHomes[s]; ok {
			return alt
		}
		for _, pre := range []string{"op", "st", "eb", "prb"} {
			if strings.HasPrefix(s, pre) {
				n := s[len(pre):]
				return []string{pre + n, pre + "-" + n}
			}
		}
	}
	return []string{s}
}

// onePieceHomes names the CardTrader expansions a One Piece set's plain
// cards live in when they are not under the set's own code: EB-04 Egghead
// Crisis shipped alongside OP-14 and OP-15, and CardTrader files its cards
// under "op14" and "op15" ("eb04" holds a handful of event cards only).
// Read off the blueprint export on 2026-10-07.
var onePieceHomes = map[string][]string{
	"eb04": {"op14", "op15", "eb04"},
}

type bpKey struct {
	Exp    string // CardTrader expansion code, lower case
	Number string // collector number as printed, upper case
}

// variantWords are the words in a blueprint's version that name another
// printing of the card: an alternate art, a stamp, a signed or foil-only
// run, a reprint. A version made of anything else (CardTrader also writes
// the expansion code, "OGN", or the rarity, "Secret Rare", there) still
// names the plain card. A closed list, so an unknown word never promotes a
// blueprint to "plain" by accident: it keeps the card ambiguous instead.
var variantWords = []string{
	"alternate", "alternative", "art", "stamp", "showcase", "signed", "foil", "reprint",
	"manga", "parallel", "wanted", "revision", "anniversary", "championship", "champion",
	"promo", "prize", "winner", "finalist", "top ", "participation", "pack",
	"sleeve", "box", "deck", "case", "collection", "edition", "exclusive", "event",
	"stamped", "serialized", "oversized", "non-foil", "pre-release", "prerelease",
	"asia", "asian", "chinese", "korean", "japanese", "french", "gift", "fest", "campaign",
	"skirmish", "regional", "qualifier", "bundle", "gencon", "judge", "store", "demo",
	"tournament", "celebration", "tin", "complete", "plated", "metal", "holographic",
	"legend |", "crystal", "heartsteel", "lunar", "garden", "night", "boss", "vault",
	"ultimate", "black & gold",
}

func marksVariant(version string) bool {
	v := strings.ToLower(strings.TrimSpace(version))
	if v == "" {
		return false
	}
	for _, w := range variantWords {
		if strings.Contains(v, w) {
			return true
		}
	}
	return false
}

// blueprintIndex keys every plain blueprint (no variant word in its version)
// by expansion and number. Variant blueprints are kept aside, under the
// same key, so the report can list them as candidates for the variant ids.
type blueprintIndex struct {
	plain     map[bpKey][]ctBlueprint
	versioned map[bpKey][]ctBlueprint
	// Every blueprint whose number starts with a base code, keyed by
	// (expansion, base code), and again by base code alone across every
	// expansion: the candidate pool of a variant id.
	byBase    map[bpKey][]ctBlueprint
	allByBase map[string][]ctBlueprint
	// Every blueprint listing a Cardmarket id, by that id: the way back
	// from a product to its printing and the printing's other shelves.
	byCM map[int][]ctBlueprint
}

// variantBase strips CardTrader's printing suffix from a collector
// number: "OP01-025A" -> "OP01-025", "P-001P2" -> "P-001", "007A" -> "007".
// The base is the leading "<set>-<digits>" (or bare digits for Riftbound);
// whatever follows the digit run is the suffix.
func variantBase(num string) string {
	i := strings.LastIndex(num, "-")
	start := i + 1
	end := start
	for end < len(num) && num[end] >= '0' && num[end] <= '9' {
		end++
	}
	if end == start {
		return num
	}
	return num[:end]
}

func indexBlueprints(exps map[int]ctExpansion, bps []ctBlueprint) *blueprintIndex {
	if bps == nil {
		return nil
	}
	ix := &blueprintIndex{plain: map[bpKey][]ctBlueprint{}, versioned: map[bpKey][]ctBlueprint{}, byBase: map[bpKey][]ctBlueprint{}, allByBase: map[string][]ctBlueprint{}, byCM: map[int][]ctBlueprint{}}
	for _, b := range bps {
		for _, cm := range nonZero(b.CardMarketIDs) {
			ix.byCM[cm] = append(ix.byCM[cm], b)
		}
		exp, ok := exps[b.ExpansionID]
		if !ok {
			continue
		}
		num := strings.ToUpper(strings.TrimSpace(b.FixedProperties.CollectorNumber))
		if num == "" {
			continue
		}
		key := bpKey{strings.ToLower(exp.Code), num}
		bk := bpKey{key.Exp, variantBase(num)}
		ix.byBase[bk] = append(ix.byBase[bk], b)
		ix.allByBase[bk.Number] = append(ix.allByBase[bk.Number], b)
		if marksVariant(b.Version) {
			ix.versioned[key] = append(ix.versioned[key], b)
		} else {
			ix.plain[key] = append(ix.plain[key], b)
		}
	}
	return ix
}

// ourNumber is the collector number as CardTrader prints it for our card:
// One Piece uses the full code ("OP01-025"); Riftbound the bare number with
// its variant letter ("007A"), and "S" for the signed showcases our fetcher
// writes with a star ("299*" -> "299S").
func ourNumber(game string, card ourCard) string {
	num := strings.ToUpper(card.Number)
	if game == "onepiece" {
		return strings.ToUpper(card.Set) + "-" + num
	}
	if game == "riftbound" {
		num = strings.ReplaceAll(num, "*", "S")
	}
	return num
}

// numberNamesPrinting reports whether the collector number alone already
// says which printing the card is, so a version word on the blueprint is
// redundant rather than a sign of another printing. True for Riftbound,
// where alt arts are "007a" and showcases are the 299+ numbers of the set.
func numberNamesPrinting(game string, card ourCard) bool {
	if game != "riftbound" {
		return false
	}
	n := card.Number
	if strings.ContainsAny(n, "abcdefghijklmnopqrstuvwxyz*") {
		return true
	}
	// Showcases are numbered past the set's size, which our id carries as
	// its last part: "sfd-222-221" is card 222 of a 221-card set.
	var v int
	if _, err := fmt.Sscanf(n, "%d", &v); err == nil && v > card.setSize {
		return true
	}
	return false
}

// resolveBlueprint applies rule A then rule B. It returns the product, or
// the candidates when the card is a variant or the language cannot be
// told, or nothing when no blueprint carries the number.
func resolveBlueprint(game, id string, card ourCard, ix *blueprintIndex, shelf map[int]shelfLang, catalog map[int]cmProduct) (product int, cands []int) {
	num := ourNumber(game, card)
	var plain, versioned []ctBlueprint
	for _, exp := range homeExpansions(game, card.Set) {
		plain = append(plain, ix.plain[bpKey{exp, num}]...)
		versioned = append(versioned, ix.versioned[bpKey{exp, num}]...)
	}
	if numberNamesPrinting(game, card) {
		// Everything on this exact number is this printing.
		plain = append(plain, versioned...)
		versioned = nil
	}
	// A number with exactly one blueprint in its home expansion, whatever
	// its version says, is that printing: a set printed foil-only ("EB-01
	// Memorial Collection", version "Foil"), a special-numbered promo
	// ("VEN SP1", version "Crystal Rose Alternate Art"). There is nothing
	// else on the number the version could be telling apart from.
	if len(plain) == 0 && len(versioned) == 1 {
		plain, versioned = versioned, nil
	}
	isVariant := strings.Contains(id, "_")

	if !isVariant && len(plain) > 1 {
		// Several blueprints with no variant word on one number (One Piece
		// files a "Special Rare" print of EB03-003 next to the plain one):
		// the blueprint with an empty version is the plain card.
		var bare []ctBlueprint
		for _, b := range plain {
			if strings.TrimSpace(b.Version) == "" {
				bare = append(bare, b)
			}
		}
		if len(bare) == 1 {
			versioned = append(versioned, plain...)
			plain = bare
		}
	}

	if !isVariant && len(plain) == 1 {
		ids := nonZero(plain[0].CardMarketIDs)
		if len(ids) == 0 {
			return 0, nil
		}
		if p, ok := pickShelf(ids, shelf, catalog); ok {
			return p, nil
		}
		return 0, ids
	}
	// A variant, or a number several plain blueprints share: every printing
	// of the base card in the home expansions is a candidate, listed for
	// the tie-break.
	pool := append(plain, versioned...)
	if isVariant {
		// Every printing of the base card, from every expansion: a parallel
		// of OP01-025 may sit on a Premium Card Collection or reprint shelf
		// rather than Romance Dawn's, and only the artwork tells which.
		pool = ix.allByBase[variantBase(num)]
	}
	seen := map[int]bool{}
	for _, b := range pool {
		for _, cm := range nonZero(b.CardMarketIDs) {
			if !seen[cm] {
				seen[cm] = true
				cands = append(cands, cm)
			}
		}
	}
	sort.Ints(cands)
	return 0, cands
}

// englishOnly keeps the ids whose Cardmarket expansion is an English shelf.
func englishOnly(ids []int, shelf map[int]shelfLang, catalog map[int]cmProduct) []int {
	var out []int
	for _, id := range ids {
		p, ok := catalog[id]
		if !ok {
			continue
		}
		if shelf[p.IDExpansion] == shelfEnglish {
			out = append(out, id)
		}
	}
	return out
}

// pickShelf is rule B on the Cardmarket ids of one printing, one per
// shelf: the id on the English shelf. Two English shelves (EB-03: the
// normal run and an "Asia Region Legal" run that reads as English) are
// told apart by which sells more plain English sealed products; a tie, or
// ids all on unknown shelves, is not settled. A lone id is taken as is,
// whatever its shelf (reported under ForeignShelf when it is not English).
func pickShelf(ids []int, shelf map[int]shelfLang, catalog map[int]cmProduct) (int, bool) {
	ids = nonZero(ids)
	if len(ids) == 1 {
		return ids[0], true
	}
	en := englishOnly(ids, shelf, catalog)
	if len(en) == 1 {
		return en[0], true
	}
	best, bestN, tie := 0, -1, false
	for _, id := range en {
		n := shelfEnglishSealed[catalog[id].IDExpansion]
		switch {
		case n > bestN:
			best, bestN, tie = id, n, false
		case n == bestN:
			tie = true
		}
	}
	if len(en) > 1 && !tie && bestN > 0 {
		return best, true
	}
	return 0, false
}

// visualShelf applies rule B to a visual pick. The tie-break compares
// artworks, and the shelves of one printing share theirs, so its pick is
// whichever shelf of the printing sorted first: the product is the English
// shelf of the picked product's blueprint.
func visualShelf(product int, ix *blueprintIndex, shelf map[int]shelfLang, catalog map[int]cmProduct) (int, bool) {
	bps := ix.byCM[product]
	if len(bps) != 1 {
		return 0, false
	}
	return pickShelf(bps[0].CardMarketIDs, shelf, catalog)
}

// nonEnglishTwins finds, for every mapped One Piece card, the same printing
// on the set's non-English shelf: the other Cardmarket id of the product's
// blueprint that sits on a foreign shelf and carries the very same product
// name. Exactly one, or none. A printing never listed on a second shelf
// (anniversary sets, most promos) has no twin, and the app prices its
// foreign copies on the English product.
func nonEnglishTwins(r *gameResult, ix *blueprintIndex, shelf map[int]shelfLang, catalog map[int]cmProduct) {
	if ix == nil {
		return
	}
	r.NonEnglish = map[string]int{}
	for id, m := range r.Mapped {
		bps := ix.byCM[m.Product]
		if len(bps) != 1 {
			continue
		}
		var twins []int
		for _, cm := range nonZero(bps[0].CardMarketIDs) {
			p, ok := catalog[cm]
			if cm == m.Product || !ok || shelf[p.IDExpansion] != shelfForeign || p.Name != catalog[m.Product].Name {
				continue
			}
			twins = append(twins, cm)
		}
		if len(twins) == 1 {
			r.NonEnglish[id] = twins[0]
		}
	}
}

func nonZero(ids []int) []int {
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id != 0 {
			out = append(out, id)
		}
	}
	return out
}
