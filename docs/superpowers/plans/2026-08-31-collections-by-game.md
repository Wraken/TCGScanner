# Collections by Game Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the anonymous scan-session collections with derived per-game collections plus optional named custom collections, and build the frontend views on top (scanner selector, real home tiles, Nocturne collection screen).

**Architecture:** `cards` gains a denormalized `game` column resolved from the images manifest at save time; per-game collections are `GROUP BY game` (never stored, never drift); the `collections` table keeps only user-named custom collections and `cards.collection_id` becomes nullable (one custom max per copy). A v1→v2 migration rebuilds `cards`, cleans up old session collections, and backfills `game` on every startup.

**Tech Stack:** Go + modernc.org/sqlite, Wails v2 bindings, React/TS + Tailwind + shadcn/ui (Nocturne `n-*` classes).

**Spec:** `docs/superpowers/specs/2026-08-31-collections-by-game-design.md`

## Global Constraints

- UI copy is **French**; code, comments, commit messages are **English** (repo convention).
- Commit subjects: short, lowercase, imperative (matches repo history). End every commit message with the two trailer lines the controller supplies (Co-Authored-By + Claude-Session).
- `frontend/wailsjs/` is **gitignored** and regenerated. After ANY change to Go bound methods or bound structs, run `wails generate module` from the repo root so the frontend compiles.
- Platform is Windows; run Go tests with `go test ./... -count=1`. Some tests skip when `models/tcg` is absent — on this machine it is present, so they must PASS, not skip.
- Never touch `models/`, `scripts/embeddingtest/` data dirs, or anything under `E:\TCGScannerMobile`.
- One row in `cards` = one physical copy. Aggregation (normal/foil counts) happens at display/export time only.
- `game = ''` means "unknown"; UI shows it as "Autres" and only when non-empty.

---

### Task 1: Schema v2 + v1→v2 migration

**Files:**
- Modify: `db.go` (whole file — currently 43 lines)
- Test: `db_migration_test.go` (create)

**Interfaces:**
- Consumes: nothing new.
- Produces: `openDB(path string) (*sql.DB, error)` returning a DB whose schema is v2 (see spec). Later tasks rely on: `cards.game TEXT NOT NULL DEFAULT ''`, `cards.collection_id` NULLABLE with `ON DELETE SET NULL`, `collections.created_at` (renamed from `started_at`), and old empty unnamed collections being deleted / non-empty ones named `Collection <id>`.

- [ ] **Step 1: Write the failing tests**

Create `db_migration_test.go`:

```go
package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// buildV1DB creates a database with the pre-2026-08-31 schema and some rows:
// collection 1 = empty unnamed session, collection 2 = unnamed with 2 cards,
// collection 3 = named "Trades" with 1 card.
func buildV1DB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := `
		CREATE TABLE collections (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT    NOT NULL DEFAULT '',
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE cards (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			collection_id INTEGER NOT NULL REFERENCES collections(id),
			card_id       TEXT    NOT NULL,
			foil          INTEGER NOT NULL DEFAULT 0,
			model_name    TEXT    NOT NULL DEFAULT '',
			scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO collections (name) VALUES (''), (''), ('Trades');
		INSERT INTO cards (collection_id, card_id, foil, model_name) VALUES
			(2, 'OP01-025', 0, 'tcg'),
			(2, 'ogn-045-123', 1, 'tcg'),
			(3, 'sv01-020', 0, 'tcg');
	`
	if _, err := db.Exec(stmts); err != nil {
		t.Fatal(err)
	}
	return path
}

func columnInfo(t *testing.T, db *sql.DB, table string) map[string]struct {
	notNull bool
} {
	t.Helper()
	rows, err := db.Query(`SELECT name, "notnull" FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]struct{ notNull bool }{}
	for rows.Next() {
		var name string
		var nn int
		if err := rows.Scan(&name, &nn); err != nil {
			t.Fatal(err)
		}
		out[name] = struct{ notNull bool }{nn != 0}
	}
	return out
}

func TestMigrationV1ToV2(t *testing.T) {
	path := buildV1DB(t)
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cards := columnInfo(t, db, "cards")
	if _, ok := cards["game"]; !ok {
		t.Fatal("cards.game column missing after migration")
	}
	if cards["collection_id"].notNull {
		t.Fatal("cards.collection_id should be nullable after migration")
	}
	cols := columnInfo(t, db, "collections")
	if _, ok := cols["created_at"]; !ok {
		t.Fatal("collections.created_at missing (rename from started_at)")
	}

	// Empty unnamed session deleted; unnamed-with-cards renamed; named kept.
	rows, err := db.Query(`SELECT id, name FROM collections ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int]string{}
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		got[id] = name
	}
	if len(got) != 2 {
		t.Fatalf("want 2 collections after migration, got %v", got)
	}
	if got[2] != "Collection 2" {
		t.Errorf("collection 2 name = %q, want %q", got[2], "Collection 2")
	}
	if got[3] != "Trades" {
		t.Errorf("collection 3 name = %q, want %q", got[3], "Trades")
	}

	// Existing cards kept, with game backfill target '' for now.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cards WHERE game = ''`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("want 3 cards with empty game, got %d", n)
	}
}

