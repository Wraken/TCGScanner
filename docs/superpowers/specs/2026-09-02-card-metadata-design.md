# Card Metadata — Design

Date: 2026-09-02
Status: approved in chat (pending user review of this document)

## Context

The app knows nothing about a card beyond its id, its game (resolved from
`models/tcg/images/manifest.json`) and the copies the user owns. The card
details dialog (`CardCopiesDialog`) therefore shows a bare id. The user wants
real card metadata there. Decisions made in chat:

- **Common core only** for now: the same fields for every game; per-game
  extras (HP, cost, effect text…) maybe later.
- **Details dialog only** as the display surface for now (not the scanner,
  grid tiles, or search).
- Metadata is **English** (matches the UI and the English card ids).

## Data format

One JSON file per game, shipped in the model bundle at
`models/tcg/metadata/<game>.json` (NOT under `images/` — with the planned
distribution split, `images/` becomes optional per-game packs while metadata
travels in the mandatory core; see the distribution design memory). Keyed by
card id, same ids as the index / manifest:

```json
{
  "OP01-025": {
    "name": "Roronoa Zoro",
    "set": "OP01",
    "set_name": "Romance Dawn",
    "number": "025",
    "rarity": "SR",
    "type": "Character",
    "colors": ["Red"]
  }
}
```

Field rules:

| field      | meaning                                   | when unknown |
|------------|-------------------------------------------|--------------|
| `name`     | card name (English)                       | `""`         |
| `set`      | set code as printed / used in the id      | `""`         |
| `set_name` | human set name                            | `""`         |
| `number`   | collector number within the set (string) | `""`         |
| `rarity`   | source's rarity label, untranslated        | `""`         |
| `type`     | card category (Character, Pokémon, Item…) | `""`         |
| `colors`   | colours / inks / energy types, as labels  | `[]`         |

Unknown keys are ignored by the loader (forward-compatible with per-game
extras later). Files are sorted by key, 1-space indent, like the manifest.

## Sources (the real work — one per game)

Each fetcher writes `scripts/fetch/<game>/cards.json` in the format above,
resumable/incremental like the image fetchers. These files are committed
(canonical, reproducible, a few MB total).

- **pokemon** (TCGdex): the per-set listing only carries id/name/image;
  rarity, category and types need `GET /v2/en/cards/<id>` per card
  (~21.8k requests at the fetcher's 80 ms pacing ≈ 30 min, cached per card
  so re-runs are instant). `set` = TCGdex set id (`sv01`), `set_name` from
  the set list, `number` = `localId`, `type` = `category`, `colors` =
  `types`.
- **lorcana** (Lorcast): the per-set card list already carries `rarity`,
  `ink`, `type`; add them to the fetcher struct. `set` = set code,
  `number` = `collector_number`, `colors` = `[ink]`.
- **onepiece** (vegapull-records): new small fetcher reading the English
  dataset of github.com/Coko7/vegapull-records (fields: id, name, rarity,
  category, colors, …). `set` = id prefix before `-`, `set_name` from the
  pack list, `number` = id suffix, `type` = `category`. Ids must match the
  index labels including `_p1`-style alt-art suffixes; the fetcher reports
  index labels it cannot map.
- **riftbound**: `parsedcards.json` only has id/publicCode/imageUrl. The
  gallery JS bundle (see the fetcher README) also carries name, rarity,
  domain and type; the manual DevTools extraction step is extended to keep
  those fields. Until the user redoes that extraction, the riftbound
  `cards.json` may be missing or partial — the app copes (see below).

`package_app_model.py` gains `--metadata GAME=FILE` (defaults mirroring
`--images`), copies each file to `models/tcg/metadata/<game>.json`, and
prints a coverage line per game: index labels of that game with no metadata
entry (a warning, never a failure).

## Go

`card_metadata.go`:

```go
type CardInfo struct {
    ID      string   `json:"id"`
    Game    string   `json:"game"`
    Name    string   `json:"name"`
    Set     string   `json:"set"`
    SetName string   `json:"set_name"`
    Number  string   `json:"number"`
    Rarity  string   `json:"rarity"`
    Type    string   `json:"type"`
    Colors  []string `json:"colors"`
}

func modelMetadataDir(modelName string) string            // ./models/<model>/metadata
func cardMetadata(metadataDir, game string) map[string]CardInfo  // mtime-cached per file, empty map when absent
func lookupCardInfo(imagesDirs, metadataDirs []string, cardID string) (CardInfo, error)
```

`lookupCardInfo` resolves the game through the image manifests first
(`gameResolver`), reads only that game's file, and falls back to scanning
every metadata file present when the id is not in any manifest. Not found →
error. `Game` and `ID` are filled by the loader, never read from the file.

Binding: `(a *App) GetCardInfo(cardID string) (CardInfo, error)` over
`allModelImagesDirs()` / `allModelMetadataDirs()`.

## Frontend

`CollectionScreen` fetches `GetCardInfo(openCard)` when a card dialog opens
(cached per card id for the screen's lifetime; errors → `null`).
`CardCopiesDialog` gets a new prop `info: CardInfo | null`:

- With info: title = `name` (or the id when `name` is empty); the id in
  mono becomes a subtitle; under it a row of neutral `n-tag`s: `set_name`
  (with `set` in mono), `#number`, `rarity`, `type`, one tag per colour.
  Empty fields are skipped.
- Without info (unknown card, missing file): exactly today's layout.

Types: `CardInfo` added to `types.ts` mirroring the JSON tags.

## Edge cases

- Metadata dir absent (older bundle): every lookup errors, dialog unchanged.
- Card known to the index but missing from its game's file: dialog
  unchanged; packager already warned at build time.
- Alt-art ids (`_p1`, `-star-`): the fetchers are responsible for emitting
  them explicitly; the app does NOT canonicalise ids for metadata lookup
  (an alt art may have its own rarity).
- Hostile `game` from the manifest is impossible (the manifest is our own
  output and `manifestGameSegment` already rejects `.`/`..`).

## Testing

- Go: `cardMetadata` against a temp file (load, mtime reload, unknown
  keys ignored, absent file → empty map); `lookupCardInfo` with a temp
  manifest + two metadata files (manifest path, fallback scan, not found);
  real-bundle smoke test skipped when `models/tcg/metadata` is absent.
- Packager: coverage warning exercised by a unit test on a tiny fixture.
- Frontend: `tsc` + `vite build`; manual pass in `wails dev`.

## Out of scope

Per-game extras, metadata on the scanner / grid / search, translations,
prices.
