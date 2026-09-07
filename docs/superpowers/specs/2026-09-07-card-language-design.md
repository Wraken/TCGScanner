# Card language — design

**Date:** 2026-09-07
**Status:** approved design, pending implementation plan

## Goal

Record the printed language of every scanned copy (English, French, Japanese…)
so the collection can later be priced per language, and so exports can split
counts by language. The language is *declared by the user at scan time*: the
matcher recognises the artwork, which is identical across languages, so
nothing in the pipeline knows what language a physical card is.

## Decisions (user, 2026-09-07)

- One short list of languages common to all games: `en, fr, de, es, it, pt,
  ja, ko, zh` (ISO 639-1, lowercase). No per-game lists.
- The scan-time selector is *sticky*: like "Also add to", the chosen language
  stays selected from one scan to the next and survives screen changes. One
  scans a batch in one language.
- Scope of this version: storage + scan selector + per-copy edit in the
  details dialog + a "Language" CSV field. No filtering in the grid, no
  per-language counts on tiles, no detection.

## Data model

`cards` gains one column:

```
lang TEXT NOT NULL DEFAULT 'en'
```

- `schemaV2`'s `CREATE TABLE IF NOT EXISTS cards` and `rebuildCards`'
  `cards_v2` both declare it (fresh databases and the one-shot v1 rebuild).
- Existing v2 databases get it through the tolerated
  `ALTER TABLE cards ADD COLUMN lang TEXT NOT NULL DEFAULT 'en'` in
  `migrate`, next to the `game` one. Every existing copy therefore becomes
  English, which matches the user's collection. No backfill logic.
- `CollectionCard` gains `Lang string \`json:"lang"\``; `cardColumns`,
  `scanCard`, `insertCard` (new `lang` parameter) and `updateCard` (also
  writes `lang`) follow.

Valid codes live in one Go list:

```go
type CardLanguage struct {
    Code  string `json:"code"`  // "fr"
    Label string `json:"label"` // "French"
}
var cardLanguages = []CardLanguage{{"en", "English"}, {"fr", "French"},
    {"de", "German"}, {"es", "Spanish"}, {"it", "Italian"}, {"pt", "Portuguese"},
    {"ja", "Japanese"}, {"ko", "Korean"}, {"zh", "Chinese"}}
func validLanguage(code string) bool
```

Any binding that stores a language rejects an unknown code with an error
(`unknown language "xx"`); nothing normalises case or aliases.

## Backend API (app.go)

- `ListLanguages() []CardLanguage` — the list above, for the frontend.
- `SetScanLang(code string) error` / `GetScanLang() string` — the sticky
  scan language, held in the `App` struct next to `activeCustomID`
  (`sync.Mutex`-guarded string, or `atomic.Value`); defaults to `"en"` at
  startup, not persisted across runs (a settings table is out of scope).
  `SetScanLang` validates.
- `SaveCard(cardID, foil)` keeps its signature and stamps the copy with the
  current scan language, exactly like it stamps the active custom.
- `UpdateCard(card)` validates `card.Lang` and writes it (a hand-edited
  language in the dialog). The existing rule stays: `Game` is re-resolved
  only when `CardID` changes.
- `GetCards`, `MoveCardToCustom`, `DeleteCard`: unchanged (the new field
  simply travels in `CollectionCard`).

## Scan screen

A "Language" `Select` next to the "Finish" segmented control (two columns in
one row: Finish | Language). Options come from `ListLanguages()` (loaded once
in `useCollection`, like the export presets); the trigger shows the label
("French"). Value = `collection.scanLang`, change = `collection.setScanLang`
(optimistic state + binding call + resync on mount through `GetScanLang`,
mirroring `activeCustomId`). `handleSave` is unchanged. The "Saved" line may
show the code (e.g. `OGN-001/298 · FR`) — optional polish, not required.

## Details dialog (CardCopiesDialog)

Each copy row gets, after the Foil/Normal toggle, a compact language
`Select` (`w-[110px]`, same options) showing the copy's current language and
calling `onChangeLang(copy, code)`, wired in `CollectionScreen` to
`UpdateCard({ ...c, lang })` + `refreshCards()`, like the foil toggle. The
copies list is unchanged otherwise; the grid tile counts still merge every
language.

## CSV export

- New `FieldLanguage ExportField = "language"`, label `Language`, exposed in
  `ExportModal.ALL_FIELDS` (preview value `en`). No new preset: the user
  toggles the field on; the Riftbound preset does not include it (the site's
  import format has no language column).
- `writeCardsCSV` groups by `card_id, lang` **only when** the config
  contains `FieldLanguage`, and then orders by `card_id, lang`; otherwise the
  query is exactly today's `GROUP BY card_id`. Rows carry `lang` ("" when not
  grouped), `resolveField` renders it for `FieldLanguage`.

So a collection with 2 EN + 1 FR of the same card exports one row (3 copies)
without the field, and two rows (`en` 2, `fr` 1) with it.

## Frontend types

- `CollectionCard.lang: string`; `CardLanguage { code, label }`;
  `ExportField` union gains `"language"`.
- `useCollection` gains `languages: CardLanguage[]`, `scanLang: string`,
  `setScanLang(code)`.

## Testing

Go (`go test ./...`):
- `db_migration_test.go`: a v2 database without `lang` gets the column with
  default `en` on every existing row; a fresh database has it; the v1
  rebuild path keeps it.
- `card_test.go`: insert with `fr` reads back `fr`; `updateCard` changes the
  language; `listCards` carries it.
- `app_test.go`: `SetScanLang("xx")` errors and leaves the previous value;
  `SaveCard` stamps the current scan language; `UpdateCard` rejects `"xx"`.
- `export_test.go`: with `FieldLanguage` two rows per language and the
  `Language` header; without it, one row per card, unchanged headers.

Frontend: `tsc --noEmit` clean; live pass in `wails dev` by the user
(select a language, scan, see it in the dialog, change it, export with and
without the field).

## Out of scope (noted for later)

- Persisting the scan language across restarts (settings table).
- Language filter or per-language counts in the collection grid.
- Language detection from the scan (OCR) — the artwork is language-agnostic.
- Per-game language availability (One Piece has no German print run, etc.).
- Price lookups per language (the Cardmarket / TCGCSV idea in the backlog).
