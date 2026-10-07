// Card id → Cardmarket product id, for every game, built once here and
// shipped in the model bundle (metadata/cardmarket.json) so the app can
// price a collection from Cardmarket's public daily price guides without
// any account. Design: docs/superpowers/specs/2026-10-07-cardmarket-prices-design.md.
//
// Resolution per card, in this order:
//  1. published id  — TCGdex (Pokémon), LorcanaJSON (Lorcana)
//  2. CardTrader    — the one blueprint on the card's (set, number), when it
//     lists one Cardmarket id (needs CARDTRADER_TOKEN)
//  3. overrides.json — hand-made picks for what the above cannot settle
//
// then every product is checked against today's Cardmarket catalog (exists,
// name agrees). Anything else is listed, never guessed.
//
// Usage:
//
//	go run .                      # all four games, write cardmarket.json
//	go run . -games lorcana       # one game (the others keep their entries)
//	go run . -offline             # rebuild from cache/ without the network
//	go run . -retry-missing       # ask TCGdex again for ids it had no product for
//	go run . -list-games          # print CardTrader's game ids (to fill cardTraderGames)
//	go run . -report report.txt   # also write the full lists to a file
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	outFile       = "cardmarket.json"
	overridesFile = "overrides.json"
)

var allGames = []string{"pokemon", "onepiece", "lorcana", "riftbound"}

// mapping is the committed output: one map for every game. games carries
// Cardmarket's game ids so the app knows which price guides to fetch.
type mapping struct {
	Version int            `json:"version"`
	BuiltAt string         `json:"built_at"`
	Games   map[string]int `json:"games"`
	Cards   map[string]int `json:"cards"`
	// CardsNonEnglish is the same printing on Cardmarket's non-English
	// shelf, for the One Piece cards that have one: the product a copy in
	// another language is priced on. Absent: the app uses Cards.
	CardsNonEnglish map[string]int `json:"cards_non_english"`
}

func loadMapping() (mapping, error) {
	m := mapping{Version: 1, Games: map[string]int{}, Cards: map[string]int{}, CardsNonEnglish: map[string]int{}}
	data, err := os.ReadFile(outFile)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w", outFile, err)
	}
	if m.Cards == nil {
		m.Cards = map[string]int{}
	}
	if m.Games == nil {
		m.Games = map[string]int{}
	}
	if m.CardsNonEnglish == nil {
		m.CardsNonEnglish = map[string]int{}
	}
	return m, nil
}

func loadOverrides() (map[string]int, error) {
	o := map[string]int{}
	data, err := os.ReadFile(overridesFile)
	if errors.Is(err, fs.ErrNotExist) {
		return o, nil
	}
	if err != nil {
		return nil, err
	}
	raw := map[string]any{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", overridesFile, err)
	}
	// Keys starting with "_" are comments (the reason next to each pick).
	for k, v := range raw {
		if strings.HasPrefix(k, "_") {
			continue
		}
		f, ok := v.(float64)
		if !ok || f <= 0 || f != float64(int(f)) {
			return nil, fmt.Errorf("%s: %q must be a positive product id", overridesFile, k)
		}
		o[k] = int(f)
	}
	return o, nil
}

