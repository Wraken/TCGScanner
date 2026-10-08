package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// How a card got its product, for the report. The order is the resolution
// order: a later step only runs for cards the earlier ones left unresolved.
type source string

const (
	srcPublished source = "published" // TCGdex / LorcanaJSON id, taken as is
	srcBlueprint source = "blueprint" // one CardTrader blueprint, one Cardmarket id
	srcVisual    source = "visual"    // visual.json, the embedding tie-break
	srcPack      source = "pack"      // rule C: the one print of its kind in the pack's expansion
	srcManual    source = "manual"    // manual.json, the review tool's hand picks
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
	// ManualNone lists the cards the review tool settled as "no candidate
	// matches": unresolved by verdict, not for want of a source.
	ManualNone map[string]bool
	// PackDisagree lists the manual picks rule C would have settled
	// otherwise (card -> the pack rule's product): a cross-check, the
	// manual pick still ships.
	PackDisagree map[string]int
	// Candidates are the selectable printings of every ambiguous card,
	// shipped in cardmarket.json for the app's price reference selector
	// (spec 2026-10-08-price-reference-selector-design.md).
	Candidates map[string][]candidateView
}

// resolveGame runs the steps of the spec for one game. published holds the
// ids of step 1 (nil for games without such a source), blueprints the
// CardTrader export of step 2 (nil when the token is absent), overrides the
// hand-made picks. The catalog is the day's product list: every product
// must exist in it and carry a compatible name, or the card is dropped.
func resolveGame(game string, cards map[string]ourCard, published map[string]int,
	exps map[int]ctExpansion, blueprints []ctBlueprint, visual map[string]visualPick, manual manualPicks, overrides map[string]int,
	catalog map[int]cmProduct, shelf map[int]shelfLang, catalogDay string) gameResult {

	r := gameResult{Game: game, CatalogDay: catalogDay, Mapped: map[string]resolved{}, Dropped: map[string]string{}, ManualNone: map[string]bool{}, PackDisagree: map[string]int{}, Candidates: map[string][]candidateView{}}

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
	kin := kinCounts(game, cards)

	for _, id := range ids {
		card := cards[id]
		var pick resolved
		var cands []int

		switch {
		case overrides[id] != 0:
			pick = resolved{overrides[id], srcOverride}
		case manual.None[id]:
			r.ManualNone[id] = true
			r.Unresolved = append(r.Unresolved, id)
			continue
		case manual.Picks[id] != 0:
			// A hand pick names a printing CardTrader lists, by any of its
			// products; rule B chooses the shelf, as for a visual pick.
			p, ok := 0, false
			if ix != nil {
				p, ok = visualShelf(manual.Picks[id], ix, shelf, catalog)
			}
			if !ok {
				r.Dropped[id] = fmt.Sprintf("manual pick %d is on no blueprint, or its printing has no English shelf", manual.Picks[id])
				continue
			}
			pick = resolved{p, srcManual}
			if p2, src2, _ := resolveBlueprint(game, id, card, ix, shelf, catalog, kin); src2 == srcPack && p2 != p {
				r.PackDisagree[id] = p2
			}
		case published[id] != 0:
			pick = resolved{published[id], srcPublished}
		case ix != nil:
			var product int
			var src source
			product, src, cands = resolveBlueprint(game, id, card, ix, shelf, catalog, kin)
			if product != 0 {
				pick = resolved{product, src}
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
				r.Candidates[id] = candidateViews(cands, ix, exps, shelf, catalog)
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
	// Every blueprint by (set label of its expansion, base number): the
	// candidate pool of a variant whose pack is known (rule C).
	byPackBase map[bpKey][]ctBlueprint
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
	ix := &blueprintIndex{plain: map[bpKey][]ctBlueprint{}, versioned: map[bpKey][]ctBlueprint{}, byBase: map[bpKey][]ctBlueprint{}, allByBase: map[string][]ctBlueprint{}, byCM: map[int][]ctBlueprint{}, byPackBase: map[bpKey][]ctBlueprint{}}
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
		if lab := expansionLabel(exp); lab != "" {
			pk := bpKey{lab, bk.Number}
			ix.byPackBase[pk] = append(ix.byPackBase[pk], b)
		}
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
func resolveBlueprint(game, id string, card ourCard, ix *blueprintIndex, shelf map[int]shelfLang, catalog map[int]cmProduct, kin map[kinKey]int) (product int, src source, cands []int) {
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
			return 0, "", nil
		}
		if p, ok := pickShelf(ids, shelf, catalog); ok {
			return p, srcBlueprint, nil
		}
		return 0, "", ids
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
		// Rule C: the pack names the expansion; see packLabels.
		if labels := packLabels(card.Pack); labels != nil {
			var mine []ctBlueprint
			parallel := strings.Contains(id, "_p")
			for _, l := range labels {
				for _, b := range ix.byPackBase[bpKey{l, variantBase(num)}] {
					if isVariantPrinting(b) == parallel {
						mine = append(mine, b)
					}
				}
			}
			if len(mine) == 1 && kin[kinKey{variantBase(num), strings.ToUpper(card.Pack), parallel}] == 1 {
				if p, ok := pickShelf(nonZero(mine[0].CardMarketIDs), shelf, catalog); ok {
					return p, srcPack, nil
				}
			}
			if len(mine) > 0 {
				pool = mine
			}
		}
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
	return 0, "", cands
}

// ── Rule C: the pack names the expansion ─────────────────────────────────
//
// A One Piece reprint ("_r1") or parallel ("_p1") carries in our metadata
// the product it comes from (ourCard.Pack, from punk-records): "PRB-02",
// "ST-34", "OP-01", the combined "OP14-EB04", "PROMO". CardTrader files its
// blueprints by expansion, and the expansion's name starts with the same
// label (the code is less reliable: "st04" is "ST-05"). So the candidates
// of a variant are the blueprints of its pack's expansion on its base
// number: the reprint is the one without a variant marking (no letter after
// the number, no version word beyond "Reprint" / "Non-Foil" / errata), the
// parallels are the marked ones. When the pack lists exactly one print of
// the kind and we have exactly one variant of the kind in that pack, they
// are the same printing (srcPack). Otherwise the prints of the kind are
// the candidates, listed for the review tool. Measured 2026-10-08 on the
// 2,064 ambiguous One Piece cards: 1,137 settled, the rest narrowed to 4.8
// candidates on average (6.7 before).

var (
	setLabelRe  = regexp.MustCompile(`^[ ]*(OP|ST|EB|PRB)[ ]*-?[ ]*0*([0-9]+)`)
	cleanCodeRe = regexp.MustCompile(`^(op|st|eb|prb)-?0*([0-9]+)$`)
	packRe      = regexp.MustCompile(`^(OP|ST|EB|PRB)-?0*([0-9]+)(?:-(EB)-?0*([0-9]+))?$`)
)

func normLabel(prefix, number string) string {
	n, _ := strconv.Atoi(number)
	return fmt.Sprintf("%s-%02d", strings.ToUpper(prefix), n)
}

// expansionLabel is the set label of a CardTrader expansion ("ST-05"),
// from its name when the code is a plain set code (else the code), "PROMO"
// for the promo expansion, "" for a stamped or special run ("op05p1st",
// "op02p") and for the products no pack of ours names.
func expansionLabel(e ctExpansion) string {
	code := strings.ToLower(strings.TrimSpace(e.Code))
	if code == "promo" {
		return "PROMO"
	}
	cm := cleanCodeRe.FindStringSubmatch(code)
	if cm == nil {
		return ""
	}
	if m := setLabelRe.FindStringSubmatch(e.Name); m != nil {
		return normLabel(m[1], m[2])
	}
	return normLabel(cm[1], cm[2])
}

// packLabels are the expansion labels a pack of ours spans: one, or two for
// the combined "OP14-EB04" boosters; nil when the pack is unknown.
func packLabels(pack string) []string {
	pack = strings.ToUpper(strings.TrimSpace(pack))
	if pack == "" {
		return nil
	}
	if pack == "PROMO" {
		return []string{"PROMO"}
	}
	m := packRe.FindStringSubmatch(pack)
	if m == nil {
		return []string{pack}
	}
	out := []string{normLabel(m[1], m[2])}
	if m[3] != "" {
		out = append(out, normLabel(m[3], m[4]))
	}
	return out
}

// reprintWords mark a plain reprint (or a rarity) on CardTrader, not
// another printing; they are blanked before the variant words are looked
// for. "Special Rare" is a printing of its own and is tested first.
var reprintWords = []string{"non-foil", "reprint", "missing pen symbol next to the artist name", "errata", "fixed text", "1st print", "first print", "secret rare", "super rare", "uncommon", "common", "rare", "leader"}

// isVariantPrinting reports whether a blueprint is a parallel / special
// print rather than the plain card or its plain reprint: a letter after
// the number ("EB01-009f"), "Special Rare", or a variant word in the version.
func isVariantPrinting(b ctBlueprint) bool {
	num := strings.ToUpper(strings.TrimSpace(b.FixedProperties.CollectorNumber))
	if num != "" && variantBase(num) != num {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(b.Version))
	if strings.Contains(v, "special rare") {
		return true
	}
	for _, w := range reprintWords {
		v = strings.ReplaceAll(v, w, "")
	}
	return marksVariant(v)
}

// kinKey counts our variants of one kind (parallel or reprint) of one base
// card in one pack: rule C settles a print only when both sides count one.
type kinKey struct {
	Base, Pack string
	Parallel   bool
}

func kinCounts(game string, cards map[string]ourCard) map[kinKey]int {
	out := map[kinKey]int{}
	for id, c := range cards {
		if !strings.Contains(id, "_") || c.Pack == "" {
			continue
		}
		out[kinKey{variantBase(ourNumber(game, c)), strings.ToUpper(c.Pack), strings.Contains(id, "_p")}]++
	}
	return out
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
		if twin := twinOf(m.Product, bps[0], shelf, catalog); twin != 0 {
			r.NonEnglish[id] = twin
		}
	}
}

// twinOf is the same printing on a foreign shelf: the one other Cardmarket
// id of the blueprint that sits on a foreign shelf and carries the very
// same product name; 0 when there is none or several.
func twinOf(product int, b ctBlueprint, shelf map[int]shelfLang, catalog map[int]cmProduct) int {
	var twins []int
	for _, cm := range nonZero(b.CardMarketIDs) {
		p, ok := catalog[cm]
		if cm == product || !ok || shelf[p.IDExpansion] != shelfForeign || p.Name != catalog[product].Name {
			continue
		}
		twins = append(twins, cm)
	}
	if len(twins) == 1 {
		return twins[0]
	}
	return 0
}

// candidateView is one selectable printing of an ambiguous card: the
// product the app prices on (the English shelf), its non-English twin, a
// label a person can read, and CardTrader's scan to look at.
type candidateView struct {
	Product    int    `json:"product"`
	NonEnglish int    `json:"non_english"`
	Label      string `json:"label"`
	Image      string `json:"image"`
}

const cardTraderSite = "https://cardtrader.com"

// candidateViews groups a card's candidate products by printing (blueprint),
// in the candidates' order. A product no blueprint lists is a view of its own.
func candidateViews(cands []int, ix *blueprintIndex, exps map[int]ctExpansion, shelf map[int]shelfLang, catalog map[int]cmProduct) []candidateView {
	var out []candidateView
	seenBP, seenP := map[int]bool{}, map[int]bool{}
	for _, p := range cands {
		var bps []ctBlueprint
		if ix != nil {
			bps = ix.byCM[p]
		}
		if len(bps) != 1 {
			if !seenP[p] {
				seenP[p] = true
				out = append(out, candidateView{Product: p, Label: fmt.Sprintf("product %d", p)})
			}
			continue
		}
		b := bps[0]
		if seenBP[b.ID] {
			continue
		}
		seenBP[b.ID] = true
		product, ok := pickShelf(nonZero(b.CardMarketIDs), shelf, catalog)
		if !ok {
			product = p
		}
		version := strings.TrimSpace(b.Version)
		if version == "" {
			version = "Plain"
		}
		label := fmt.Sprintf("%s (%s)", version, strings.TrimSpace(b.FixedProperties.CollectorNumber))
		if e, ok := exps[b.ExpansionID]; ok && e.Name != "" {
			label += " · " + e.Name
		}
		image := strings.TrimSpace(b.Image.URL)
		if image != "" && !strings.HasPrefix(image, "http") {
			image = cardTraderSite + image
		}
		out = append(out, candidateView{Product: product, NonEnglish: twinOf(product, b, shelf, catalog), Label: label, Image: image})
	}
	return out
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
