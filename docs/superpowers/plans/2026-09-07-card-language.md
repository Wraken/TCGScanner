# Card Language Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record the printed language of every scanned copy (sticky selector at scan time, editable per copy, exportable as a CSV field).

**Architecture:** One `lang` column on `cards` (default `en`), one Go list of valid languages exposed through a binding, a sticky scan language held in the `App` struct like the active custom collection, and a CSV export that groups by language only when the `Language` field is selected. Spec: `docs/superpowers/specs/2026-09-07-card-language-design.md`.

**Tech Stack:** Go 1.26 + modernc sqlite (backend, `go test ./...`), Wails v2 bindings (`wails generate module`, output `frontend/wailsjs/` is gitignored), React 18 + TypeScript + shadcn `Select` (frontend, `npx tsc --noEmit`).

**Conventions:** UI copy in English. Never commit `models/` or `frontend/wailsjs/`. Commit messages end with the session trailer used by the other commits of the day (see `git log -3`). Branch: `main`.

---

## File map

| File | Responsibility |
|---|---|
| `card_lang.go` (new) | `CardLanguage`, `cardLanguages`, `defaultLanguage`, `validLanguage` |
| `card_lang_test.go` (new) | list sanity + insert/update round trip |
| `db.go` | column in `schemaV2`, tolerated `ALTER`, `rebuildCards` |
| `db_migration_test.go` | lang column present + default on v1 / pre-lang v2 / fresh DBs |
| `card.go` | `CollectionCard.Lang`, columns, `insertCard(..., lang)`, `updateCard` |
| `app.go` | sticky scan language, `ListLanguages`, `SetScanLang`, `GetScanLang`, stamping, validation |
| `app_test.go` | scan-lang binding tests, stub model |
| `export.go` / `export_test.go` | `FieldLanguage`, conditional `GROUP BY card_id, lang` |
| `frontend/src/types.ts` | `lang`, `CardLanguage`, `ExportField` |
| `frontend/src/hooks/useCollection.ts` | `languages`, `scanLang`, `setScanLang` |
| `frontend/src/components/ScanScreen.tsx` | Language select next to Finish |
| `frontend/src/components/CardCopiesDialog.tsx` | per-copy language select |
| `frontend/src/components/CollectionScreen.tsx` | wires `onChangeLang` |
| `frontend/src/components/ExportModal.tsx` | `Language` field + preview |

---

### Task 1: Language list, schema column, card layer

**Files:**
- Create: `card_lang.go`, `card_lang_test.go`
- Modify: `db.go` (schemaV2 cards table, `migrate`, `rebuildCards`), `card.go`, `db_migration_test.go`
- Modify (call sites of `insertCard`, append `, "en"`): `app.go:237`, `app_test.go:10`, `collection_test.go:44,75,103,137,138,139,202,206,222`, `export_test.go:22`

- [ ] **Step 1: Write the failing tests**

Create `card_lang_test.go`:

```go
package main

import "testing"

func TestLanguages(t *testing.T) {
	if !validLanguage(defaultLanguage) {
		t.Fatalf("defaultLanguage %q is not in cardLanguages", defaultLanguage)
	}
	for _, bad := range []string{"", "EN", "xx", "en ", "fr-FR"} {
		if validLanguage(bad) {
			t.Errorf("validLanguage(%q) = true, want false", bad)
		}
	}
	seen := map[string]bool{}
	for _, l := range cardLanguages {
		if l.Code == "" || l.Label == "" || seen[l.Code] {
			t.Errorf("bad or duplicate language entry %+v", l)
		}
		seen[l.Code] = true
	}
}

func TestCardLanguageRoundTrip(t *testing.T) {
	db := testDB(t)
	id, err := insertCard(db, 0, "OP01-025", "onepiece", "tcg", false, "fr")
	if err != nil {
		t.Fatal(err)
	}
	got, err := getCard(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lang != "fr" {
		t.Errorf("lang after insert = %q, want fr", got.Lang)
	}
	got.Lang = "ja"
	if err := updateCard(db, got); err != nil {
		t.Fatal(err)
	}
	list, err := listCards(db, CardFilter{Game: "onepiece"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Lang != "ja" {
		t.Errorf("listCards after update = %+v, want one copy in ja", list)
	}
}
```

Append to `db_migration_test.go`:

