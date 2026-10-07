# Cardmarket prices — Design

Date: 2026-10-07
Status: approved in chat (pending user review of this document)

## Context

Cardmarket publishes, every night, one public price guide per game (no
account, no API key): `https://downloads.s3.cardmarket.com/productCatalog/priceGuide/price_guide_<game>.json`.
Each row is keyed by `idProduct` and carries `trend`, `low`, `avg`, the 1/7/30
day averages, and the same columns again with a `-foil` suffix. A product is
one printing of a card; a card and its foil are two columns of the same row.

The app knows cards by their own ids (`OP01-025`, `OP01-025_p1`, `1-1`,
`ogn-001-298`, `sv01-020`). Nothing in Cardmarket's files carries those ids,
and the product list alone (name + expansion) cannot resolve them: measured on
2026-10-03, name matching resolves 75 % of Lorcana, 59 % of Riftbound, 8 % of
Pokémon and 2 % of One Piece, where up to 20 products share one code
(`Monkey.D.Luffy (OP05-119)` spans 2.52 € to 11 712 €). A guessed match can be
wrong by three orders of magnitude.

Decisions made in chat (2026-10-07):

- **Every card gets a correct product, not just the easy ones.** The mapping
  is built once, by us, from sources that publish the exact Cardmarket id per
  printing, and ships in the model bundle. Users need no account and no key.
- **Price shown: `trend`** (`trend-foil` for foil copies). Other columns are
  kept in the cache but not surfaced.
- **Surfaces: card details dialog, collection grid (per card + total of the
  view), Home (total, and per game on the tiles).** Not the Scan screen.
- **Refresh: automatic, once a day, can be turned off.** The app stays usable
  offline with the last cache.
- Prices are per product and, for Pokémon, Lorcana and Riftbound,
  **aggregated over every printed language**: Cardmarket computes `trend`
  from all listings of the product, so there is no "English price" in these
  files, only a blend that English listings usually dominate. The app shows
  one price per card whatever the copy's language and says so in the source
  line. Per-language prices for these games stay out of scope: they only
  exist behind Cardmarket's authenticated API, which no longer accepts
  applications.
- **One Piece copies are priced by language** (user, 2026-10-07). Cardmarket
  sells every One Piece set on two shelves, English and non-English (the
  second one labelled "(Non-English)" up to OP-13 and "(Asia Region Legal)"
  from OP-14 on), with separate products and prices: Luffy OP05-119 Secret
  Rare trends at 8.33 € on one and 4.56 € on the other. The copy's `lang`
  column (stored since 2026-09-07) picks the shelf: `en` reads `cards`,
  anything else reads `cards_non_english` and falls back to `cards` when the
  printing has no second shelf.
- **Pokémon TCG Pocket ids are excluded** (user, 2026-10-07): mobile-only
  cards, never priced, never counted.

## Part 1 — The mapping table (pipeline side)

### Sources with exact ids

| Game | Source | Field | Coverage measured (2026-10-03) |
|---|---|---|---|
| Pokémon | TCGdex `GET /v2/en/cards/<id>` (already our fetcher) | `pricing.cardmarket.idProduct` | 171/200 sampled; every miss but 15 was a TCG Pocket id (see below) |
| Lorcana | LorcanaJSON `allCards.json` | `externalLinks.cardmarketId` | 3 148 / 3 192 (98.6 %); 44 left: the five Reign of Jafar Panorama prints (`8-1f`…, a foil-only second print with its own product, hand-mapped from CardTrader in `overrides.json`), "Coconut", newest C2 promos |
| One Piece | CardTrader `GET /blueprints/export?expansion_id=` | `card_market_ids[]` | to measure (needs the key) |
| Riftbound | CardTrader (same) | `card_market_ids[]` | to measure |

CardTrader needs a free account and a bearer token; the token is read from
`CARDTRADER_TOKEN` by the build script and never stored in the repo. It is
rate-limited to 200 requests per 10 s; one request per expansion is enough.
CardTrader also covers Pokémon and Lorcana, so it doubles as the fallback for
their leftovers.

### Resolution order, per card