func TestMigrationIdempotent(t *testing.T) {
	path := buildV1DB(t)
	for i := 0; i < 2; i++ {
		db, err := openDB(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		db.Close()
	}
}

func TestFreshDBSchemaV2(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// A copy can exist with no custom collection.
	if _, err := db.Exec(`INSERT INTO cards (collection_id, card_id, game) VALUES (NULL, 'sv01-020', 'pokemon')`); err != nil {
		t.Fatalf("insert with NULL collection_id: %v", err)
	}
}

func TestDeleteCollectionSetsNull(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "fk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO collections (name) VALUES ('Deck')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cards (collection_id, card_id, game) VALUES (1, 'a', 'pokemon')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM collections WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var cid sql.NullInt64
	if err := db.QueryRow(`SELECT collection_id FROM cards WHERE card_id = 'a'`).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	if cid.Valid {
		t.Fatalf("collection_id should be NULL after deleting the collection, got %d", cid.Int64)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestMigration|TestFreshDB|TestDeleteCollectionSetsNull' -count=1 -v .`
Expected: FAIL (`cards.game column missing`, NULL insert rejected, etc.)

- [ ] **Step 3: Rewrite `db.go`**

Replace the whole file with:

```go
package main

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

// Schema v2 (2026-08-31): per-game collections are DERIVED (GROUP BY
// cards.game); the collections table holds only user-named custom
// collections, and a copy belongs to at most one of them.
const schemaV2 = `
	CREATE TABLE IF NOT EXISTS collections (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT    NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS cards (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_id INTEGER REFERENCES collections(id) ON DELETE SET NULL,
		card_id       TEXT    NOT NULL,
		game          TEXT    NOT NULL DEFAULT '',
		foil          INTEGER NOT NULL DEFAULT 0,
		model_name    TEXT    NOT NULL DEFAULT '',
		scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
	);
`

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
	return db, nil
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
	return n > 0, err
}

func columnNotNull(db *sql.DB, table, column string) (bool, error) {
	var nn int
	err := db.QueryRow(`SELECT "notnull" FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&nn)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return nn != 0, err
}

// migrate brings any database (fresh, or any older layout) to schema v2.
// Idempotent: every step is a no-op on an already-migrated database.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaV2); err != nil {
		return err
	}

	// Pre-model_name databases (before the sessions->collections rename era).
	_, _ = db.Exec(`ALTER TABLE cards ADD COLUMN model_name TEXT NOT NULL DEFAULT ''`)
	// v1 -> v2 additions that ALTER can handle.
	_, _ = db.Exec(`ALTER TABLE cards ADD COLUMN game TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE collections RENAME COLUMN started_at TO created_at`)

	// v1 declared collection_id NOT NULL; SQLite cannot relax that in place,
	// so rebuild the table. Runs at most once per database.
	notNull, err := columnNotNull(db, "cards", "collection_id")
	if err != nil {
		return err
	}
	if notNull {
		if err := rebuildCards(db); err != nil {
			return err
		}
	}

	// Old auto-created scan sessions: drop the empty unnamed ones, promote
	// the unnamed ones that hold cards to regular named customs.
	if _, err := db.Exec(`
		DELETE FROM collections WHERE name = '' AND id NOT IN (
			SELECT DISTINCT collection_id FROM cards WHERE collection_id IS NOT NULL
		)`); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE collections SET name = 'Collection ' || id WHERE name = ''`); err != nil {
		return err
	}
	return nil
}

func rebuildCards(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`CREATE TABLE cards_v2 (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			collection_id INTEGER REFERENCES collections(id) ON DELETE SET NULL,
			card_id       TEXT    NOT NULL,
			game          TEXT    NOT NULL DEFAULT '',
			foil          INTEGER NOT NULL DEFAULT 0,
			model_name    TEXT    NOT NULL DEFAULT '',
			scanned_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO cards_v2 (id, collection_id, card_id, game, foil, model_name, scanned_at)
			SELECT id, collection_id, card_id, game, foil, model_name, scanned_at FROM cards`,
		`DROP TABLE cards`,
		`ALTER TABLE cards_v2 RENAME TO cards`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return tx.Commit()
}
```

Note: `PRAGMA foreign_keys = ON` moves AFTER `migrate` so the rebuild's
`DROP TABLE cards` never trips FK enforcement; `SetMaxOpenConns(1)` keeps the
pragma on the single connection for the process lifetime.

- [ ] **Step 4: Run the tests**

Run: `go test -run 'TestMigration|TestFreshDB|TestDeleteCollectionSetsNull' -count=1 -v .`
Expected: PASS (4 tests)

- [ ] **Step 5: Run the whole suite to catch fallout**

Run: `go test ./... -count=1`
Expected: `app.go` still compiles (startup still calls `getLastCollection`/`createCollection` — they still work against v2 because `createCollection` inserts DEFAULT VALUES... it does NOT anymore: `name` lost its DEFAULT). If `TestLoadModel`-style tests fail on startup paths, that is Task 4's territory — but the build must stay green. To keep it green in this task, patch `createCollection` minimally in `collection.go`:

```go
func createCollection(db *sql.DB) (int, error) {
	res, err := db.Exec(`INSERT INTO collections (name) VALUES ('')`)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}
```

and in `getLastCollection`, `getCollection`, `listCollections` replace `co.started_at` with `co.created_at` in the three SQL strings (the Go struct field mapping is Task 3's job; keep scanning into `StartedAt` for now).

Run again: `go test ./... -count=1` → all PASS.

- [ ] **Step 6: Commit**

```bash
git add db.go db_migration_test.go collection.go
git commit -m "feat: schema v2 - nullable custom collections, game column, v1 migration"
```

---

### Task 2: Game resolution from the manifest + startup backfill

**Files:**
- Modify: `card_images.go` (add helpers at the end)
- Modify: `card.go` (add `backfillCardGames` at the end)
- Test: `card_images_test.go` (append), `card_test.go` (create)

**Interfaces:**
- Consumes: `cardImageManifest(imagesDir string) map[string]string` (existing, mtime-cached), `openDB` from Task 1.
- Produces:
  - `modelImagesDir(modelName string) string` — `./models/<name>/images`
  - `gameOfCard(imagesDir, cardID string) string` — manifest game or `""`
  - `manifestGames(imagesDir string) []string` — sorted distinct games
  - `allModelImagesDirs() []string` — images dir of every dir under `./models`
  - `gameResolver(imagesDirs []string) func(cardID string) string`
  - `backfillCardGames(db *sql.DB, resolve func(string) string) (int, error)` — fills `game` for rows where it is `''`, returns number of updated rows

- [ ] **Step 1: Write the failing tests**

Append to `card_images_test.go`:

```go
func TestGameOfCard(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"OP01-025": "onepiece/OP01-025.webp", "flat-001": "flat-001.webp"}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if g := gameOfCard(dir, "OP01-025"); g != "onepiece" {
		t.Errorf("gameOfCard = %q, want onepiece", g)
	}
	if g := gameOfCard(dir, "flat-001"); g != "" {
		t.Errorf("flat entry should have no game, got %q", g)
	}
	if g := gameOfCard(dir, "unknown"); g != "" {
		t.Errorf("unknown id should have no game, got %q", g)
	}
}

func TestManifestGames(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"a": "pokemon/a.webp", "b": "onepiece/b.webp", "c": "pokemon/c.webp", "d": "d.webp"}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	got := manifestGames(dir)
	want := []string{"onepiece", "pokemon"}
	if !slices.Equal(got, want) {
		t.Errorf("manifestGames = %v, want %v", got, want)
	}
}

func TestGameOfCardRealModel(t *testing.T) {
	dir := modelImagesDir("tcg")
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Skip("models/tcg not present")
	}
	if g := gameOfCard(dir, "sv01-020"); g != "pokemon" {
		t.Errorf("sv01-020 game = %q, want pokemon", g)
	}
}
```

(add `"slices"` to the test file imports if missing)

Create `card_test.go`:

```go
package main

import (
	"path/filepath"
	"testing"
)

func TestBackfillCardGames(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "bf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO cards (card_id, game) VALUES ('OP01-025', '')`)
	mustExec(`INSERT INTO cards (card_id, game) VALUES ('OP01-025', '')`) // duplicate copy
	mustExec(`INSERT INTO cards (card_id, game) VALUES ('mystery-1', '')`)
	mustExec(`INSERT INTO cards (card_id, game) VALUES ('sv01-020', 'pokemon')`) // already set

	resolve := func(id string) string {
		if id == "OP01-025" {
			return "onepiece"
		}
		return ""
	}
	n, err := backfillCardGames(db, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("backfill updated %d rows, want 2", n)
	}
	var unknown int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cards WHERE game = ''`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if unknown != 1 {
		t.Errorf("%d rows still unknown, want 1 (mystery-1)", unknown)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestGameOfCard|TestManifestGames|TestBackfill' -count=1 -v .`
Expected: FAIL to compile (`undefined: gameOfCard`, ...)

- [ ] **Step 3: Implement the helpers**

Append to `card_images.go`:

```go
func modelImagesDir(modelName string) string {
	return filepath.Join(".", "models", modelName, "images")
}

// gameOfCard returns the game sub-directory the manifest files cardID under
// ("onepiece/OP01-025.webp" -> "onepiece"). Unknown ids and flat entries
// return "".
func gameOfCard(imagesDir, cardID string) string {
	rel, ok := cardImageManifest(imagesDir)[cardID]
	if !ok {
		return ""
	}
	if i := strings.IndexByte(rel, '/'); i > 0 {
		return rel[:i]
	}
	return ""
}

// manifestGames returns the sorted distinct games of a manifest.
func manifestGames(imagesDir string) []string {
	seen := map[string]bool{}
	for _, rel := range cardImageManifest(imagesDir) {
		if i := strings.IndexByte(rel, '/'); i > 0 {
			seen[rel[:i]] = true
		}
	}
	games := make([]string, 0, len(seen))
	for g := range seen {
		games = append(games, g)
	}
	sort.Strings(games)
	return games
}

// allModelImagesDirs lists the images dir of every model under ./models.
func allModelImagesDirs() []string {
	entries, err := os.ReadDir(filepath.Join(".", "models"))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, modelImagesDir(e.Name()))
		}
	}
	return dirs
}

// gameResolver resolves a card id through several manifests, first hit wins.
func gameResolver(imagesDirs []string) func(cardID string) string {
	return func(cardID string) string {
		for _, dir := range imagesDirs {
			if g := gameOfCard(dir, cardID); g != "" {
				return g
			}
		}
		return ""
	}
}
```

(add `"sort"` to the imports; also update `loadCardImage` to use the new helper:)

```go
func loadCardImage(cardID, modelName string) (string, error) {
	return loadCardImageFrom(modelImagesDir(modelName), cardID)
}
```

Append to `card.go`:

```go
// backfillCardGames fills cards.game for rows where it is still unknown.
// Runs on every startup: cards scanned before a game's images were installed
// heal themselves once the manifest knows their id.
func backfillCardGames(db *sql.DB, resolve func(cardID string) string) (int, error) {
	rows, err := db.Query(`SELECT DISTINCT card_id FROM cards WHERE game = ''`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	total := 0
	for _, id := range ids {
		g := resolve(id)
		if g == "" {
			continue
		}
		res, err := db.Exec(`UPDATE cards SET game = ? WHERE card_id = ? AND game = ''`, g, id)
		if err != nil {
			return total, err
		}
		if n, err := res.RowsAffected(); err == nil {
			total += int(n)
		}
	}
	return total, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -run 'TestGameOfCard|TestManifestGames|TestBackfill' -count=1 -v .`
Expected: PASS (4 tests, `TestGameOfCardRealModel` must PASS on this machine, not skip)

- [ ] **Step 5: Full suite + commit**

Run: `go test ./... -count=1` → PASS.

```bash
git add card_images.go card_images_test.go card.go card_test.go
git commit -m "feat: game resolution from image manifests + startup backfill"
```

---

### Task 3: Data layer — customs CRUD, game views, filtered card queries

**Files:**
- Modify: `collection.go` (whole file)
- Modify: `card.go` (whole file except `backfillCardGames` from Task 2)
- Test: `collection_test.go` (create), `card_test.go` (append)

**Interfaces:**
- Consumes: `openDB` (Task 1).
- Produces (later tasks and the frontend rely on these exact shapes):

```go
type Collection struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	CardCount int    `json:"card_count"`
}
type GameCollection struct {
	Game      string `json:"game"`
	CardCount int    `json:"card_count"`
	FoilCount int    `json:"foil_count"`
}
type CardFilter struct {
	Game         string `json:"game,omitempty"`
	OtherBucket  bool   `json:"other_bucket,omitempty"`
	CollectionID int    `json:"collection_id,omitempty"`
}
type CollectionCard struct {
	ID           int    `json:"id"`
	CardID       string `json:"card_id"`
	Game         string `json:"game"`
	CollectionID int    `json:"collection_id"` // 0 = no custom
	Foil         bool   `json:"foil"`
	ModelName    string `json:"model_name"`
	ScannedAt    string `json:"scanned_at"`
}

func createCollection(db *sql.DB, name string) (int, error)          // error on empty trimmed name
func getCollection(db *sql.DB, id int) (Collection, error)
func listCollections(db *sql.DB) ([]Collection, error)               // newest first
func renameCollection(db *sql.DB, id int, name string) error         // error on empty trimmed name
func deleteCollection(db *sql.DB, id int) error                      // copies keep game, lose custom
func gameCollections(db *sql.DB, knownGames []string) ([]GameCollection, error)
func cardFilterWhere(f CardFilter) (string, []any, error)            // "game = ?" / "game = ''" / "collection_id = ?"
func insertCard(db *sql.DB, collectionID int, cardID, game, modelName string, foil bool) (int, error) // returns row id; collectionID 0 -> NULL
func getCard(db *sql.DB, id int) (CollectionCard, error)
func listCards(db *sql.DB, f CardFilter) ([]CollectionCard, error)
func moveCardToCustom(db *sql.DB, rowID, customID int) error         // customID 0 -> NULL
func updateCard(db *sql.DB, c CollectionCard) error                  // sets foil, card_id, game
func deleteCard(db *sql.DB, id int) error                            // unchanged
```

Removed: `getLastCollection` (delete it; Task 4 removes its caller).

- [ ] **Step 1: Write the failing tests**

Create `collection_test.go`:

```go
package main

import (
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCustomCollectionCRUD(t *testing.T) {
	db := testDB(t)

	if _, err := createCollection(db, "  "); err == nil {
		t.Fatal("empty name should be rejected")
	}
	id, err := createCollection(db, "  Deck Ligue ")
	if err != nil {
		t.Fatal(err)
	}
	c, err := getCollection(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Deck Ligue" {
		t.Errorf("name = %q, want trimmed %q", c.Name, "Deck Ligue")
	}
	if err := renameCollection(db, id, ""); err == nil {
		t.Fatal("rename to empty should be rejected")
	}
	if err := renameCollection(db, id, "À vendre"); err != nil {
		t.Fatal(err)
	}

	// A copy in the custom survives the custom's deletion.
	rowID, err := insertCard(db, id, "sv01-020", "pokemon", "tcg", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteCollection(db, id); err != nil {
		t.Fatal(err)
	}
	card, err := getCard(db, rowID)
	if err != nil {
		t.Fatal(err)
	}
	if card.CollectionID != 0 {
		t.Errorf("collection_id = %d after delete, want 0", card.CollectionID)
	}
	if card.Game != "pokemon" {
		t.Errorf("game = %q after delete, want pokemon", card.Game)
	}
}

func TestGameCollections(t *testing.T) {
	db := testDB(t)
	seed := []struct {
		id, game string
		foil     bool
	}{
		{"sv01-020", "pokemon", false},
		{"sv01-020", "pokemon", true},
		{"OP01-025", "onepiece", false},
		{"weird-1", "", false},
	}
	for _, s := range seed {
		if _, err := insertCard(db, 0, s.id, s.game, "tcg", s.foil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := gameCollections(db, []string{"lorcana", "onepiece", "pokemon"})
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by game; '' bucket LAST and only when non-empty; known-but-empty
	// games present with zero counts.
	want := []GameCollection{
		{Game: "lorcana", CardCount: 0, FoilCount: 0},
		{Game: "onepiece", CardCount: 1, FoilCount: 0},
		{Game: "pokemon", CardCount: 2, FoilCount: 1},
		{Game: "", CardCount: 1, FoilCount: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCardFilterValidation(t *testing.T) {
	db := testDB(t)
	bad := []CardFilter{
		{},                                     // nothing set
		{Game: "pokemon", CollectionID: 3},     // two set
		{Game: "pokemon", OtherBucket: true},   // two set
		{OtherBucket: true, CollectionID: 3},   // two set
	}
	for _, f := range bad {
		if _, err := listCards(db, f); err == nil {
			t.Errorf("filter %+v should be rejected", f)
		}
	}
}

func TestListCardsAndMove(t *testing.T) {
	db := testDB(t)
	custom, err := createCollection(db, "Trades")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := insertCard(db, 0, "sv01-020", "pokemon", "tcg", false)
	b, _ := insertCard(db, custom, "OP01-025", "onepiece", "tcg", true)
	if _, err := insertCard(db, 0, "weird-1", "", "tcg", false); err != nil {
		t.Fatal(err)
	}

	poke, err := listCards(db, CardFilter{Game: "pokemon"})
	if err != nil {
		t.Fatal(err)
	}
	if len(poke) != 1 || poke[0].ID != a || poke[0].Game != "pokemon" {
		t.Fatalf("pokemon view = %+v", poke)
	}

	other, err := listCards(db, CardFilter{OtherBucket: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 || other[0].CardID != "weird-1" {
		t.Fatalf("other bucket = %+v", other)
	}

	inCustom, err := listCards(db, CardFilter{CollectionID: custom})
	if err != nil {
		t.Fatal(err)
	}
	if len(inCustom) != 1 || inCustom[0].ID != b || inCustom[0].CollectionID != custom {
		t.Fatalf("custom view = %+v", inCustom)
	}

	// Move a into the custom, b out of it.
	if err := moveCardToCustom(db, a, custom); err != nil {
		t.Fatal(err)
	}
	if err := moveCardToCustom(db, b, 0); err != nil {
		t.Fatal(err)
	}
	inCustom, _ = listCards(db, CardFilter{CollectionID: custom})
	if len(inCustom) != 1 || inCustom[0].ID != a {
		t.Fatalf("custom view after moves = %+v", inCustom)
	}
}

func TestUpdateCardRewritesIdAndGame(t *testing.T) {
	db := testDB(t)
	id, _ := insertCard(db, 0, "sv01-020", "pokemon", "tcg", false)
	c, _ := getCard(db, id)
	c.CardID = "OP01-025"
	c.Game = "onepiece"
	c.Foil = true
	if err := updateCard(db, c); err != nil {
		t.Fatal(err)
	}
	got, _ := getCard(db, id)
	if got.CardID != "OP01-025" || got.Game != "onepiece" || !got.Foil {
		t.Fatalf("after update: %+v", got)
	}
}
```

(add `"database/sql"` to imports)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestCustomCollection|TestGameCollections|TestCardFilter|TestListCardsAndMove|TestUpdateCardRewrites' -count=1 -v .`
Expected: compile FAIL (`createCollection` arity, `insertCard` arity, undefined types)

- [ ] **Step 3: Rewrite `collection.go`**

```go
package main

import (
	"database/sql"
	"fmt"
	"strings"
)

// Collection is a user-named custom collection (a grouping of copies).
// Per-game collections are derived from cards.game — see gameCollections.
type Collection struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	CardCount int    `json:"card_count"`
}

type GameCollection struct {
	Game      string `json:"game"`
	CardCount int    `json:"card_count"`
	FoilCount int    `json:"foil_count"`
}

func createCollection(db *sql.DB, name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("collection name cannot be empty")
	}
	res, err := db.Exec(`INSERT INTO collections (name) VALUES (?)`, name)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func getCollection(db *sql.DB, id int) (Collection, error) {
	var c Collection
	err := db.QueryRow(`
		SELECT co.id, co.name, co.created_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		WHERE co.id = ?
		GROUP BY co.id
	`, id).Scan(&c.ID, &c.Name, &c.CreatedAt, &c.CardCount)
	return c, err
}

func listCollections(db *sql.DB) ([]Collection, error) {
	rows, err := db.Query(`
		SELECT co.id, co.name, co.created_at, COUNT(ca.id)
		FROM collections co
		LEFT JOIN cards ca ON ca.collection_id = co.id
		GROUP BY co.id
		ORDER BY co.created_at DESC, co.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.CardCount); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

func renameCollection(db *sql.DB, id int, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("collection name cannot be empty")
	}
	_, err := db.Exec(`UPDATE collections SET name = ? WHERE id = ?`, name, id)
	return err
}