func main() {
	gamesFlag := flag.String("games", strings.Join(allGames, ","), "comma-separated games to build")
	offline := flag.Bool("offline", false, "rebuild from cache/ without downloading")
	retryMissing := flag.Bool("retry-missing", false, "ask TCGdex again for cards it had no Cardmarket product for")
	listGames := flag.Bool("list-games", false, "print CardTrader's game list and exit")
	reportPath := flag.String("report", "", "write the full per-card report to this file")
	flag.Parse()

	if *listGames {
		games, err := cardTraderGamesList(*offline)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		for _, g := range games {
			fmt.Printf("%4d  %s\n", g.ID, g.Name)
		}
		return
	}

	m, err := loadMapping()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	overrides, err := loadOverrides()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	_, hasToken := os.LookupEnv("CARDTRADER_TOKEN")
	if !hasToken && !*offline {
		fmt.Println("CARDTRADER_TOKEN not set: CardTrader step skipped (One Piece and Riftbound will stay unresolved)")
	}

	var report strings.Builder
	failed := false
	for _, game := range strings.Split(*gamesFlag, ",") {
		game = strings.TrimSpace(game)
		if _, ok := cmGames[game]; !ok {
			fmt.Printf("unknown game %q\n", game)
			os.Exit(2)
		}
		fmt.Printf("%s\n", game)
		res, err := buildGame(game, overrides, *offline, *retryMissing, hasToken)
		if err != nil {
			fmt.Printf("  %v\n", err)
			failed = true
			continue
		}
		// Replace this game's entries wholesale: a card that lost its
		// product today must not keep yesterday's.
		for id := range m.Cards {
			if gameOfID(id, game) {
				delete(m.Cards, id)
			}
		}
		for id := range m.CardsNonEnglish {
			if gameOfID(id, game) {
				delete(m.CardsNonEnglish, id)
			}
		}
		for id, r := range res.Mapped {
			m.Cards[id] = r.Product
		}
		for id, p := range res.NonEnglish {
			m.CardsNonEnglish[id] = p
		}
		m.Games[game] = cmGames[game]
		printSummary(res, &report)
	}
	m.BuiltAt = time.Now().UTC().Format("2006-01-02")
	if err := writeJSON(outFile, m); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("%s written: %d cards, %d with a non-English twin\n", outFile, len(m.Cards), len(m.CardsNonEnglish))
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, []byte(report.String()), 0o644); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Printf("report written to %s\n", *reportPath)
	}
	if failed {
		os.Exit(1)
	}
}

// gameOfID tells whether an id belongs to game, from the id shapes the
// fetchers produce (the mapping is one map, so games must be told apart
// when one is rebuilt). Riftbound: lower-case set, three dashes ("ogn-001-298").
// Lorcana: digits or a known promo set, dash, number ("1-1", "P1-4").
// One Piece: upper-case letters+digits set, dash, three digits ("OP01-025").
// Pokémon: everything else TCGdex-shaped ("sv01-020", "base1-1").
func gameOfID(id, game string) bool {
	switch game {
	case "riftbound":
		return strings.Count(id, "-") == 2 && id == strings.ToLower(id)
	case "onepiece":
		set, rest, ok := strings.Cut(id, "-")
		return ok && set == strings.ToUpper(set) && len(rest) >= 3 && rest[0] >= '0' && rest[0] <= '9' &&
			!strings.ContainsAny(set, "0123456789") || isOnePieceSet(set)
	case "lorcana":
		set, _, ok := strings.Cut(id, "-")
		return ok && isLorcanaSet(set)
	case "pokemon":
		return !gameOfID(id, "riftbound") && !gameOfID(id, "onepiece") && !gameOfID(id, "lorcana")
	}
	return false
}

func isOnePieceSet(set string) bool {
	return set == "P" || strings.HasPrefix(set, "OP") || strings.HasPrefix(set, "ST") || strings.HasPrefix(set, "EB") || strings.HasPrefix(set, "PRB")
}

func isLorcanaSet(set string) bool {
	if _, err := fmt.Sscanf(set, "%d", new(int)); err == nil && !strings.ContainsAny(set, "abcdefghijklmnopqrstuvwxyz") {
		return true
	}
	switch set {
	case "P1", "P2", "P3", "P4", "C1", "C2", "D23", "DIS", "PD1", "Q1", "Q2", "Coconut", "cp", "CC1":
		return true
	}
	return false
}

