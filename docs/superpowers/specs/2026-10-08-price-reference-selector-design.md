# Price reference selector — design

Date: 2026-10-08. Follows the Cardmarket prices design
(`2026-10-07-cardmarket-prices-design.md`) and the same-day work on the
mapper (rule C, the review tool, `manual.json`).

## Problem

The mapper resolves a card id to one Cardmarket product. 316 One Piece ids
cannot be resolved by any data we have: they are *identical twins*, printings
that share one artwork and differ only by a treatment the images do not show
(Jolly Roger Foil vs Textured Foil, foil vs non-foil of one reprint, a
pre-release stamp). The official site carries no field for the treatment,
CardTrader's labels cannot be tied to our `_pN` numbering, and the scanner
cannot tell the twins apart either (identical reference images). Only a
person holding the physical card can.

Today those ids ship without a product and the app shows no price. The
owner's attempt to settle them by eye in the review tool failed for the
reason above (10 of the first 59 picks were wrong the same way).

## Decision

Move the decision to where the information exists: the owner, in the app,
with the card in hand and the prices on screen.

- The **scan duplicate selector** (variant choice on identical artwork)
  stays what it is: "which card id is this?".
- A separate **price reference selector** answers "which Cardmarket product
  is this id?", once per card id, from the candidates the mapper ships.
- A card id without a product shows a small warning in the collection
  views: *No Cardmarket reference, set it by hand* when candidates exist,
  *Not on Cardmarket* when none do.
- The choice is stored per card id (not per copy). It gives the id a
  meaning in the owner's collection; to keep that meaning consistent, the
  duplicate selector shows the chosen label and price next to each twin id,
  so the owner picks the id that matches the card in hand.

Delivery order, decided by the owner: (1) the warning badge, (2) the price
reference selector, (3) the label in the duplicate selector.

## Non-goals

- Per-copy product choice (a copy keeps its id, finish and language; the
  product follows the id).
- Free-text product ids. A card with no candidates stays unpriced.
- Any change to how the mapper resolves what it can resolve; rule C, the
  manual picks and the tie-breaks stay in front of this feature.

## Data

### `cardmarket.json` (bundle, `metadata/cardmarket.json`)

Version stays 1; one optional field is added, absent for games or builds
without it. Readers must treat a missing field as empty.

```json
"candidates": {
  "OP01-006_p3": [
    {"product": 799298, "non_english": 799299, "label": "Full Art (OP01-006f) · PRB-01: The Best",
     "image": "https://cardtrader.com/uploads/blueprints/image/283722/full-art-otama.jpg"},
    {"product": 799296, "non_english": 799297, "label": "Jolly Roger Foil (OP01-006j) · PRB-01: The Best",
     "image": "https://cardtrader.com/uploads/blueprints/image/283723/jolly-roger-otama.jpg"}
  ]
}
```

- One entry per *printing* (CardTrader blueprint) among the card's
  candidate products: `product` is the English shelf's id chosen by rule B
  (`visualShelf`), `non_english` the same printing's non-English twin
  (0 when none), `label` is `<version or "Plain"> (<collector number>) ·
  <CardTrader expansion name>`, `image` the full URL of the blueprint's
  original scan (`https://cardtrader.com` + the blueprint's `image.url`;
  470×656 or 868×1213; a public URL, no API token involved; "" when the
  blueprint has none). A product on no blueprint gets
  `{"product": p, "non_english": 0, "label": "product p", "image": ""}`.
- The images are not shipped: they are CardTrader's scans, loaded by the
  app on demand (see below). Unlike the official images, they show the
  treatment (a Jolly Roger foil pattern is visible), which is what the
  owner needs to match the card in hand.
- Only ids that are **ambiguous** in the mapper's result (several candidate
  products, nothing settled) get an entry. Resolved ids never do; unresolved
  ids with no candidate never do.
- `cards` and `cards_non_english` are unchanged. An id is in `cards` or in
  `candidates`, never both.

### App database

```sql
CREATE TABLE IF NOT EXISTS price_refs (
  card_id     TEXT PRIMARY KEY,
  product     INTEGER NOT NULL,
  non_english INTEGER NOT NULL DEFAULT 0,
  label       TEXT NOT NULL DEFAULT '',
  chosen_at   TEXT NOT NULL
);
```

Added by `migrate` like the other `IF NOT EXISTS` statements. Deleting the
row clears the choice. The table survives bundle updates: a choice whose
product is no longer in the day's guide is simply unpriced until re-chosen
(the selector then shows it as stale).

## Mapper (`scripts/fetch/cardmarket`)