// deleteCollection removes a custom collection; its copies keep their game
// and simply leave the custom. The explicit UPDATE makes the behavior
// independent of the connection's foreign_keys pragma.
func deleteCollection(db *sql.DB, id int) error {
	if _, err := db.Exec(`UPDATE cards SET collection_id = NULL WHERE collection_id = ?`, id); err != nil {
		return err
	}
	_, err := db.Exec(`DELETE FROM collections WHERE id = ?`, id)
	return err
}

// gameCollections derives the per-game collections: every game the manifests
// know (zero counts included) plus every game present in the DB, sorted, with
// the unknown '' bucket last and only when non-empty.
func gameCollections(db *sql.DB, knownGames []string) ([]GameCollection, error) {
	rows, err := db.Query(`
		SELECT game, COUNT(*), SUM(foil)
		FROM cards
		GROUP BY game
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]GameCollection{}
	for rows.Next() {
		var g GameCollection
		if err := rows.Scan(&g.Game, &g.CardCount, &g.FoilCount); err != nil {
			return nil, err
		}
		counts[g.Game] = g
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []GameCollection
	for _, g := range knownGames {
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		gc := counts[g]
		gc.Game = g
		out = append(out, gc)
	}
	for g, gc := range counts {
		if g != "" && !seen[g] {
			out = append(out, gc)
		}
	}
	sortGameCollections(out)
	if other, ok := counts[""]; ok && other.CardCount > 0 {
		out = append(out, other)
	}
	return out, nil
}

func sortGameCollections(gs []GameCollection) {
	for i := 1; i < len(gs); i++ {
		for j := i; j > 0 && gs[j].Game < gs[j-1].Game; j-- {
			gs[j], gs[j-1] = gs[j-1], gs[j]
		}
	}
}
```

(Use `sort.Slice` instead of the hand-rolled sort if you prefer — then import `"sort"`. Either is fine; keep one.)

- [ ] **Step 4: Rewrite `card.go`** (keep `backfillCardGames` from Task 2 at the end)

```go
package main

import (
	"database/sql"
	"fmt"
)

type CollectionCard struct {
	ID           int    `json:"id"`
	CardID       string `json:"card_id"`
	Game         string `json:"game"`
	CollectionID int    `json:"collection_id"` // 0 = not in a custom collection
	Foil         bool   `json:"foil"`
	ModelName    string `json:"model_name"`
	ScannedAt    string `json:"scanned_at"`
}

// CardFilter selects one view: a game collection, the unknown-game bucket,
// or a custom collection. Exactly one selector must be set.
type CardFilter struct {
	Game         string `json:"game,omitempty"`
	OtherBucket  bool   `json:"other_bucket,omitempty"`
	CollectionID int    `json:"collection_id,omitempty"`
}

func cardFilterWhere(f CardFilter) (string, []any, error) {
	set := 0
	if f.Game != "" {
		set++
	}
	if f.OtherBucket {
		set++
	}
	if f.CollectionID != 0 {
		set++
	}
	if set != 1 {
		return "", nil, fmt.Errorf("card filter must set exactly one of game, other_bucket, collection_id")
	}
	switch {
	case f.Game != "":
		return "game = ?", []any{f.Game}, nil
	case f.OtherBucket:
		return "game = ''", nil, nil
	default:
		return "collection_id = ?", []any{f.CollectionID}, nil
	}
}

func nullableID(id int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(id), Valid: id > 0}
}

func insertCard(db *sql.DB, collectionID int, cardID, game, modelName string, foil bool) (int, error) {
	foilInt := 0
	if foil {
		foilInt = 1
	}
	res, err := db.Exec(
		`INSERT INTO cards (collection_id, card_id, game, foil, model_name) VALUES (?, ?, ?, ?, ?)`,
		nullableID(collectionID), cardID, game, foilInt, modelName,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func scanCard(row interface{ Scan(...any) error }) (CollectionCard, error) {
	var c CollectionCard
	var foilInt int
	var cid sql.NullInt64
	err := row.Scan(&c.ID, &c.CardID, &c.Game, &cid, &foilInt, &c.ModelName, &c.ScannedAt)
	if err != nil {
		return c, err
	}
	c.Foil = foilInt != 0
	if cid.Valid {
		c.CollectionID = int(cid.Int64)
	}
	return c, nil
}

const cardColumns = `id, card_id, game, collection_id, foil, model_name, scanned_at`

func getCard(db *sql.DB, id int) (CollectionCard, error) {
	return scanCard(db.QueryRow(`SELECT `+cardColumns+` FROM cards WHERE id = ?`, id))
}

func listCards(db *sql.DB, f CardFilter) ([]CollectionCard, error) {
	where, args, err := cardFilterWhere(f)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT `+cardColumns+` FROM cards WHERE `+where+` ORDER BY scanned_at ASC, id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cards []CollectionCard
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

func moveCardToCustom(db *sql.DB, rowID, customID int) error {
	_, err := db.Exec(`UPDATE cards SET collection_id = ? WHERE id = ?`, nullableID(customID), rowID)
	return err
}

func updateCard(db *sql.DB, c CollectionCard) error {
	foilInt := 0
	if c.Foil {
		foilInt = 1
	}
	_, err := db.Exec(`UPDATE cards SET foil = ?, card_id = ?, game = ? WHERE id = ?`,
		foilInt, c.CardID, c.Game, c.ID)
	return err
}

func deleteCard(db *sql.DB, id int) error {
	_, err := db.Exec(`DELETE FROM cards WHERE id = ?`, id)
	return err
}
```

Delete `getLastCollection` from `collection.go` and `listCollectionCards` from `card.go`. This BREAKS `app.go` (startup, `GetCollectionCards`, `SaveCard` call `insertCard` with the old arity). To keep the build green within this task, patch `app.go` minimally — these lines are rewritten properly in Task 4:

- In `startup`: delete the whole `last, err := getLastCollection(db)` switch block (keep `a.db = db`).
- `GetCollectionCards`: change body to `return listCards(a.db, CardFilter{CollectionID: collectionID})`.
- `SaveCard`: change the insert line to `_, err := insertCard(a.db, a.collectionID, cardID, "", modelName, foil); return err`.
- `NewCollection`: change `createCollection(a.db)` to `createCollection(a.db, "Sans nom")`.

- [ ] **Step 5: Run the tests**

Run: `go test -run 'TestCustomCollection|TestGameCollections|TestCardFilter|TestListCardsAndMove|TestUpdateCardRewrites' -count=1 -v .` → PASS
Run: `go test ./... -count=1` → PASS

- [ ] **Step 6: Commit**

```bash
git add collection.go card.go collection_test.go card_test.go app.go
git commit -m "feat: data layer for game views, custom collections and filtered card queries"
```

---

### Task 4: App API + filtered CSV export

**Files:**
- Modify: `app.go`
- Modify: `export.go`
- Test: `export_test.go` (create)

**Interfaces:**
- Consumes: everything from Tasks 1–3 (exact signatures listed there).
- Produces (Wails-bound methods the frontend calls — names are binding names):

```go
func (a *App) SaveCard(cardID string, foil bool) (CollectionCard, error)
func (a *App) SetActiveCustom(id int)
func (a *App) GetActiveCustom() int
func (a *App) GetGameCollections() ([]GameCollection, error)
func (a *App) GetCustomCollections() ([]Collection, error)
func (a *App) NewCustomCollection(name string) (Collection, error)
func (a *App) RenameCollection(id int, name string) error
func (a *App) DeleteCollection(id int) error
func (a *App) GetCards(f CardFilter) ([]CollectionCard, error)
func (a *App) MoveCardToCustom(rowID, customID int) error
func (a *App) UpdateCard(card CollectionCard) error   // re-resolves game from card_id
func (a *App) DeleteCard(id int) error                // unchanged
func (a *App) ExportCSV(f CardFilter, config ExportConfig) (string, error)
func (a *App) GetCardImage(cardID string) (string, error)          // unchanged
func (a *App) GetCardImageByModel(cardID, modelName string) (string, error) // unchanged
```

Removed bindings: `NewCollection`, `GetCurrentCollectionID`, `SetCollection`, `GetCollectionCards`, `GetCollections`, `ExportCollectionCSV`.

- [ ] **Step 1: Write the failing export test**

Create `export_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCardsCSVFiltered(t *testing.T) {
	db := testDB(t)
	seed := []struct {
		id, game string
		foil     bool
	}{
		{"sv01-020", "pokemon", false},
		{"sv01-020", "pokemon", false},
		{"sv01-020", "pokemon", true},
		{"OP01-025", "onepiece", false},
	}
	for _, s := range seed {
		if _, err := insertCard(db, 0, s.id, s.game, "tcg", s.foil); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(t.TempDir(), "out.csv")
	config := ExportConfig{Fields: []ExportField{FieldNormalCount, FieldFoilCount, FieldCardID}}
	if err := writeCardsCSV(db, CardFilter{Game: "pokemon"}, path, config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	want := "Normal Count,Foil Count,Card ID\n2,1,sv01-020"
	if got != want {
		t.Errorf("csv = %q, want %q", got, want)
	}

	if err := writeCardsCSV(db, CardFilter{}, path, config); err == nil {
		t.Error("invalid filter should be rejected")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -run TestWriteCardsCSVFiltered -count=1 -v .`
Expected: compile FAIL (`undefined: writeCardsCSV`)

- [ ] **Step 3: Update `export.go`**

Replace `writeCollectionCSV` with:

```go
func writeCardsCSV(db *sql.DB, filter CardFilter, path string, config ExportConfig) error {
	where, args, err := cardFilterWhere(filter)
	if err != nil {
		return err
	}
	rows, err := db.Query(`
		SELECT card_id,
		       SUM(CASE WHEN foil = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN foil = 1 THEN 1 ELSE 0 END)
		FROM cards
		WHERE `+where+`
		GROUP BY card_id
		ORDER BY card_id ASC
	`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)

	headers := make([]string, len(config.Fields))
	for i, field := range config.Fields {
		headers[i] = fieldLabel(field)
	}
	w.Write(headers)

	for rows.Next() {
		var cardID string
		var normalCount, foilCount int
		if err := rows.Scan(&cardID, &normalCount, &foilCount); err != nil {
			return err
		}
		row := make([]string, len(config.Fields))
		for i, field := range config.Fields {
			row[i] = resolveField(cardID, normalCount, foilCount, field, config)
		}
		w.Write(row)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	w.Flush()
	return w.Error()
}
```

(everything above `writeCollectionCSV` in the file — fields, presets, helpers — is unchanged)

- [ ] **Step 4: Rewrite the collection-related parts of `app.go`**

`App` struct: replace `collectionID int` with `activeCustomID int`.

`startup` becomes:

```go
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	db, err := openDB("./collection.db")
	if err != nil {
		fmt.Printf("failed to open database: %v\n", err)
		return
	}
	a.db = db

	// Cards scanned before their game's images were installed heal on start.
	if n, err := backfillCardGames(db, gameResolver(allModelImagesDirs())); err != nil {
		fmt.Printf("game backfill failed: %v\n", err)
	} else if n > 0 {
		fmt.Printf("game backfill: %d copies resolved\n", n)
	}
}
```

Replace `NewCollection`, `GetCurrentCollectionID`, `SetCollection`, `GetCollectionCards`, `GetCollections`, `SaveCard`, `UpdateCard`, `ExportCollectionCSV` with:

```go
func (a *App) SetActiveCustom(id int) {
	a.activeCustomID = id
}

func (a *App) GetActiveCustom() int {
	return a.activeCustomID
}

func (a *App) SaveCard(cardID string, foil bool) (CollectionCard, error) {
	if a.db == nil {
		return CollectionCard{}, fmt.Errorf("database not initialized")
	}
	model := a.currentModel()
	if model == nil {
		return CollectionCard{}, fmt.Errorf("no model loaded")
	}
	game := gameOfCard(modelImagesDir(model.Name()), cardID)
	id, err := insertCard(a.db, a.activeCustomID, cardID, game, model.Name(), foil)
	if err != nil {
		return CollectionCard{}, err
	}
	return getCard(a.db, id)
}

func (a *App) GetGameCollections() ([]GameCollection, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	var known []string
	for _, dir := range allModelImagesDirs() {
		known = append(known, manifestGames(dir)...)
	}
	return gameCollections(a.db, known)
}

func (a *App) GetCustomCollections() ([]Collection, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	return listCollections(a.db)
}

func (a *App) NewCustomCollection(name string) (Collection, error) {
	if a.db == nil {
		return Collection{}, fmt.Errorf("database not initialized")
	}
	id, err := createCollection(a.db, name)
	if err != nil {
		return Collection{}, err
	}
	return getCollection(a.db, id)
}

func (a *App) DeleteCollection(id int) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	if a.activeCustomID == id {
		a.activeCustomID = 0
	}
	return deleteCollection(a.db, id)
}

func (a *App) GetCards(f CardFilter) ([]CollectionCard, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	return listCards(a.db, f)
}

func (a *App) MoveCardToCustom(rowID, customID int) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return moveCardToCustom(a.db, rowID, customID)
}

func (a *App) UpdateCard(card CollectionCard) error {
	if a.db == nil {
		return fmt.Errorf("database not initialized")
	}
	// The id may have been corrected by hand — re-resolve the game.
	card.Game = gameResolver(allModelImagesDirs())(card.CardID)
	return updateCard(a.db, card)
}

func (a *App) ExportCSV(f CardFilter, config ExportConfig) (string, error) {
	if a.db == nil {
		return "", fmt.Errorf("database not initialized")
	}
	if len(config.Fields) == 0 {
		config = ExportConfig{Fields: builtinPresets[0].Fields}
	}
	name := f.Game
	if f.OtherBucket {
		name = "autres"
	}
	if f.CollectionID != 0 {
		if c, err := getCollection(a.db, f.CollectionID); err == nil {
			name = c.Name
		}
	}
	if name == "" {
		name = "export"
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: fmt.Sprintf("%s.csv", name),
		Filters: []runtime.FileFilter{
			{DisplayName: "CSV Files (*.csv)", Pattern: "*.csv"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := writeCardsCSV(a.db, f, path, config); err != nil {
		return "", err
	}
	return path, nil
}
```

Keep unchanged: `RenameCollection`, `DeleteCard`, `GetCardImage`, `GetCardImageByModel`, `ListExportPresets`, `ListModels`, `LoadModel`, `Predict`, `Close`, `currentModel`.

- [ ] **Step 5: Run everything**

Run: `go test ./... -count=1` → PASS.
Run: `go build ./...` → OK.

- [ ] **Step 6: Regenerate the Wails bindings**

Run from repo root: `wails generate module`
Expected: `frontend/wailsjs/go/main/App.d.ts` now exports `SaveCard(...): Promise<main.CollectionCard>`, `GetGameCollections`, `GetCards`, `ExportCSV`, etc., and `models.ts` has `GameCollection` and `CardFilter` classes. (This dir is gitignored — nothing to commit from it.)

- [ ] **Step 7: Commit**

```bash
git add app.go export.go export_test.go
git commit -m "feat: app api for game views, active custom and filtered csv export"
```

---

### Task 5: Frontend data layer — types, game labels, useCollection rewrite

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/lib/utils.ts`
- Rewrite: `frontend/src/hooks/useCollection.ts`

**Interfaces:**
- Consumes: the Task 4 bindings via `frontend/wailsjs/go/main/App` (regenerate first if missing: `wails generate module`).
- Produces (screens in Tasks 6–8 use exactly these):

```ts
// types.ts
export interface Collection { id: number; name: string; created_at: string; card_count: number; }
export interface GameCollection { game: string; card_count: number; foil_count: number; }
export interface CollectionCard {
  id: number; card_id: string; game: string; collection_id: number;
  foil: boolean; model_name: string; scanned_at: string;
}
export interface CardFilter { game?: string; other_bucket?: boolean; collection_id?: number; }

// lib/utils.ts
export function gameLabel(game: string): string;

// useCollection() returns:
{
  games: GameCollection[];
  customs: Collection[];
  activeCustomId: number;
  refresh(): Promise<void>;
  setActiveCustom(id: number): void;
  createCustom(name: string): Promise<Collection | null>;
  renameCustom(id: number, name: string): Promise<void>;
  deleteCustom(id: number): Promise<void>;
  exportTarget: CardFilter | null;
  openExport(f: CardFilter): void;
  closeExport(): void;
  exportPresets: ExportPreset[];
  confirmExport(config: ExportConfig): Promise<void>;
}
```

- [ ] **Step 1: Update `types.ts`**

Replace the `Collection` and `CollectionCard` interfaces and add the new ones:

```ts
export interface Collection {
  id: number;
  name: string;
  created_at: string;
  card_count: number;
}

export interface GameCollection {
  game: string;
  card_count: number;
  foil_count: number;
}

export interface CollectionCard {
  id: number;
  card_id: string;
  game: string;
  // Custom collection this copy belongs to; 0 = none.
  collection_id: number;
  foil: boolean;
  model_name: string;
  scanned_at: string;
}

// Selects one view: a game, the unknown-game bucket, or a custom collection.
// Exactly one field set (the backend rejects anything else).
export interface CardFilter {
  game?: string;
  other_bucket?: boolean;
  collection_id?: number;
}
```

(`CameraDevice`, `BoxNorm`, `Prediction`, export types unchanged.)

- [ ] **Step 2: Update `lib/utils.ts`**

Replace `formatCollectionLabel` (its `started_at` field is gone) with:

```ts
export function formatCollectionLabel(c: Collection): string {
  return `${c.name} (${c.card_count})`;
}

const GAME_LABELS: Record<string, string> = {
  pokemon: "Pokémon",
  onepiece: "One Piece",
  lorcana: "Lorcana",
  riftbound: "Riftbound",
  "": "Autres",
};

export function gameLabel(game: string): string {
  return GAME_LABELS[game] ?? game;
}
```

- [ ] **Step 3: Rewrite `useCollection.ts`**

```ts
import { useEffect, useState } from "react";
import {
  DeleteCollection,
  ExportCSV,
  GetCustomCollections,
  GetGameCollections,
  ListExportPresets,
  NewCustomCollection,
  RenameCollection,
  SetActiveCustom,
} from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import type { CardFilter, Collection, ExportConfig, ExportPreset, GameCollection } from "@/types";

export function useCollection() {
  const [games, setGames] = useState<GameCollection[]>([]);
  const [customs, setCustoms] = useState<Collection[]>([]);
  // Custom collection new scans are also filed under; 0 = none. In-memory
  // only, on purpose: it resets to "aucune" on every app start.
  const [activeCustomId, setActiveCustomId] = useState(0);
  const [exportTarget, setExportTarget] = useState<CardFilter | null>(null);
  const [exportPresets, setExportPresets] = useState<ExportPreset[]>([]);

  useEffect(() => {
    refresh();
    ListExportPresets()
      .then((p) => setExportPresets((p ?? []) as ExportPreset[]))
      .catch(() => {});
  }, []);

  async function refresh() {
    try {
      const [g, c] = await Promise.all([GetGameCollections(), GetCustomCollections()]);
      setGames((g ?? []) as GameCollection[]);
      setCustoms((c ?? []) as Collection[]);
    } catch (e) {
      LogDebug(`[Collections] refresh error: ${e}`);
    }
  }

  function setActiveCustom(id: number) {
    setActiveCustomId(id);
    SetActiveCustom(id);
  }

  async function createCustom(name: string): Promise<Collection | null> {
    try {
      const c = (await NewCustomCollection(name)) as Collection;
      setCustoms((prev) => [c, ...prev]);
      return c;
    } catch (e) {
      LogDebug(`[Collections] create error: ${e}`);
      return null;
    }
  }

  async function renameCustom(id: number, name: string) {
    try {
      await RenameCollection(id, name);
      await refresh();
    } catch (e) {
      LogDebug(`[Collections] rename error: ${e}`);
    }
  }

  async function deleteCustom(id: number) {
    try {
      await DeleteCollection(id);
      if (activeCustomId === id) setActiveCustomId(0);
      await refresh();
    } catch (e) {
      LogDebug(`[Collections] delete error: ${e}`);
    }
  }

  function openExport(f: CardFilter) {
    setExportTarget(f);
  }

  function closeExport() {
    setExportTarget(null);
  }

  async function confirmExport(config: ExportConfig) {
    if (!exportTarget) return;
    try {
      await ExportCSV(exportTarget, config);
    } catch (e) {
      LogDebug(`[Export] error: ${e}`);
    }
    setExportTarget(null);
  }

  return {
    games,
    customs,
    activeCustomId,
    refresh,
    setActiveCustom,
    createCustom,
    renameCustom,
    deleteCustom,
    exportTarget,
    openExport,
    closeExport,
    exportPresets,
    confirmExport,
  };
}
```

- [ ] **Step 4: Check what still compiles**

Run from `frontend/`: `npx tsc --noEmit`
Expected: errors ONLY in `ScanScreen.tsx`, `App.tsx`, `HomeScreen.tsx` usages that Tasks 6–8 rewrite (old hook members like `currentCollectionId`, `openCollectionDetail`), and in orphaned components (`CollectionBar.tsx`, `CollectionDetail.tsx`, `CardForm.tsx`). Nothing else. Note the error list in the task report; do NOT commit yet if unrelated files break.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types.ts frontend/src/lib/utils.ts frontend/src/hooks/useCollection.ts
git commit -m "feat: frontend data layer for game and custom collections"
```

(The frontend build is intentionally red between Tasks 5 and 8 — the tasks are committed in sequence in the same session. Task 8 ends with a green `tsc` + `vite build` gate.)

---

### Task 6: Scanner — "Ajouter aussi à" selector

**Files:**
- Modify: `frontend/src/components/ScanScreen.tsx`

**Interfaces:**
- Consumes: `useCollection()` shape from Task 5; `SaveCard` binding now returns `Promise<main.CollectionCard>` (has `.game`); `gameLabel` from `lib/utils`.
- Produces: `ScanScreen` still takes `{ model, collection }` props (same as today, `collection` being the new hook shape).

- [ ] **Step 1: Replace the Collection block (lines ~199–234)**

Replace the whole `{/* Collection */}` div with:

```tsx
        {/* Optional custom collection new scans are also filed under */}
        <div>
          <label className="block text-xs n-n400 mb-1.5">Ajouter aussi à</label>
          <div className="flex items-center gap-2">
            <Select
              value={String(collection.activeCustomId)}
              onValueChange={(v) => collection.setActiveCustom(parseInt(v, 10) || 0)}
            >
              <SelectTrigger className="flex-1 min-w-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">aucune collection</SelectItem>
                {collection.customs.map((c) => (
                  <SelectItem key={c.id} value={String(c.id)}>
                    {formatCollectionLabel(c)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <button
              className="n-btn n-btn-secondary n-btn-icon shrink-0"
              title="Nouvelle collection custom"
              onClick={handleNewCustom}
            >
              <Plus className="w-4 h-4" />
            </button>
          </div>
        </div>
```

Add next to `handleSave`:

```tsx
  async function handleNewCustom() {
    const name = window.prompt("Nom de la nouvelle collection :")?.trim();
    if (!name) return;
    const c = await collection.createCustom(name);
    if (c) collection.setActiveCustom(c.id);
  }
```

- [ ] **Step 2: Show which game the copy was filed under**

Change `handleSave` and add a `lastSaved` state:

```tsx
  const [lastSaved, setLastSaved] = useState<{ card_id: string; game: string } | null>(null);

  async function handleSave() {
    const id = pred.editedCardID.trim();
    if (!id) return;
    const image = pred.cardImage;
    try {
      const saved = await SaveCard(id, pred.foil);
      await collection.refresh();
      setLastSaved({ card_id: saved.card_id, game: saved.game });
      setRecent((prev) => [{ card_id: id, image }, ...prev.filter((r) => r.card_id !== id)].slice(0, 3));
    } catch (e) {
      LogDebug(`[SaveCard] Error: ${e}`);
    }
    pred.resetCard();
  }
```

And under the `{/* Actions */}` buttons div, add:

```tsx
        {lastSaved && (
          <p className="text-center text-xs n-n400">
            {lastSaved.card_id} ajouté à {gameLabel(lastSaved.game)}
          </p>
        )}
```

- [ ] **Step 3: Fix imports and remove dead usages**

- Import `gameLabel` alongside `formatCollectionLabel` from `@/lib/utils`.
- Remove the now-unused `List` icon import.
- `collection.refreshCollections()` no longer exists — the code above already uses `collection.refresh()`.

- [ ] **Step 4: Verify**

Run from `frontend/`: `npx tsc --noEmit`
Expected: no errors in `ScanScreen.tsx` (remaining errors only in files Tasks 7–8 rewrite).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ScanScreen.tsx
git commit -m "feat: scanner files copies by game with optional custom collection"
```

---

### Task 7: Home — real stats and game tiles

**Files:**
- Rewrite: `frontend/src/components/HomeScreen.tsx`

**Interfaces:**
- Consumes: `GameCollection`, `Collection`, `gameLabel` (Task 5).
- Produces: `HomeScreen` props consumed by Task 8's `App.tsx`:

```ts
interface Props {
  games: GameCollection[];
  customs: Collection[];
  onScan: () => void;
  onOpenGame: (game: string) => void;
  onOpenCustom: (id: number) => void;
}
```

- [ ] **Step 1: Rewrite `HomeScreen.tsx`**

```tsx
import { Camera, FolderOpen, ImageIcon } from "lucide-react";
import { gameLabel } from "@/lib/utils";
import type { Collection, GameCollection } from "@/types";

interface Props {
  games: GameCollection[];
  customs: Collection[];
  onScan: () => void;
  onOpenGame: (game: string) => void;
  onOpenCustom: (id: number) => void;
}

function fmt(n: number): string {
  return n.toLocaleString("fr-FR");
}

export function HomeScreen({ games, customs, onScan, onOpenGame, onOpenCustom }: Props) {
  const totalCards = games.reduce((s, g) => s + g.card_count, 0);
  const totalFoils = games.reduce((s, g) => s + g.foil_count, 0);
  const trackedGames = games.filter((g) => g.game !== "").length;

  const stats = [
    { value: fmt(totalCards), label: "Cartes" },
    { value: fmt(totalFoils), label: "Foils" },
    { value: fmt(trackedGames), label: "Jeux suivis" },
    { value: fmt(customs.length), label: "Collections custom" },
  ];

  return (
    <div className="h-full overflow-y-auto" style={{ padding: "36px 44px" }}>
      <div className="flex flex-col" style={{ gap: "26px" }}>
        {/* Header */}
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="text-[32px] leading-tight">Ta collection</h2>
            <p className="mt-1 text-sm n-n400">
              {trackedGames} jeu{trackedGames > 1 ? "x" : ""} suivi{trackedGames > 1 ? "s" : ""} —{" "}
              {fmt(totalCards)} carte{totalCards > 1 ? "s" : ""}
            </p>
          </div>
          <button onClick={onScan} className="n-btn n-btn-primary shrink-0">
            <Camera className="w-4 h-4" />
            Scanner des cartes
          </button>
        </div>

        {/* Stats band */}
        <div className="flex flex-wrap gap-x-16 gap-y-6">
          {stats.map((s) => (
            <div key={s.label} className="flex flex-col">
              <span className="text-[40px] font-medium leading-none">{s.value}</span>
              <span
                className="mt-2 n-n400"
                style={{ fontSize: "11px", letterSpacing: "0.08em", textTransform: "uppercase" }}
              >
                {s.label}
              </span>
            </div>
          ))}
        </div>

        <hr className="n-hr" />

        {/* Game tiles */}
        <div className="grid gap-[18px]" style={{ gridTemplateColumns: "repeat(4, minmax(0, 1fr))" }}>
          {games.map((g) => (
            <div key={g.game || "__other"} className="n-tile" onClick={() => onOpenGame(g.game)}>
              <div
                className="flex flex-col items-center justify-center gap-2"
                style={{ height: "280px", background: "var(--n-bg)" }}
              >
                <ImageIcon className="w-6 h-6 n-n600" />
                <span className="text-xs n-n500">{gameLabel(g.game)}</span>
              </div>
              <div style={{ padding: "14px 16px 16px" }} className="flex flex-col gap-2.5">
                <div className="flex items-baseline justify-between gap-2">
                  <span className="text-base font-medium">{gameLabel(g.game)}</span>
                  <span className="text-xs n-n400">{fmt(g.card_count)} cartes</span>
                </div>
                <div className="flex items-center justify-between" style={{ fontSize: "11px" }}>
                  <span className="n-n500">{fmt(g.foil_count)} foils</span>
                </div>
              </div>
            </div>
          ))}
        </div>

        {/* Custom collections */}
        {customs.length > 0 && (
          <>
            <hr className="n-hr" />
            <div className="flex flex-col gap-2">
              <span
                className="n-n500"
                style={{ fontSize: "11px", letterSpacing: "0.08em", textTransform: "uppercase" }}
              >
                Collections custom
              </span>
              <div className="flex flex-wrap gap-2">
                {customs.map((c) => (
                  <button key={c.id} className="n-btn n-btn-secondary" onClick={() => onOpenCustom(c.id)}>
                    <FolderOpen className="w-4 h-4" />
                    {c.name}
                    <span className="n-n500 text-xs">({c.card_count})</span>
                  </button>
                ))}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
```

Design notes: the placeholder "valeur estimée" / completion bar / "scans cette semaine" are dropped (no backing data — spec out of scope). The tile layout, spacing and type sizes are kept identical to the current placeholder version.

- [ ] **Step 2: Verify**

Run from `frontend/`: `npx tsc --noEmit`
Expected: `HomeScreen.tsx` errors gone; `App.tsx` now complains about the new required props — Task 8 fixes it.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/HomeScreen.tsx
git commit -m "feat: home screen shows real per-game and custom collection stats"
```

---

### Task 8: Collection screen (Nocturne), routing, copies panel, cleanup

**Files:**
- Create: `frontend/src/components/CollectionScreen.tsx`
- Create: `frontend/src/components/CardCopiesDialog.tsx`
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/components/Sidebar.tsx`
- Delete: `frontend/src/components/CollectionDetail.tsx`, `frontend/src/components/CollectionBar.tsx`

**Interfaces:**
- Consumes: `GetCards`, `GetCardImageByModel`, `MoveCardToCustom`, `DeleteCard`, `UpdateCard` bindings; `useCollection()` (Task 5); `gameLabel`, `formatDate` from `lib/utils`; `ExportModal` (unchanged: `{open, onOpenChange, presets, onExport}`).
- Produces: `CollectionScreen` with props:

```ts
interface Props {
  collection: ReturnType<typeof useCollection>;
  target: CardFilter;                 // view to show
  onChangeTarget: (f: CardFilter) => void;
  onScan: () => void;
}
```

- [ ] **Step 1: Create `CardCopiesDialog.tsx`**

Per-copy management for one card id (replaces the old CollectionDetail dialog):

```tsx
import { Trash2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatDate } from "@/lib/utils";
import type { Collection, CollectionCard } from "@/types";

interface Props {
  cardId: string | null;              // null = closed
  copies: CollectionCard[];           // copies of that card id
  image: string | null;
  customs: Collection[];
  onClose: () => void;
  onToggleFoil: (copy: CollectionCard) => void;
  onMove: (rowId: number, customId: number) => void;
  onDelete: (rowId: number) => void;
}

export function CardCopiesDialog({
  cardId, copies, image, customs, onClose, onToggleFoil, onMove, onDelete,
}: Props) {
  return (
    <Dialog open={cardId !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle className="font-mono">{cardId} · {copies.length} exemplaire{copies.length > 1 ? "s" : ""}</DialogTitle>
        </DialogHeader>
        <div className="flex gap-4 min-h-0">
          <div
            className="w-40 shrink-0 rounded-lg overflow-hidden self-start"
            style={{ background: "var(--n-surface)" }}
          >
            {image && <img src={image} alt={cardId ?? ""} className="w-full object-contain" />}
          </div>
          <div className="flex-1 overflow-y-auto flex flex-col gap-2">
            {copies.map((c) => (
              <div
                key={c.id}
                className="flex items-center gap-3 rounded-lg p-2"
                style={{ border: "1px solid var(--n-divider)" }}
              >
                <button className={c.foil ? "n-tag n-tag-accent" : "n-tag n-tag-neutral"} onClick={() => onToggleFoil(c)}>
                  {c.foil ? "Foil" : "Normale"}
                </button>
                <span className="text-xs n-n500 flex-1">{formatDate(c.scanned_at)}</span>
                <Select value={String(c.collection_id)} onValueChange={(v) => onMove(c.id, parseInt(v, 10) || 0)}>
                  <SelectTrigger className="w-[160px] h-8 text-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="0">aucune custom</SelectItem>
                    {customs.map((cc) => (
                      <SelectItem key={cc.id} value={String(cc.id)}>{cc.name}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <button className="n-btn n-btn-secondary n-btn-icon" onClick={() => onDelete(c.id)}>
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            ))}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 2: Create `CollectionScreen.tsx`**

Layout from the handoff mock (03-collection.png): title row with view name + count tag + Exporter/Scanner buttons; search field + Toutes/Normales/Foil segmented filter; card grid with FOIL badge, id and ×count under each tile; footer "N cartes · M uniques". View navigation = a chip row (games then customs) instead of the mock's back arrow, since this screen is also the landing page for "Collections".

```tsx
import { useEffect, useMemo, useState } from "react";
import { Camera, ImageIcon, Pencil, Search, Trash2, Upload } from "lucide-react";
import { GetCardImageByModel, GetCards, DeleteCard, MoveCardToCustom, UpdateCard } from "../../wailsjs/go/main/App";
import { LogDebug } from "../../wailsjs/runtime/runtime";
import { gameLabel } from "@/lib/utils";
import { CardCopiesDialog } from "@/components/CardCopiesDialog";
import type { CardFilter, CollectionCard } from "@/types";
import type { useCollection } from "@/hooks/useCollection";

interface Props {
  collection: ReturnType<typeof useCollection>;
  target: CardFilter;
  onChangeTarget: (f: CardFilter) => void;
  onScan: () => void;
}

interface CardGroup {
  card_id: string;
  normal: number;
  foil: number;
  copies: CollectionCard[];
}

type FoilFilter = "all" | "normal" | "foil";

function sameFilter(a: CardFilter, b: CardFilter): boolean {
  return a.game === b.game && !!a.other_bucket === !!b.other_bucket && a.collection_id === b.collection_id;
}

export function CollectionScreen({ collection, target, onChangeTarget, onScan }: Props) {
  const [cards, setCards] = useState<CollectionCard[]>([]);
  const [images, setImages] = useState<Record<string, string>>({});
  const [search, setSearch] = useState("");
  const [foilFilter, setFoilFilter] = useState<FoilFilter>("all");
  const [openCard, setOpenCard] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setCards([]);
    setImages({});
    setOpenCard(null);
    GetCards(target as never)
      .then(async (rows) => {
        if (cancelled) return;
        const list = (rows ?? []) as CollectionCard[];
        setCards(list);
        // One image per distinct card id, loaded after the grid renders.
        const seen = new Map<string, string>();
        for (const c of list) if (!seen.has(c.card_id)) seen.set(c.card_id, c.model_name);
        const imgs: Record<string, string> = {};
        await Promise.all(
          [...seen.entries()].map(([id, model]) =>
            GetCardImageByModel(id, model)
              .then((img: string) => { imgs[id] = img; })
              .catch(() => {})
          )
        );
        if (!cancelled) setImages(imgs);
      })
      .catch((e) => LogDebug(`[CollectionScreen] ${e}`));
    return () => { cancelled = true; };
  }, [target.game, target.other_bucket, target.collection_id]);

  const groups = useMemo(() => {
    const map = new Map<string, CardGroup>();
    for (const c of cards) {
      let g = map.get(c.card_id);
      if (!g) {
        g = { card_id: c.card_id, normal: 0, foil: 0, copies: [] };
        map.set(c.card_id, g);
      }
      if (c.foil) g.foil++; else g.normal++;
      g.copies.push(c);
    }
    let list = [...map.values()].sort((a, b) => a.card_id.localeCompare(b.card_id));
    if (search.trim()) {
      const q = search.trim().toLowerCase();
      list = list.filter((g) => g.card_id.toLowerCase().includes(q));
    }
    if (foilFilter === "normal") list = list.filter((g) => g.normal > 0);
    if (foilFilter === "foil") list = list.filter((g) => g.foil > 0);
    return list;
  }, [cards, search, foilFilter]);

  const title =
    target.collection_id != null && target.collection_id !== 0
      ? collection.customs.find((c) => c.id === target.collection_id)?.name ?? "Collection"
      : gameLabel(target.other_bucket ? "" : target.game ?? "");

  const openGroup = openCard ? groups.find((g) => g.card_id === openCard) ?? null : null;

  async function refreshCards() {
    try {
      const rows = (await GetCards(target as never)) as CollectionCard[] | null;
      setCards(rows ?? []);
    } catch (e) {
      LogDebug(`[CollectionScreen] refresh: ${e}`);
    }
    await collection.refresh();
  }

  return (
    <div className="h-full overflow-y-auto flex flex-col" style={{ padding: "28px 36px", gap: "18px" }}>
      {/* View chips: games, then customs */}
      <div className="flex flex-wrap items-center gap-2">
        {collection.games.map((g) => {
          const f: CardFilter = g.game === "" ? { other_bucket: true } : { game: g.game };
          return (
            <button
              key={g.game || "__other"}
              className="n-tag"
              data-active={sameFilter(target, f)}
              style={sameFilter(target, f) ? { borderColor: "var(--n-accent)", color: "var(--n-accent)" } : undefined}
              onClick={() => onChangeTarget(f)}
            >
              {gameLabel(g.game)} · {g.card_count}
            </button>
          );
        })}
        {collection.customs.map((c) => {
          const f: CardFilter = { collection_id: c.id };
          return (
            <button
              key={`c${c.id}`}
              className="n-tag"
              data-active={sameFilter(target, f)}
              style={sameFilter(target, f) ? { borderColor: "var(--n-accent)", color: "var(--n-accent)" } : undefined}
              onClick={() => onChangeTarget(f)}
            >
              {c.name} · {c.card_count}
            </button>
          );
        })}
      </div>

      {/* Title row */}
      <div className="flex items-center gap-3">
        <h2 className="text-[28px] leading-tight">{title}</h2>
        <span className="n-tag n-tag-neutral">{cards.length} carte{cards.length > 1 ? "s" : ""}</span>
        <div className="ml-auto flex items-center gap-2">
          {target.collection_id != null && target.collection_id !== 0 && (
            <>
              <button
                className="n-btn n-btn-secondary n-btn-icon"
                title="Renommer"
                onClick={async () => {
                  const name = window.prompt("Nouveau nom :", title)?.trim();
                  if (name) await collection.renameCustom(target.collection_id!, name);
                }}
              >
                <Pencil className="w-4 h-4" />
              </button>
              <button
                className="n-btn n-btn-secondary n-btn-icon"
                title="Supprimer la collection (les cartes sont conservées)"
                onClick={async () => {
                  if (!window.confirm(`Supprimer « ${title} » ? Les cartes restent dans leurs jeux.`)) return;
                  await collection.deleteCustom(target.collection_id!);
                  onChangeTarget(collection.games[0]?.game ? { game: collection.games[0].game } : { other_bucket: true });
                }}
              >
                <Trash2 className="w-4 h-4" />
              </button>
            </>
          )}
          <button className="n-btn n-btn-secondary" onClick={() => collection.openExport(target)}>
            <Upload className="w-4 h-4" />
            Exporter
          </button>
          <button className="n-btn n-btn-primary" onClick={onScan}>
            <Camera className="w-4 h-4" />
            Scanner
          </button>
        </div>
      </div>

      {/* Search + foil filter */}
      <div className="flex items-center gap-3">
        <div
          className="flex items-center gap-2 rounded-md px-3 flex-1 max-w-[340px]"
          style={{ background: "var(--n-surface)", border: "1px solid var(--n-divider)", height: "36px" }}
        >
          <Search className="w-4 h-4 n-n500 shrink-0" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Rechercher un ID…"
            className="bg-transparent outline-none text-sm flex-1 min-w-0"
          />
        </div>
        <div className="flex rounded-md overflow-hidden" style={{ border: "1px solid var(--n-divider)" }}>
          {([["all", "Toutes"], ["normal", "Normales"], ["foil", "Foil"]] as const).map(([v, label], i) => (
            <button
              key={v}
              className="n-seg-opt"
              data-active={foilFilter === v}
              style={i > 0 ? { borderLeft: "1px solid var(--n-divider)" } : undefined}
              onClick={() => setFoilFilter(v)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {/* Grid */}
      {groups.length === 0 ? (
        <div className="flex-1 flex items-center justify-center">
          <p className="text-sm n-n500">Aucune carte dans cette vue.</p>
        </div>
      ) : (
        <div className="grid gap-[14px]" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))" }}>
          {groups.map((g) => (
            <div key={g.card_id} className="flex flex-col gap-1.5 cursor-pointer" onClick={() => setOpenCard(g.card_id)}>
              <div
                className="relative rounded-lg overflow-hidden"
                style={{ aspectRatio: "63 / 88", background: "var(--n-surface)" }}
              >
                {images[g.card_id] ? (
                  <img src={images[g.card_id]} alt={g.card_id} className="w-full h-full object-cover" />
                ) : (
                  <div className="w-full h-full flex items-center justify-center">
                    <ImageIcon className="w-5 h-5 n-n600" />
                  </div>
                )}
                {g.foil > 0 && (
                  <span className="absolute top-2 right-2 n-tag n-tag-accent" style={{ fontSize: "10px" }}>
                    FOIL
                  </span>
                )}
              </div>
              <div className="flex items-center justify-between px-0.5">
                <span className="font-mono n-n400" style={{ fontSize: "11px" }}>{g.card_id}</span>
                <span className="n-n500" style={{ fontSize: "11px" }}>×{g.normal + g.foil}</span>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Footer line */}
      {cards.length > 0 && (
        <p className="text-xs n-n500">
          {cards.length} carte{cards.length > 1 ? "s" : ""} · {groups.length} unique{groups.length > 1 ? "s" : ""} — triées par numéro
        </p>
      )}

      <CardCopiesDialog
        cardId={openCard}
        copies={openGroup?.copies ?? []}
        image={openCard ? images[openCard] ?? null : null}
        customs={collection.customs}
        onClose={() => setOpenCard(null)}
        onToggleFoil={async (c) => {
          try {
            await UpdateCard({ ...c, foil: !c.foil } as never);
            await refreshCards();
          } catch (e) {
            LogDebug(`[CollectionScreen] toggle foil: ${e}`);
          }
        }}
        onMove={async (rowId, customId) => {
          try {
            await MoveCardToCustom(rowId, customId);
            await refreshCards();
          } catch (e) {
            LogDebug(`[CollectionScreen] move: ${e}`);
          }
        }}
        onDelete={async (rowId) => {
          try {
            await DeleteCard(rowId);
            await refreshCards();
          } catch (e) {
            LogDebug(`[CollectionScreen] delete: ${e}`);
          }
        }}
      />
    </div>
  );
}
```

Note on `GetCards(target as never)`: the generated binding types its parameter as the `main.CardFilter` class; the structural `CardFilter` interface is compatible at runtime (Wails serializes plain objects). If `tsc` accepts `GetCards(target)` without the cast, drop the casts — try without first.

- [ ] **Step 3: Update `Sidebar.tsx`**

"Collections" becomes a real screen; "Export" navigates there too (export now lives inside each view):

```tsx
import { Camera, Layers, LayoutGrid, Settings, Upload, WalletCards } from "lucide-react";

export type Screen = "home" | "scan" | "collection";

interface Props {
  screen: Screen;
  onNavigate: (s: Screen) => void;
}

export function Sidebar({ screen, onNavigate }: Props) {
  return (
    <aside
      className="flex flex-col shrink-0 w-[216px] border-r"
      style={{ borderColor: "var(--n-divider)", padding: "20px 14px" }}
    >
      <div className="flex items-center gap-2.5 px-2 mb-6">
        <div
          className="flex items-center justify-center w-[30px] h-[30px] rounded-lg"
          style={{ border: "1px solid var(--n-accent)" }}
        >
          <WalletCards className="w-4 h-4 n-brand" />
        </div>
        <span className="text-[15px] font-medium">TCGScanner</span>
      </div>

      <nav className="flex flex-col gap-1">
        <button className="n-nav" data-active={screen === "home"} onClick={() => onNavigate("home")}>
          <LayoutGrid className="w-[18px] h-[18px]" />
          Accueil
        </button>
        <button className="n-nav" data-active={screen === "scan"} onClick={() => onNavigate("scan")}>
          <Camera className="w-[18px] h-[18px]" />
          Scanner
        </button>
        <button className="n-nav" data-active={screen === "collection"} onClick={() => onNavigate("collection")}>
          <Layers className="w-[18px] h-[18px]" />
          Collections
        </button>
        <button className="n-nav" onClick={() => onNavigate("collection")}>
          <Upload className="w-[18px] h-[18px]" />
          Export
        </button>
      </nav>

      <button className="n-nav mt-auto" disabled title="Bientôt disponible">
        <Settings className="w-[18px] h-[18px]" />
        Réglages
      </button>
    </aside>
  );
}
```

- [ ] **Step 4: Update `App.tsx`**

```tsx
import { useState } from "react";
import { useModel } from "@/hooks/useModel";
import { useCollection } from "@/hooks/useCollection";
import { Sidebar, type Screen } from "@/components/Sidebar";
import { HomeScreen } from "@/components/HomeScreen";
import { ScanScreen } from "@/components/ScanScreen";
import { CollectionScreen } from "@/components/CollectionScreen";
import { ExportModal } from "@/components/ExportModal";
import type { CardFilter } from "@/types";

function App() {
  const [screen, setScreen] = useState<Screen>("home");
  const [collectionTarget, setCollectionTarget] = useState<CardFilter>({ game: "pokemon" });
  const model = useModel();
  const collection = useCollection();

  function openGame(game: string) {
    setCollectionTarget(game === "" ? { other_bucket: true } : { game });
    setScreen("collection");
  }

  function openCustom(id: number) {
    setCollectionTarget({ collection_id: id });
    setScreen("collection");
  }

  return (
    <div className="flex h-screen bg-background text-foreground overflow-hidden">
      <Sidebar screen={screen} onNavigate={setScreen} />

      <main className="flex-1 min-w-0 overflow-hidden">
        {screen === "home" && (
          <HomeScreen
            games={collection.games}
            customs={collection.customs}
            onScan={() => setScreen("scan")}
            onOpenGame={openGame}
            onOpenCustom={openCustom}
          />
        )}
        {screen === "scan" && <ScanScreen model={model} collection={collection} />}
        {screen === "collection" && (
          <CollectionScreen
            collection={collection}
            target={collectionTarget}
            onChangeTarget={setCollectionTarget}
            onScan={() => setScreen("scan")}
          />
        )}
      </main>

      <ExportModal
        open={collection.exportTarget !== null}
        onOpenChange={(o) => !o && collection.closeExport()}
        presets={collection.exportPresets}
        onExport={collection.confirmExport}
      />
    </div>
  );
}

export default App;
```

The default `collectionTarget` (`{ game: "pokemon" }`) is only a fallback for direct sidebar navigation; entering from a home tile always sets a real target first. When the pokemon view is empty the screen shows the empty state, which is correct.

- [ ] **Step 5: Delete the replaced components**

```bash
git rm frontend/src/components/CollectionDetail.tsx frontend/src/components/CollectionBar.tsx
```

(`CardForm.tsx`, `CameraPanel.tsx`, `CardPreview.tsx`, `ModelSelector.tsx` were already orphaned before this feature — leave them; not this task's scope. If `tsc` reports errors inside them because they import removed hook members, they DO import `useCollection`… check: only `CollectionBar` did. The others compile standalone.)

- [ ] **Step 6: Full frontend gate**

Run from `frontend/`: `npx tsc --noEmit` → 0 errors anywhere.
Run from `frontend/`: `npm run build` → vite build succeeds.

- [ ] **Step 7: Full backend gate**

Run from repo root: `go test ./... -count=1` → PASS. `go build ./...` → OK.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/App.tsx frontend/src/components/Sidebar.tsx frontend/src/components/CollectionScreen.tsx frontend/src/components/CardCopiesDialog.tsx
git commit -m "feat: nocturne collection screen with game views, customs and copy management"
```

---

### Task 9: Live verification pass (manual, controller-run)

**Files:** none (verification only)

Not a subagent task — the controller runs `wails dev` and walks through with the user:

- [ ] Fresh + migrated DB: start on the existing `collection.db` (a backup copy first: `Copy-Item collection.db collection.db.bak`), check the startup log for `game backfill: N copies resolved`.
- [ ] Home shows real counts per game; tiles open the right view.
- [ ] Scan a card: it lands in its game view; "ajouté à Pokémon" line appears; with a custom selected in "Ajouter aussi à", the copy also shows in that custom's view.
- [ ] Collection screen: search, foil filter, copies dialog (toggle foil, move to custom, delete), rename/delete custom.
- [ ] Export CSV from a game view and from a custom view; open both files.
- [ ] `git status` clean except intended files.

---

## Self-Review (done at plan-writing time)

- **Spec coverage:** schema v2 + migration → Task 1; game resolution + heal-on-start backfill → Tasks 2/4; API surface → Task 4 (spec's `GetActiveCustom` included; `SaveCard` returns the saved copy so the scanner can show the game — a deliberate addition the spec's frontend section requires); scanner selector → Task 6; home tiles → Task 7; collection screen + copies management + export-from-view → Task 8; edge cases (Autres bucket, custom deletion keeps copies, filter validation) → Tasks 1/3/8; tests listed in spec → Tasks 1–4.
- **Deviations from spec (intentional):** `CollectionCard` also gains `collection_id` (the copies dialog needs the current membership); `GameCollection` gains `foil_count` (home stats band). Both are additive.
- **Type consistency:** `CardFilter` JSON keys (`game`, `other_bucket`, `collection_id`) match between Go structs (Task 3), TS interfaces (Task 5) and all call sites (Tasks 6–8); `insertCard` new arity used in Tasks 3/4 and tests; `created_at` used everywhere post-rename.