func buildGame(game string, overrides map[string]int, offline, retryMissing, hasToken bool) (gameResult, error) {
	cards, err := loadOurCards(game)
	if err != nil {
		return gameResult{}, err
	}
	catalog, day, err := loadCardmarketCatalog(game, offline)
	if err != nil {
		return gameResult{}, err
	}
	shelf, err := loadShelfLanguages(game, offline, catalog)
	if err != nil {
		return gameResult{}, err
	}

	var published map[string]int
	switch game {
	case "pokemon":
		ids := make([]string, 0, len(cards))
		for id := range cards {
			if !isPocket(id) {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		published, err = tcgdexIDs(ids, offline, retryMissing)
	case "lorcana":
		published, err = lorcanaJSONIDs(offline)
	}
	if err != nil {
		return gameResult{}, err
	}

	var exps map[int]ctExpansion
	var bps []ctBlueprint
	if hasToken || offline {
		if _, ok := cardTraderGames[game]; ok {
			exps, bps, err = cardTraderBlueprints(game, offline)
			if err != nil {
				fmt.Printf("  cardtrader: %v (step skipped)\n", err)
				exps, bps = nil, nil
			}
		}
	}

	visual, err := loadVisual()
	if err != nil {
		return gameResult{}, err
	}
	gameOverrides := map[string]int{}
	for id, p := range overrides {
		if _, ok := cards[id]; ok {
			gameOverrides[id] = p
		}
	}
	return resolveGame(game, cards, published, exps, bps, visual, gameOverrides, catalog, shelf, day), nil
}

func printSummary(r gameResult, report *strings.Builder) {
	bySource := map[source]int{}
	for _, m := range r.Mapped {
		bySource[m.Source]++
	}
	total := len(r.Mapped) + len(r.Dropped) + len(r.Ambiguous) + len(r.Unresolved)
	if r.Game == "onepiece" {
		total += len(r.ForeignShelf) // shipped under cards_non_english only
	}
	pct := 0.0
	if total > 0 {
		pct = 100 * float64(len(r.Mapped)) / float64(total)
	}
	fmt.Printf("  catalog of %s\n", strings.TrimSpace(strings.Split(r.CatalogDay, "T")[0]))
	fmt.Printf("  mapped %d/%d (%.1f%%): published %d, blueprint %d, visual %d, override %d\n",
		len(r.Mapped), total, pct, bySource[srcPublished], bySource[srcBlueprint], bySource[srcVisual], bySource[srcOverride])
	fmt.Printf("  dropped by validation %d, ambiguous %d, unresolved %d", len(r.Dropped), len(r.Ambiguous), len(r.Unresolved))
	if r.Skipped > 0 {
		fmt.Printf(", skipped (Pocket) %d", r.Skipped)
	}
	fmt.Println()
	switch {
	case r.Game == "onepiece":
		fmt.Printf("  non-English twins %d, non-English shelf only %d\n", len(r.NonEnglish)-len(r.ForeignShelf), len(r.ForeignShelf))
	case len(r.ForeignShelf) > 0:
		fmt.Printf("  mapped on a foreign shelf %d (see -report)\n", len(r.ForeignShelf))
	}

	fmt.Fprintf(report, "== %s (catalog %s)\n", r.Game, r.CatalogDay)
	dropped := make([]string, 0, len(r.Dropped))
	for id := range r.Dropped {
		dropped = append(dropped, id)
	}
	sort.Strings(dropped)
	for _, id := range dropped {
		fmt.Fprintf(report, "dropped    %-16s %s\n", id, r.Dropped[id])
	}
	for _, a := range r.Ambiguous {
		fmt.Fprintf(report, "ambiguous  %-16s candidates %v\n", a.Card, a.Candidates)
	}
	for _, id := range r.Unresolved {
		fmt.Fprintf(report, "unresolved %s\n", id)
	}
	for _, id := range r.ForeignShelf {
		p, note := r.Mapped[id].Product, "kept"
		if r.Game == "onepiece" {
			p, note = r.NonEnglish[id], "shipped as non-English only"
		}
		fmt.Fprintf(report, "foreign    %-16s product %d sits on a non-English shelf, no English id listed: %s\n", id, p, note)
	}
	fmt.Fprintln(report)

	// The console gets a taste; the file gets everything.
	show := func(label string, ids []string) {
		if len(ids) == 0 {
			return
		}
		n := len(ids)
		if n > 8 {
			ids = ids[:8]
		}
		fmt.Printf("  %s: %s", label, strings.Join(ids, " "))
		if n > 8 {
			fmt.Printf(" … (%d, see -report)", n)
		}
		fmt.Println()
	}
	show("dropped", dropped)
	amb := make([]string, 0, len(r.Ambiguous))
	for _, a := range r.Ambiguous {
		amb = append(amb, a.Card)
	}
	show("ambiguous", amb)
	show("unresolved", r.Unresolved)
}