1. **Published id**: TCGdex for Pokémon, LorcanaJSON for Lorcana. Taken as is.
2. **CardTrader blueprint** (plain cards): the one blueprint of the set's
   home expansion with exactly the card's collector number and no variant
   word in its version. Riftbound numbers name the printing themselves
   (`007a`, showcases past the set size, `299*`), so the exact number
   resolves every Riftbound card.
   A One Piece blueprint lists one Cardmarket id per language shelf
   (Cardmarket sells every set twice: "Romance Dawn" and "Romance Dawn
   (Non-English)", same metacard, different prices); the English one is
   chosen by reading the shelf's language off its sealed products in
   `products_nonsingles` (found 2026-10-07; dates do not tell the twins
   apart). The non-English id of the same blueprint, when it carries the
   same product name, ships as the card's `cards_non_english` entry; a
   printing CardTrader lists on the non-English shelf only (8 promos on
   2026-10-07) ships there alone, so its English copies have no price
   rather than a wrong one. Pre-release and tournament packs are event
   shelves: never English, never a twin.
3. **Visual tie-break** (One Piece `_pN` / `_rN` printings): our numbered
   printings and CardTrader's lettered ones have no mapping. Every
   blueprint on the base code, from every expansion, is a candidate; the
   candidates' preview images are embedded with our own model and compared
   to the card's reference image, and the nearest wins only if it clears
   the lock margin from `config.json`. These printings differ precisely by
   their artwork, which is what the model separates. Runs on the training
   PC; its picks ship in `visual.json` with their score and lead, and the
   builder re-checks each against the thresholds before using it.
4. **Overrides**: `scripts/fetch/cardmarket/overrides.json`, versioned, card
   id → product id, for what the above cannot settle. Wins over everything.
5. Otherwise the card has **no entry**. Never a guess.

Two further refusals, both found on the first Pokémon run: a product two
differently-named cards claim ("Mew" / "Mewtwo" on one TCGdex id) ships for
neither; and TCGdex ids shifted for whole expansions (`ex4`, trainer kits)
are caught by the name check and left to CardTrader.

### Validation against the day's catalog

Every resolved id must exist in that game's `products_singles_<game>.json`
of the same day, and the product name must contain the card name (after the
same normalisation the name matcher uses). A failed check drops the entry
and lists it in the report; it never ships. (A first name-only prototype
mapped Lorcana promos to the wrong products; this check is what would have
caught it.)

### Output

`scripts/fetch/cardmarket/cardmarket.json`, committed, one map for all
games:

```json
{
  "version": 1,
  "built_at": "2026-10-07",
  "games": { "pokemon": 6, "onepiece": 18, "lorcana": 19, "riftbound": 22 },
  "cards": {
    "OP05-119":    747876,
    "1-1":         727081,
    "sv01-020":    702316
  },
  "cards_non_english": {
    "OP05-119":    733461
  }
}
```

`games` carries the Cardmarket game ids the app needs to fetch the right
guides. `cards` is sparse: a card absent from it has no price.
`cards_non_english` holds, for One Piece, the same printing on the
non-English shelf (2,185 entries for 2,742 cards on 2026-10-07); it is read
for copies whose `lang` is not `en`.

Measured on 2026-10-07 (before the visual step): Lorcana 3,153 / 3,192,
Pokémon 19,557 / 21,065 physical cards, Riftbound 1,187 / 1,189, One Piece
2,742 / 4,843 with 2,064 printings waiting on the visual step and 8 promos
on the non-English shelf only.

`package_app_model.py` copies it to `models/<model>/metadata/cardmarket.json`
(core of the bundle, next to `<game>.json`) and reports coverage per game
against the index labels, like it does for metadata. **The build fails the
coverage gate when a game drops below its previous run**, so a regression
in a source cannot ship silently.

### Report

The build script prints, per game: resolved by source, by tie-break, by
override, dropped by validation, unresolved (ids listed to a file). The goal
is 100 % of physical cards; the unresolved list is the work queue for
`overrides.json`.

### Pokémon TCG Pocket

`scripts/fetch/pokemon/cards.json` holds 2 480 ids of the mobile game
(`A1-…`, `A2a-…`, `B1-…`, `P-A-…`). They are not printed cards and will
never have a price; the script skips them and does not count them in the
coverage. Whether they are also in the recognition index is to check on the
training PC; if so, dropping them from the index is a separate change.

## Part 2 — Prices in the app (Go side)

### Cache

`./prices/` next to `collection.db`:

- `price_guide_<game>.json` as downloaded (2 – 16 MB each);
- `state.json`: per game, `fetched_at` (UTC), `etag`, and whether the last
  attempt failed.

Downloads use `If-None-Match`; a `304` costs one round-trip and no transfer.
A download replaces its file atomically (temp file + rename), so a cut
connection never leaves a half-written guide.

### Refresh

On startup, after the database, for every game in the mapping's `games`: if
no file, or `fetched_at` older than 24 h, fetch in the background. Failures
are logged, keep the old file, and set the failed flag; the UI shows the
prices it has with their age. A manual **Refresh** action re-runs the same
routine; an `enabled` toggle (default on) is persisted in a new `settings`
table (`key TEXT PRIMARY KEY, value TEXT`). There is no settings screen
yet, so both live on Home's prices line ("Refresh", "Turn off" / "Turn
on"); they move to Settings → Prices when that screen exists. When
disabled, nothing is fetched and the cache is left as is. The backend
emits a `prices:updated` event after every background refresh, and the
frontend reloads its prices on it.

The app sends nothing but the GET; the only data leaving the machine is the
request itself. `PRODUCT.md`'s "local first" line gains this one, named,
optional exception.

### Lookup

`prices.go` loads each guide once into `map[idProduct]PriceRow` and the
mapping once (`cards` and `cards_non_english`, both `map[cardID]idProduct`;
re-read when the file's mtime changes, like the metadata). Lookups are
in-memory:

```go
type CardPrice struct {
    Trend     float64 `json:"trend"`      // normal finish, EUR
    TrendFoil float64 `json:"trend_foil"` // 0 when the row has none
    // The same printing on Cardmarket's non-English shelf (One Piece);
    // 0 when the card has none.
    TrendNonEnglish     float64 `json:"trend_non_english"`
    TrendNonEnglishFoil float64 `json:"trend_non_english_foil"`
    AsOf      string  `json:"as_of"`      // guide's createdAt, ISO date
}
func (a *App) GetCardPrices(cardIDs []string) (map[string]CardPrice, error)
func (a *App) GetPricesStatus() (PricesStatus, error) // enabled, per game: as_of, error
func (a *App) RefreshPrices() error
func (a *App) SetPricesEnabled(bool) error
```

`value(copy)`: the non-English pair when the copy's `lang` is not `en` and
`TrendNonEnglish` is set, else the English pair; within the pair,
`TrendFoil` when `foil` and the row has a foil price, else `Trend`. A card
with neither product, or whose products have no `trend`, is simply absent
from the returned map; a card present in `cards_non_english` alone has an
empty English pair, so its English copies count as unpriced.

### Totals

Computed where the cards already are: `GetCards(filter)` returns the copies,
the frontend sums `value(copy)` over the map. Home's per-game totals need one
new query: `GetGameValues()` sums over all copies per game, in Go, from the
same maps (a few thousand lookups; no SQL change).

## Part 3 — Display (frontend)

Format: `Intl.NumberFormat("en", { style: "currency", currency: "EUR" })`,
so "€2.32" today and the right French form once the UI is localised. Values
under 0.01 show as "<€0.01". Nothing is shown for a card without a price;
totals state how many cards they cover when it is not all of them:
"€1,204.50 · 108 of 110 cards priced".

- **Card details dialog**: under the tags, "Trend €2.32 · foil €35.36", then
  "Your 4 copies: €41.32" (each copy valued by its finish and language).
  A One Piece card with a non-English product adds "· other languages
  €4.56". Source line in small type: "Cardmarket trend, all languages ·
  7 Oct" (One Piece: "Cardmarket trend, English · 7 Oct").
- **Collection grid**: the trend price at the right of the card id, tabular
  numerals; foil-only copies show the foil price. The footer line gains the
  view's total.
- **Home**: a fourth stat, "Collection value"; each game tile shows its
  value under the card count.
- **Status**: when prices are disabled, off, or stale by more than 3 days, a
  single quiet line on Home ("Prices off" / "Prices from 3 Oct · Refresh").
  Never a blocking dialog.

Copy and layout follow DESIGN.md: numbers in Mist 400, no accent on prices,
monospace nowhere (Inter with `tabular-nums`).

## Not in scope

- Per-language prices (source has none).
- Price history, charts, alerts.
- TCGplayer / USD.
- Sealed products.
- Pokémon Pocket cleanup of the index.

## Verification

- Build script on all four games with the key set: report shows 100 % of
  physical cards resolved or listed; validation step drops nothing silently.
- Unit tests: mapping load, guide parse, `value(copy)` for normal/foil/none
  and for an `en` / `ja` copy of a One Piece card with and without a
  non-English product, staleness rule, atomic replace on a simulated cut.
- App: cold start offline (no prices, no error), first online start (fetch,
  then prices appear without reload), toggle off (no network call, checked
  with a logging round-tripper), stale cache (age shown).
- Frontend: the three surfaces at 1024×640 and 1920×1080 with mock data;
  totals with partial coverage; a 5-figure value does not wrap the tile.