1. **Pixel tie-break**, `pixel_tiebreak.py` (stdlib + numpy + Pillow, runs on
   Windows, no GPU): for every ambiguous card of the game in the report,
   Pearson correlation between our reference image and each candidate
   printing's CardTrader original (`cache/images/`, shared with the review
   tool), both grayscale 200×280. Writes `visual.json` in the format the
   mapper already reads (`floor` 0.7, `margin` 0.15, `picks` {id: {product,
   score, lead}}), with one extra rule: two ids of one base card in one pack
   that pick the same printing are both dropped (identical twins). Writes
   `pixel_status.json` {id: "settled" | "tied" | "twin" | "low"} for the
   review tool. Measured 2026-10-08: 491 settled, 271 tied, 45 twin, 73 low
   of 880. `visual_tiebreak.py` (embedding model, WSL) stays as an
   alternative and is not run.
2. **Candidates in the output**: `writeJSON` adds `candidates` built from
   `gameResult.Ambiguous`, grouped by blueprint through `ix.byCM`, shelf by
   `visualShelf`/`pickShelf`, twin by the same rule as `nonEnglishTwins`,
   label from the blueprint's version, collector number and the expansion's
   name. The summary prints `candidates shipped N`.
3. **Review tool**: reads `pixel_status.json` when present; the queue lists
   "low" cards first, then the rest, each with a status badge ("twin: no
   image can tell these apart"). Nothing else changes.

## App, Go side

- `priceMapping` gains `Candidates map[string][]PriceCandidate` (`product`,
  `non_english`, `label`).
- `cardPrice(m, cardID)` consults the user's reference first: with a
  `price_refs` row, the product and non-English twin come from the row;
  else from `cards` / `cards_non_english` as today.
- `CardPrice` gains `Ref bool` (priced through the owner's reference) and
  `Candidates int` (number of selectable printings when the id has no
  product and no reference). `GetCardPrices` fills both. An id with
  `Candidates == 0` and no price is simply absent from Cardmarket.
- New bindings:
  - `GetPriceCandidates(cardID) ([]PriceCandidateView, error)`: the
    candidates with today's price per column (`CardPrice`) and a
    `Chosen bool`.
  - `SetPriceRef(cardID string, product int) error`: product must be one of
    the id's candidates; stores the row with its label and twin.
  - `ClearPriceRef(cardID string) error`.
  - `GetPriceRefs() (map[string]PriceRef, error)` for the duplicate
    selector's labels (step 3).
  - `GetPriceCandidateImage(url string) (string, error)`: the candidate's
    scan as a data URL, like `GetCardImage` for our own images. Fetched
    once with the app's User-Agent and cached as
    `./prices/images/<sha1 of the url>.jpg` (next to the price guides, so
    the same housekeeping applies); only `https://cardtrader.com/uploads/`
    URLs are accepted; a failure returns "" and the dialog shows a "no
    image" box. Fetched one at a time, when the dialog opens.
- `GetGameValues` and the Home stats need no change: they go through
  `cardPrice`.
- `prices:updated` is emitted after a set/clear so open views refresh.

## App, frontend

1. **Badge** (`CollectionScreen` grid tile, `CardCopiesDialog` header): when
   `GetCardPrices` returns no price for the id, show a small warning chip:
   "No Cardmarket reference" with the hint "set it by hand" when
   `Candidates > 0`, "Not on Cardmarket" when 0. Chip style from the
   Nocturne tokens (warn colour), no new UI kit.
2. **Price reference dialog** (`PriceReferenceDialog`, opened from a "Set
   reference" action next to the chip in the copies dialog; the grid tile
   is itself a button, so its chip only informs): our
   reference image and the id on the left; on the right one tile per
   candidate with CardTrader's scan (loaded through
   `GetPriceCandidateImage`, a "no image" box meanwhile or on failure),
   the label, the trend price (foil column too when present) and a
   "chosen" mark; a "None" action that clears the reference. Picking a
   tile calls `SetPriceRef`, closes, and the views refresh on
   `prices:updated`. Keyboard: digits pick a tile, Escape closes. The
   layout is the review tool's, with the card in hand and the prices on top.
3. **Duplicate selector label** (`VariantChoiceDialog`): under each
   candidate id, when the id has a price reference, its label and today's
   price ("Jolly Roger Foil · 15.20 €"); when the id has candidates but no
   reference, "price reference not set". Data from `GetPriceRefs` and
   `GetCardPrices` for the dialog's ids, fetched when it opens.

Strings are English, like the rest of the UI.

## Testing

- Mapper: Go tests for the candidate emission (grouping by blueprint,
  shelf, twin, label; an id never in both `cards` and `candidates`); Python
  tests for the pixel tie-break's twin rule and status file.
- App Go: tests for `cardPrice` precedence (reference over bundle, stale
  reference unpriced), `SetPriceRef` rejecting a product outside the
  candidates, migration idempotence.
- Frontend: `tsc` and `vite build`; live check under `wails dev` with a
  `models/dev/metadata/cardmarket.json` carrying candidates, on the three
  deliveries in order.

## Rollout

Mapper first (tie-break, candidates, repackage), then the app deliveries 1,
2, 3 as separate commits. The owner's remaining manual work: the 73 "low"
cards in the review tool, at their pace.
