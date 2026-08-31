# Collections by Game — Design

Date: 2026-08-31
Status: approved (pending user review of this document)

## Context

Today the DB has two tables: `collections` (anonymous scan sessions: `id, name
DEFAULT '', started_at`) and `cards` (one row per physical copy: `id,
collection_id NOT NULL FK, card_id, foil, model_name, scanned_at`). Every card
belongs to exactly one collection, the app creates/resumes a collection at
startup, and no notion of game exists anywhere — even though
`models/<model>/images/manifest.json` already maps all 28k card ids to
`<game>/<id>.webp`.

This blocked the Nocturne redesign of the Collection screens: the frontend
needs per-game views.

## Decisions (made with the user)

1. **Game collections are derived, not stored.** A card always counts in its
   game's collection; the game is resolved from the manifest, so per-game
   counters can never drift. Custom collections are groupings on top.
2. **Scan flow:** "Save" just adds the copy (game auto-resolved). An optional,
   persistent "also add to: <custom>" selector assigns the copy to a custom
   collection; it defaults to none and resets to none on app restart.
3. **One custom max per copy:** `cards.collection_id` becomes nullable and
   references custom collections only. Upgrade path to a join table exists if
   multi-membership (real decks) is ever needed.

## Schema (v2)

```sql
CREATE TABLE collections (            -- custom collections only
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE cards (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    collection_id INTEGER REFERENCES collections(id) ON DELETE SET NULL,
    card_id       TEXT    NOT NULL,
    game          TEXT    NOT NULL DEFAULT '',
    foil          INTEGER NOT NULL DEFAULT 0,
    model_name    TEXT    NOT NULL DEFAULT '',
    scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

- One row per physical copy (unchanged). Normal/foil aggregation stays at
  display/export time.
- `game` is denormalized on purpose: resolved once at save time, the DB stays
  coherent even if the manifest changes later.
- `game = ''` means "unknown" and is shown as an "Autres" bucket in the UI
  (only when non-empty).

## Migration (in `openDB`, idempotent like the existing one)

Detect v1 via `PRAGMA table_info(cards)`: `game` column missing or
`collection_id` marked NOT NULL. Then, in one transaction:

1. **Rebuild `cards`** (SQLite cannot relax NOT NULL in place): create the v2
   table, `INSERT ... SELECT` with `game = ''`, drop old, rename.
2. **Rename `collections.started_at` → `created_at`** via the same rebuild
   trick (or `ALTER TABLE ... RENAME COLUMN`, supported by modernc/SQLite).
3. **Delete empty unnamed collections** (auto-created sessions):
   `DELETE FROM collections WHERE name = '' AND id NOT IN (SELECT DISTINCT
   collection_id FROM cards WHERE collection_id IS NOT NULL)`.
4. **Name the remaining unnamed collections** (they hold cards and become
   regular customs): `UPDATE collections SET name = 'Collection ' || id
   WHERE name = ''`.
5. **Backfill `game`:** for every distinct `card_id` with `game = ''`, resolve
   through the merged manifests of all dirs under `./models/*/images/`
   (a plain file read via `cardImageManifest`, no model loading). Unresolved
   ids stay `''`.

Backfill runs on every startup for rows with `game = ''` (cheap: one SELECT
DISTINCT + targeted UPDATEs), so cards scanned before a game's images were
installed heal themselves later.

## Game resolution

`gameOfCard(cardID, modelName) string` in `card_images.go`: look up the
manifest entry (`<game>/<id>.webp`) through the existing mtime-cached
`cardImageManifest`, return the path prefix before `/`. Flat entries or
missing ids return `''`.

## Go API (Wails bindings)

Removed: `NewCollection` (anonymous), `GetCurrentCollectionID`,
`SetCollection`, `GetCollectionCards`, startup collection create/resume.

Changed / new:

```go
// scan flow
SaveCard(cardID string, foil bool) error      // resolves game; inserts with activeCustomID or NULL
SetActiveCustom(id int)                       // 0 = none; in-memory only, resets on restart
GetActiveCustom() int

// views
GetGameCollections() ([]GameCollection, error)   // {game, card_count}; union of manifest games
                                                 // and DB games, zero counts included,
                                                 // '' bucket only when non-empty
GetCustomCollections() ([]Collection, error)     // {id, name, created_at, card_count}
GetCards(f CardFilter) ([]CollectionCard, error) // exactly one of Game / CollectionID set, else error

// custom collection CRUD
NewCustomCollection(name string) (Collection, error) // name required, trimmed, non-empty
RenameCollection(id int, name string) error
DeleteCollection(id int) error                       // cards keep game, collection_id -> NULL

// copies
MoveCardToCustom(rowID int, customID int) error  // 0 removes from custom
UpdateCard(card CollectionCard) error            // re-resolves game when card_id changed
DeleteCard(id int) error                         // unchanged

// export
ExportCSV(f CardFilter, config ExportConfig) (string, error) // same CSV format, filtered rows
```

```go
type GameCollection struct {
    Game      string `json:"game"`
    CardCount int    `json:"card_count"`
}
type CardFilter struct {
    Game         string `json:"game,omitempty"`          // "" is NOT a valid game filter…
    OtherBucket  bool   `json:"other_bucket,omitempty"`  // …use this to select game = ''
    CollectionID int    `json:"collection_id,omitempty"`
}
```

`CollectionCard` gains `game string`.

## Frontend

- **Scanner:** the collection bar is replaced by a persistent "Ajouter aussi
  à : *aucune* ▾" selector (custom collections + "aucune"). Saving shows which
  game the copy was filed under.
- **Home:** the placeholder stat tiles become real — one tile per game with
  its count, plus the custom collections list.
- **Collection screen (Nocturne):** navigation by game + customs; grid view
  with images (manifest), per-card normal/foil aggregates computed client-side
  from the per-copy rows, delete copy, move copy to a custom, CSV export from
  any view.
- Game display names/icons live in a frontend map: `onepiece` → "One Piece",
  `riftbound` → "Riftbound", `lorcana` → "Lorcana", `pokemon` → "Pokémon",
  `''` → "Autres". Unknown keys fall back to the raw key.

## Edge cases

- Save with no model loaded: error (unchanged).
- Manual card-id override to an id absent from the manifest: saved with
  `game = ''`, appears in "Autres"; healed by the startup backfill if the id
  becomes known later.
- Deleting a custom never deletes copies (FK `ON DELETE SET NULL`).
- `GetCards` with zero or two filter fields set: error, never "all cards".

## Testing

Go:
- migration test: build a v1 DB (old schema SQL + rows) in a temp file, open
  it, assert rebuilt schema, named/deleted collections, nullable FK behavior;
- backfill + `gameOfCard` against the real `models/tcg` manifest (skip when
  absent, like `TestLoadCardImageRealModel`);
- custom CRUD, `MoveCardToCustom`, `UpdateCard` game re-resolution;
- `GetCards` filter validation and results;
- `ExportCSV` filtered output.

Frontend: `tsc --noEmit` + manual pass in `wails dev`.

## Out of scope

- Multi-custom membership (join table) — documented upgrade path only.
- Quantity columns (row-per-copy stays).
- Price data, per-game export presets, collection screens beyond the views
  described above.