```go
// buildV2NoLangDB creates a schema-v2 database from before 2026-09-07: no
// lang column, two copies.
func buildV2NoLangDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v2-nolang.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := `
	CREATE TABLE collections (
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
	INSERT INTO cards (card_id, game) VALUES ('sv01-020', 'pokemon'), ('OP01-025', 'onepiece');
	`
	if _, err := db.Exec(stmts); err != nil {
		t.Fatal(err)
	}
	return path
}

// Every migration path ends with a NOT NULL lang column and existing copies
// defaulting to English.
func TestMigrationAddsLang(t *testing.T) {
	paths := map[string]string{
		"v1":              buildV1DB(t),
		"v2 without lang": buildV2NoLangDB(t),
	}
	for name, path := range paths {
		db, err := openDB(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cols := columnInfo(t, db, "cards")
		if c, ok := cols["lang"]; !ok || !c.notNull {
			t.Errorf("%s: lang column = %+v, want present and NOT NULL", name, c)
		}
		var total, en int
		if err := db.QueryRow(`SELECT COUNT(*), SUM(lang = 'en') FROM cards`).Scan(&total, &en); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if total == 0 || en != total {
			t.Errorf("%s: %d of %d rows default to en", name, en, total)
		}
		db.Close()
	}
}

func TestFreshDBLangDefault(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`INSERT INTO cards (card_id, game) VALUES ('sv01-020', 'pokemon')`); err != nil {
		t.Fatal(err)
	}
	var lang string
	if err := db.QueryRow(`SELECT lang FROM cards`).Scan(&lang); err != nil {
		t.Fatal(err)
	}
	if lang != "en" {
		t.Errorf("fresh DB lang default = %q, want en", lang)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestLanguages|TestCardLanguageRoundTrip|TestMigrationAddsLang|TestFreshDBLangDefault' .`
Expected: build failure (`undefined: validLanguage`, `too many arguments in call to insertCard`).

- [ ] **Step 3: Create `card_lang.go`**

```go
package main

// CardLanguage is one printed language a copy can be recorded in. The list
// is the single source of truth: the frontend fetches it (ListLanguages)
// and every binding that stores a language validates against it.
type CardLanguage struct {
	Code  string `json:"code"`  // ISO 639-1, lowercase
	Label string `json:"label"` // English name shown in the UI
}

// defaultLanguage stamps copies when nothing else was chosen (existing rows
// migrate to it too: the collection was English-only before 2026-09-07).
const defaultLanguage = "en"

var cardLanguages = []CardLanguage{
	{"en", "English"}, {"fr", "French"}, {"de", "German"}, {"es", "Spanish"},
	{"it", "Italian"}, {"pt", "Portuguese"}, {"ja", "Japanese"}, {"ko", "Korean"},
	{"zh", "Chinese"},
}

// validLanguage reports whether code is one of cardLanguages. Exact match:
// no case folding, no aliases — the UI only ever sends codes from the list.
func validLanguage(code string) bool {
	for _, l := range cardLanguages {
		if l.Code == code {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Schema and migration in `db.go`**

In `schemaV2`, the `cards` table gains a line after `foil`:

```sql
		foil          INTEGER NOT NULL DEFAULT 0,
		lang          TEXT    NOT NULL DEFAULT 'en',
		model_name    TEXT    NOT NULL DEFAULT '',
```

In `migrate`, after the `game` ALTER:

```go
	_, _ = db.Exec(`ALTER TABLE cards ADD COLUMN game TEXT NOT NULL DEFAULT ''`)
	// 2026-09-07: printed language of a copy; pre-existing copies are English.
	_, _ = db.Exec(`ALTER TABLE cards ADD COLUMN lang TEXT NOT NULL DEFAULT 'en'`)
```

In `rebuildCards`, the `cards_v2` CREATE gains the same line after `foil`
(the `INSERT ... SELECT` list stays unchanged: on the v1 path every copy is
English and takes the column default):

```sql
			foil          INTEGER NOT NULL DEFAULT 0,
			lang          TEXT    NOT NULL DEFAULT 'en',
			model_name    TEXT    NOT NULL DEFAULT '',
```

- [ ] **Step 5: Card layer in `card.go`**

```go
type CollectionCard struct {
	ID           int    `json:"id"`
	CardID       string `json:"card_id"`
	Game         string `json:"game"`
	CollectionID int    `json:"collection_id"` // 0 = not in a custom collection
	Foil         bool   `json:"foil"`
	Lang         string `json:"lang"` // printed language, ISO 639-1 (see card_lang.go)
	ModelName    string `json:"model_name"`
	ScannedAt    string `json:"scanned_at"`
}
```

```go
func insertCard(db *sql.DB, collectionID int, cardID, game, modelName string, foil bool, lang string) (int, error) {
	foilInt := 0
	if foil {
		foilInt = 1
	}
	res, err := db.Exec(
		`INSERT INTO cards (collection_id, card_id, game, foil, model_name, lang) VALUES (?, ?, ?, ?, ?, ?)`,
		nullableID(collectionID), cardID, game, foilInt, modelName, lang,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}
```

```go
	err := row.Scan(&c.ID, &c.CardID, &c.Game, &cid, &foilInt, &c.Lang, &c.ModelName, &c.ScannedAt)
```

```go
const cardColumns = `id, card_id, game, collection_id, foil, lang, model_name, scanned_at`
```

```go
func updateCard(db *sql.DB, c CollectionCard) error {
	foilInt := 0
	if c.Foil {
		foilInt = 1
	}
	_, err := db.Exec(`UPDATE cards SET foil = ?, card_id = ?, game = ?, lang = ? WHERE id = ?`,
		foilInt, c.CardID, c.Game, c.Lang, c.ID)
	return err
}
```

- [ ] **Step 6: Update every `insertCard` call site**

Append the language argument to each call listed in **Files** above. In
`app.go` `SaveCard` use the constant (Task 2 replaces it with the sticky value):

```go
	id, err := insertCard(a.db, int(a.activeCustomID.Load()), cardID, game, model.Name(), foil, defaultLanguage)
```

In the tests append `, "en"`, e.g. `insertCard(db, 0, "sv01-020", "pokemon", "tcg", false, "en")`.

- [ ] **Step 7: Run the whole suite**

Run: `go vet . && go test .`
Expected: PASS (all migration tests, including the existing v1→v2 ones, plus the four new tests).

- [ ] **Step 8: Commit**

```bash
git add card_lang.go card_lang_test.go db.go card.go app.go db_migration_test.go app_test.go collection_test.go export_test.go
git commit -m "feat: cards carry their printed language (lang column, default en)"
```

---

### Task 2: Sticky scan language bindings

**Files:**
- Modify: `app.go` (App struct, `SaveCard`, `UpdateCard`, new bindings), `app_test.go`

- [ ] **Step 1: Write the failing tests** (append to `app_test.go`)

```go
// stubModel satisfies Predictor for bindings that only need a model name.
type stubModel struct{ name string }

func (s stubModel) Predict(string) (*Prediction, error) { return nil, nil }
func (s stubModel) Name() string                       { return s.name }
func (s stubModel) Delete()                            {}

func TestScanLang(t *testing.T) {
	app := &App{db: testDB(t)}
	if got := app.GetScanLang(); got != "en" {
		t.Errorf("initial scan lang = %q, want en", got)
	}
	if err := app.SetScanLang("xx"); err == nil {
		t.Error("unknown language accepted")
	}
	if got := app.GetScanLang(); got != "en" {
		t.Errorf("scan lang after rejected set = %q, want en", got)
	}
	if err := app.SetScanLang("fr"); err != nil {
		t.Fatal(err)
	}
	if got := app.GetScanLang(); got != "fr" {
		t.Errorf("scan lang = %q, want fr", got)
	}
	if n := len(app.ListLanguages()); n != len(cardLanguages) {
		t.Errorf("ListLanguages returned %d entries, want %d", n, len(cardLanguages))
	}
}

func TestSaveCardStampsScanLang(t *testing.T) {
	app := &App{db: testDB(t), model: stubModel{name: "tcg"}}
	if err := app.SetScanLang("ja"); err != nil {
		t.Fatal(err)
	}
	saved, err := app.SaveCard("OP01-025", false)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Lang != "ja" {
		t.Errorf("saved lang = %q, want ja", saved.Lang)
	}
}

func TestUpdateCardValidatesLang(t *testing.T) {
	app := &App{db: testDB(t)}
	id, err := insertCard(app.db, 0, "sv01-020", "pokemon", "tcg", false, "en")
	if err != nil {
		t.Fatal(err)
	}
	card, err := getCard(app.db, id)
	if err != nil {
		t.Fatal(err)
	}
	card.Lang = "xx"
	if err := app.UpdateCard(card); err == nil {
		t.Error("unknown language accepted by UpdateCard")
	}
	got, _ := getCard(app.db, id)
	if got.Lang != "en" {
		t.Errorf("lang after rejected update = %q, want en", got.Lang)
	}
	card.Lang = "de"
	if err := app.UpdateCard(card); err != nil {
		t.Fatal(err)
	}
	got, _ = getCard(app.db, id)
	if got.Lang != "de" {
		t.Errorf("lang after update = %q, want de", got.Lang)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -run 'TestScanLang|TestSaveCardStampsScanLang|TestUpdateCardValidatesLang' .`
Expected: build failure (`app.GetScanLang undefined`).

- [ ] **Step 3: Implement in `app.go`**

App struct, after `activeCustomID`:

```go
	// Printed language stamped on new scans. Sticky like activeCustomID and
	// in-memory only (resets to defaultLanguage on every start). "" means
	// defaultLanguage. Bound methods run concurrently: guard with the mutex.
	scanLangMu sync.Mutex
	scanLang   string
```

New bindings, next to `SetActiveCustom`/`GetActiveCustom`:

```go
// ListLanguages is the closed list of printed languages a copy can carry.
func (a *App) ListLanguages() []CardLanguage {
	return cardLanguages
}

// SetScanLang chooses the language stamped on the next scans. An unknown
// code is rejected and leaves the current choice untouched.
func (a *App) SetScanLang(code string) error {
	if !validLanguage(code) {
		return fmt.Errorf("unknown language %q", code)
	}
	a.scanLangMu.Lock()
	a.scanLang = code
	a.scanLangMu.Unlock()
	return nil
}

func (a *App) GetScanLang() string {
	a.scanLangMu.Lock()
	defer a.scanLangMu.Unlock()
	if a.scanLang == "" {
		return defaultLanguage
	}
	return a.scanLang
}
```

`SaveCard` stamps the sticky value:

```go
	id, err := insertCard(a.db, int(a.activeCustomID.Load()), cardID, game, model.Name(), foil, a.GetScanLang())
```

`UpdateCard` validates right after `getCard(prev)` succeeds:

```go
	if !validLanguage(card.Lang) {
		return fmt.Errorf("unknown language %q", card.Lang)
	}
```

- [ ] **Step 4: Run the suite**

Run: `go vet . && go test .`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add app.go app_test.go
git commit -m "feat: sticky scan language bindings (ListLanguages, SetScanLang, GetScanLang)"
```

---

### Task 3: CSV export field

**Files:**
- Modify: `export.go`, `export_test.go`

- [ ] **Step 1: Write the failing test** (append to `export_test.go`)

```go
// The Language field switches the aggregation from per card to per card and
// language; without it every language merges into one row, as before.
func TestWriteCardsCSVByLanguage(t *testing.T) {
	db := testDB(t)
	seed := []struct {
		lang string
		foil bool
	}{{"en", false}, {"en", false}, {"fr", false}, {"fr", true}}
	for _, s := range seed {
		if _, err := insertCard(db, 0, "sv01-020", "pokemon", "tcg", s.foil, s.lang); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "out.csv")
	read := func() string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	}

	config := ExportConfig{Fields: []ExportField{FieldCardID, FieldNormalCount, FieldFoilCount}}
	if err := writeCardsCSV(db, CardFilter{Game: "pokemon"}, path, config); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), "Card ID,Normal Count,Foil Count\nsv01-020,3,1"; got != want {
		t.Errorf("without language: csv = %q, want %q", got, want)
	}

	config.Fields = append(config.Fields, FieldLanguage)
	if err := writeCardsCSV(db, CardFilter{Game: "pokemon"}, path, config); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), "Card ID,Normal Count,Foil Count,Language\nsv01-020,2,0,en\nsv01-020,1,1,fr"; got != want {
		t.Errorf("with language: csv = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestWriteCardsCSVByLanguage .`
Expected: build failure (`undefined: FieldLanguage`).

- [ ] **Step 3: Implement in `export.go`**

Add the field and its label:

```go
const (
	FieldCardID      ExportField = "card_id"
	FieldNormalCount ExportField = "normal_count"
	FieldFoilCount   ExportField = "foil_count"
	FieldTotalCount  ExportField = "total_count"
	FieldLanguage    ExportField = "language"
)
```

```go
	case FieldTotalCount:
		return "Total Count"
	case FieldLanguage:
		return "Language"
```

`resolveField` gains the language:

```go
func resolveField(cardID, lang string, normalCount, foilCount int, f ExportField, config ExportConfig) string {
	switch f {
	case FieldCardID:
		return trimCardID(cardID, config.CardIDTrimEnd)
	case FieldNormalCount:
		return strconv.Itoa(normalCount)
	case FieldFoilCount:
		return strconv.Itoa(foilCount)
	case FieldTotalCount:
		return strconv.Itoa(normalCount + foilCount)
	case FieldLanguage:
		return lang
	}
	return ""
}
```

`writeCardsCSV` groups per language only when the field is requested
(import `"slices"`):

```go
	// Per-language rows only when the export shows the language; otherwise
	// every language merges into one row per card, exactly as before.
	byLang := slices.Contains(config.Fields, FieldLanguage)
	groupBy, langCol := "card_id", "''"
	if byLang {
		groupBy, langCol = "card_id, lang", "lang"
	}
	rows, err := db.Query(`
		SELECT card_id, `+langCol+`,
		       SUM(CASE WHEN foil = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN foil = 1 THEN 1 ELSE 0 END)
		FROM cards
		WHERE `+where+`
		GROUP BY `+groupBy+`
		ORDER BY `+groupBy+`
	`, args...)
```

and the row loop:

```go
	for rows.Next() {
		var cardID, lang string
		var normalCount, foilCount int
		if err := rows.Scan(&cardID, &lang, &normalCount, &foilCount); err != nil {
			return err
		}
		row := make([]string, len(config.Fields))
		for i, field := range config.Fields {
			row[i] = resolveField(cardID, lang, normalCount, foilCount, field, config)
		}
		w.Write(row)
	}
```

- [ ] **Step 4: Run the suite**

Run: `go vet . && go test .`
Expected: PASS (`TestWriteCardsCSVFiltered` unchanged and green).

- [ ] **Step 5: Commit**

```bash
git add export.go export_test.go
git commit -m "feat: Language CSV export field, grouped per language when selected"
```

---

### Task 4: Frontend — types, hook, scan screen selector

**Files:**
- Regenerate: `frontend/wailsjs/` via `wails generate module` (run from the repo root; gitignored, never committed)
- Modify: `frontend/src/types.ts`, `frontend/src/hooks/useCollection.ts`, `frontend/src/components/ScanScreen.tsx`

- [ ] **Step 1: Regenerate the bindings**

Run from the repo root: `wails generate module`
Expected: `frontend/wailsjs/go/main/App.d.ts` now declares `ListLanguages`, `SetScanLang`, `GetScanLang` and `frontend/wailsjs/go/models.ts` has `CardLanguage`. (`grep -n "ScanLang\|ListLanguages" frontend/wailsjs/go/main/App.d.ts`.)

- [ ] **Step 2: Types** (`frontend/src/types.ts`)

```ts
export interface CollectionCard {
  id: number;
  card_id: string;
  game: string;
  // Custom collection this copy belongs to; 0 = none.
  collection_id: number;
  foil: boolean;
  // Printed language of this copy, ISO 639-1 ("en", "fr"...). Always one of
  // ListLanguages(); declared by the user at scan time, never detected.
  lang: string;
  model_name: string;
  scanned_at: string;
}

export interface CardLanguage {
  code: string;
  label: string;
}
```

```ts
export type ExportField = "card_id" | "normal_count" | "foil_count" | "total_count" | "language";
```

- [ ] **Step 3: Hook** (`frontend/src/hooks/useCollection.ts`)

Imports: add `GetScanLang`, `ListLanguages`, `SetScanLang` to the binding import (keep alphabetical order) and `CardLanguage` to the type import.

State, after `activeCustomId`:

```ts
  // Printed language stamped on new scans. Sticky and in-memory like
  // activeCustomId: the backend owns the value, this mirrors it.
  const [scanLang, setScanLangState] = useState("en");
  const [languages, setLanguages] = useState<CardLanguage[]>([]);
```

In the mount effect, after `GetActiveCustom()`:

```ts
    ListLanguages()
      .then((l) => setLanguages((l ?? []) as CardLanguage[]))
      .catch(() => {});
    GetScanLang().then(setScanLangState).catch(() => {});
```

New function, after `setActiveCustom`:

```ts
  function setScanLang(code: string) {
    setScanLangState(code);
    SetScanLang(code).catch((e) => LogDebug(`[Collections] setScanLang error: ${e}`));
  }
```

Return object: add `languages`, `scanLang`, `setScanLang` after `setActiveCustom`.

- [ ] **Step 4: Scan screen** (`frontend/src/components/ScanScreen.tsx`)

Replace the whole `{/* Finish */}` block with a two-column row:

```tsx
        {/* Finish + printed language (sticky, see useCollection) */}
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs n-n400 mb-1.5">Finish</label>
            <div className="flex rounded-md overflow-hidden" style={{ border: "1px solid var(--n-divider)" }}>
              <button className="n-seg-opt flex-1" data-active={!pred.foil} onClick={() => pred.setFoil(false)}>
                Normal
              </button>
              <button
                className="n-seg-opt flex-1"
                data-active={pred.foil}
                onClick={() => pred.setFoil(true)}
                style={{ borderLeft: "1px solid var(--n-divider)" }}
              >
                Foil
              </button>
            </div>
          </div>
          <div>
            <label className="block text-xs n-n400 mb-1.5">Language</label>
            <Select value={collection.scanLang} onValueChange={collection.setScanLang}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {collection.languages.map((l) => (
                  <SelectItem key={l.code} value={l.code}>{l.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
```

(`Select*` are already imported in this file.)

- [ ] **Step 5: Type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/types.ts frontend/src/hooks/useCollection.ts frontend/src/components/ScanScreen.tsx
git commit -m "ui: language selector on the scan screen"
```

---

### Task 5: Frontend — per-copy language in the dialog, export field

**Files:**
- Modify: `frontend/src/components/CardCopiesDialog.tsx`, `frontend/src/components/CollectionScreen.tsx`, `frontend/src/components/ExportModal.tsx`

- [ ] **Step 1: Dialog** (`CardCopiesDialog.tsx`)

Types import: `import type { CardInfo, CardLanguage, Collection, CollectionCard } from "@/types";`

Props:

```ts
interface Props {
  cardId: string | null;              // null = closed
  copies: CollectionCard[];           // copies of that card id
  image: string | null;
  info: CardInfo | null;
  customs: Collection[];
  languages: CardLanguage[];
  onClose: () => void;
  onToggleFoil: (copy: CollectionCard) => void;
  onChangeLang: (copy: CollectionCard, code: string) => void;
  onMove: (rowId: number, customId: number) => void;
  onDelete: (rowId: number) => void;
}

export function CardCopiesDialog({
  cardId, copies, image, info, customs, languages, onClose, onToggleFoil, onChangeLang, onMove, onDelete,
}: Props) {
```

In the copy row, right after the Foil/Normal `<button>`:

```tsx
                <Select value={c.lang} onValueChange={(v) => onChangeLang(c, v)}>
                  <SelectTrigger className="w-[110px] h-8 text-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {languages.map((l) => (
                      <SelectItem key={l.code} value={l.code}>{l.label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
```

- [ ] **Step 2: Wiring** (`CollectionScreen.tsx`, the `<CardCopiesDialog>` element)

Add the two props next to `customs` / `onToggleFoil`:

```tsx
        languages={collection.languages}
        onChangeLang={async (c, lang) => {
          try {
            await UpdateCard({ ...c, lang });
            await refreshCards();
          } catch (e) {
            LogDebug(`[CollectionScreen] change language: ${e}`);
          }
        }}
```

- [ ] **Step 3: Export modal** (`ExportModal.tsx`)

```ts
const ALL_FIELDS: { key: ExportField; label: string }[] = [
  { key: "card_id", label: "Card ID" },
  { key: "normal_count", label: "Normal Count" },
  { key: "foil_count", label: "Foil Count" },
  { key: "total_count", label: "Total Count" },
  { key: "language", label: "Language" },
];
```

and in `previewValue`:

```ts
    case "total_count":  return String(EXAMPLE.normal + EXAMPLE.foil);
    case "language":     return "en";
```

- [ ] **Step 4: Type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/CardCopiesDialog.tsx frontend/src/components/CollectionScreen.tsx frontend/src/components/ExportModal.tsx
git commit -m "ui: edit a copy's language in the details dialog, Language export field"
```

---

### Task 6: Verification (controller + user)

- [ ] `go test ./...` green from the repo root; `cd frontend && npx tsc --noEmit` clean.
- [ ] `wails dev` live pass by the user: the Language select shows "English" by default; pick French, scan and save a card, open it in the collection: the row's language reads French; change it back to English; export the view with and without the Language field and compare the rows.
- [ ] Close the SDD ledger (`.superpowers/sdd/2026-09-07-card-language/progress.md`) and update memory (`project_card_language` or the frontend redesign entry).
