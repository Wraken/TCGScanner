# Card Metadata Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show real card metadata (name, set, number, rarity, type, colours) in the card details dialog, sourced per game into the model bundle.

**Architecture:** Each fetcher under `scripts/fetch/<game>/` writes a `cards.json` (card id → common-core fields); `package_app_model.py` copies them to `models/tcg/metadata/<game>.json` and warns about uncovered index labels; Go loads them lazily with the same mtime cache pattern as the images manifest and exposes `GetCardInfo`; the details dialog renders the fields when present and is unchanged otherwise.

**Tech Stack:** Go (net/http, encoding/json), Python 3 (packager, unittest), React/TS (Wails bindings), TCGdex / Lorcast / vegapull-records / Riot gallery data.

**Spec:** `docs/superpowers/specs/2026-09-02-card-metadata-design.md`

## Global Constraints

- UI copy, code, comments, commit messages: **English**. Commit subjects short lowercase imperative; end every commit message with the two trailer lines the controller supplies (Co-Authored-By + Claude-Session).
- `frontend/wailsjs/` is gitignored and regenerated: after any change to bound methods/structs run `wails generate module` from the repo root.
- Windows dev box. `go test ./... -count=1` must stay green; `models/tcg` exists here so real-bundle tests must PASS, not skip, when their data exists.
- Metadata file format (spec): `models/tcg/metadata/<game>.json`, keyed by card id, fields `name, set, set_name, number, rarity, type` (strings, `""` when unknown) and `colors` (string array, `[]` when unknown). Unknown keys ignored by the loader. Sorted keys, 1-space indent.
- Card ids are the index labels exactly (OP: `OP01-006_p3`, `EB01-006_r1`; Riftbound: `ogn-001-298`, `ogn-299-star-298`; Lorcana: `1-207`, `P1-4`; Pokémon: `sv01-020`, `A1-001`). The app never canonicalises ids for metadata lookup.
- Never touch `models/` data by hand except through the packager; never commit anything under `models/` or fetcher `images/` dirs.

---

### Task 1: Go metadata loader, lookup and binding

**Files:**
- Create: `card_metadata.go`
- Create: `card_metadata_test.go`
- Modify: `app.go` (one new bound method)

**Interfaces:**
- Consumes: `cardImageManifest`, `gameResolver`, `allModelImagesDirs`, `manifestGameSegment` from `card_images.go` (existing); test helper `writeTestFile(t, path, data)` from `card_images_test.go`.
- Produces:

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
func modelMetadataDir(modelName string) string                 // ./models/<model>/metadata
func allModelMetadataDirs() []string                           // one per dir under ./models
func cardMetadata(metadataDir, game string) map[string]CardInfo // mtime-cached; empty map when absent/invalid
func lookupCardInfo(imagesDirs, metadataDirs []string, cardID string) (CardInfo, error)
func (a *App) GetCardInfo(cardID string) (CardInfo, error)     // binding
```

- [ ] **Step 1: Write the failing tests** — create `card_metadata_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCardMetadataLoadAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onepiece.json")
	writeTestFile(t, path, `{"OP01-025": {"name": "Zoro", "set": "OP01", "set_name": "Romance Dawn",
		"number": "025", "rarity": "SR", "type": "Character", "colors": ["Red"], "future_field": 1}}`)

	m := cardMetadata(dir, "onepiece")
	c, ok := m["OP01-025"]
	if !ok {
		t.Fatal("OP01-025 missing")
	}
	if c.Name != "Zoro" || c.Set != "OP01" || c.SetName != "Romance Dawn" || c.Number != "025" ||
		c.Rarity != "SR" || c.Type != "Character" || !slices.Equal(c.Colors, []string{"Red"}) {
		t.Errorf("unexpected card: %+v", c)
	}
	// ID/Game are filled by the lookup, never by the file.
	if c.ID != "" || c.Game != "" {
		t.Errorf("loader must not set ID/Game, got %q/%q", c.ID, c.Game)
	}

	// mtime-based reload picks up a rewritten file.
	writeTestFile(t, path, `{"OP01-025": {"name": "Roronoa Zoro"}}`)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if got := cardMetadata(dir, "onepiece")["OP01-025"].Name; got != "Roronoa Zoro" {
		t.Errorf("after rewrite name = %q, want Roronoa Zoro", got)
	}
}

func TestCardMetadataAbsentOrInvalid(t *testing.T) {
	dir := t.TempDir()
	if m := cardMetadata(dir, "pokemon"); len(m) != 0 {
		t.Errorf("absent file should yield an empty map, got %d entries", len(m))
	}
	writeTestFile(t, filepath.Join(dir, "lorcana.json"), `{not json`)
	if m := cardMetadata(dir, "lorcana"); len(m) != 0 {
		t.Errorf("invalid file should yield an empty map, got %d entries", len(m))
	}
	// Hostile game names never leave the metadata dir.
	for _, g := range []string{"", "..", "../x", "a/b"} {
		if m := cardMetadata(dir, g); len(m) != 0 {
			t.Errorf("game %q should yield an empty map", g)
		}
	}
}

func TestLookupCardInfo(t *testing.T) {
	images := t.TempDir()
	meta := t.TempDir()
	writeTestFile(t, filepath.Join(images, "manifest.json"),
		`{"OP01-025": "onepiece/OP01-025.webp", "sv01-020": "pokemon/sv01-020.webp"}`)
	writeTestFile(t, filepath.Join(meta, "onepiece.json"), `{"OP01-025": {"name": "Zoro"}}`)
	writeTestFile(t, filepath.Join(meta, "lorcana.json"), `{"1-1": {"name": "Ariel"}}`)

	// Via the manifest: game resolved, only that file consulted.
	c, err := lookupCardInfo([]string{images}, []string{meta}, "OP01-025")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "OP01-025" || c.Game != "onepiece" || c.Name != "Zoro" {
		t.Errorf("got %+v", c)
	}
	if c.Colors == nil {
		t.Error("Colors must be an empty slice, not nil, so the JSON binding sends []")
	}

	// Not in any manifest: fallback scan over every metadata file present.
	c, err = lookupCardInfo([]string{images}, []string{meta}, "1-1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Game != "lorcana" || c.Name != "Ariel" {
		t.Errorf("fallback lookup got %+v", c)
	}

	// In the manifest but not in its game's file, and nowhere else -> error.
	if _, err := lookupCardInfo([]string{images}, []string{meta}, "sv01-020"); err == nil {
		t.Error("missing metadata should error")
	}
	if _, err := lookupCardInfo([]string{images}, []string{meta}, "nope"); err == nil {
		t.Error("unknown id should error")
	}
}

func TestLookupCardInfoRealBundle(t *testing.T) {
	dir := modelMetadataDir("tcg")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Skip("models/tcg/metadata not present or empty")
	}
	// Whatever game is packaged first, one of its entries must resolve
	// through the real manifests with a non-empty name.
	game := strings.TrimSuffix(entries[0].Name(), ".json")
	m := cardMetadata(dir, game)
	if len(m) == 0 {
		t.Fatalf("%s.json loaded empty", game)
	}
	for id := range m {
		c, err := lookupCardInfo(allModelImagesDirs(), allModelMetadataDirs(), id)
		if err != nil {
			t.Fatalf("lookup %s: %v", id, err)
		}
		if c.Name == "" {
			t.Errorf("%s has an empty name", id)
		}
		break
	}
}
```

- [ ] **Step 2: Run** `go test -run 'TestCardMetadata|TestLookupCardInfo' -count=1 -v .` → compile FAIL (undefined symbols).

- [ ] **Step 3: Create `card_metadata.go`:**

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Card metadata (name, set, rarity...) ships in the model bundle as one JSON
// file per game: models/<model>/metadata/<game>.json, keyed by card id. It
// travels in the mandatory core of the bundle, unlike images, so names are
// available even when a game's image pack is not installed. Loading mirrors
// the images manifest: parsed once per file, re-read when its mtime changes.
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

type cardMetadataFile struct {
	modTime time.Time
	cards   map[string]CardInfo
}

var (
	cardMetadataMu    sync.Mutex
	cardMetadataFiles = map[string]*cardMetadataFile{} // keyed by file path
)

func modelMetadataDir(modelName string) string {
	return filepath.Join(".", "models", modelName, "metadata")
}

// allModelMetadataDirs lists the metadata dir of every model under ./models
// (the dir itself may not exist for older bundles; loads then yield nothing).
func allModelMetadataDirs() []string {
	entries, err := os.ReadDir(filepath.Join(".", "models"))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, modelMetadataDir(e.Name()))
		}
	}
	return dirs
}

// cardMetadata returns the cards of metadataDir/<game>.json. A missing or
// invalid file yields an empty map; the game name is used as a file name, so
// anything that is not a plain name is rejected the same way.
func cardMetadata(metadataDir, game string) map[string]CardInfo {
	if game == "" || game == "." || game == ".." || strings.ContainsAny(game, `/\`) {
		return map[string]CardInfo{}
	}
	path := filepath.Join(metadataDir, game+".json")
	info, statErr := os.Stat(path)
	if statErr != nil {
		return map[string]CardInfo{}
	}

	cardMetadataMu.Lock()
	defer cardMetadataMu.Unlock()

	if cached := cardMetadataFiles[path]; cached != nil && cached.modTime.Equal(info.ModTime()) {
		return cached.cards
	}

	cards := map[string]CardInfo{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cards); err != nil {
			fmt.Printf("bad card metadata %s: %v\n", path, err)
			cards = map[string]CardInfo{}
		}
	}
	cardMetadataFiles[path] = &cardMetadataFile{modTime: info.ModTime(), cards: cards}
	return cards
}

// lookupCardInfo resolves cardID's game through the image manifests and reads
// that game's metadata; ids unknown to every manifest fall back to a scan of
// every metadata file present. ID and Game are always set by this function.
func lookupCardInfo(imagesDirs, metadataDirs []string, cardID string) (CardInfo, error) {
	find := func(game string) (CardInfo, bool) {
		for _, dir := range metadataDirs {
			if c, ok := cardMetadata(dir, game)[cardID]; ok {
				c.ID = cardID
				c.Game = game
				if c.Colors == nil {
					c.Colors = []string{}
				}
				return c, true
			}
		}
		return CardInfo{}, false
	}

	if game := gameResolver(imagesDirs)(cardID); game != "" {
		if c, ok := find(game); ok {
			return c, nil
		}
	}
	// Fallback: every <game>.json in every metadata dir.
	for _, dir := range metadataDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") {
				continue
			}
			if c, ok := find(strings.TrimSuffix(name, ".json")); ok {
				return c, nil
			}
		}
	}
	return CardInfo{}, fmt.Errorf("no metadata for card %q", cardID)
}
```

- [ ] **Step 4: Add the binding** in `app.go`, next to `GetGameCover`:

```go
// GetCardInfo returns the bundle metadata of a card (name, set, rarity...).
// Errors when the bundle has no metadata for it; the details dialog then
// simply shows the id.
func (a *App) GetCardInfo(cardID string) (CardInfo, error) {
	return lookupCardInfo(allModelImagesDirs(), allModelMetadataDirs(), cardID)
}
```

- [ ] **Step 5: Run** `go test -run 'TestCardMetadata|TestLookupCardInfo' -count=1 -v .` → 3 PASS; `TestLookupCardInfoRealBundle` SKIPS until Task 8 packages metadata (expected at this stage). Then `go test ./... -count=1` → PASS.

- [ ] **Step 6: Regenerate bindings:** `wails generate module` from the repo root; confirm `frontend/wailsjs/go/main/App.d.ts` exports `GetCardInfo(arg1:string):Promise<main.CardInfo>` and `models.ts` has `CardInfo`.

- [ ] **Step 7: Commit:**

```bash
git add card_metadata.go card_metadata_test.go app.go
git commit -m "feat: card metadata loader and GetCardInfo binding"
```

---

### Task 2: Details dialog shows metadata

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/components/CollectionScreen.tsx`
- Modify: `frontend/src/components/CardCopiesDialog.tsx`

**Interfaces:**
- Consumes: `GetCardInfo(cardID) → Promise<main.CardInfo>` (Task 1 binding, regenerated).
- Produces: `CardInfo` TS interface; `CardCopiesDialog` prop `info: CardInfo | null`.

- [ ] **Step 1: `types.ts`** — append:

```ts
// Bundle card metadata (models/<model>/metadata/<game>.json). Strings are ""
// and colors [] when the source did not know.
export interface CardInfo {
  id: string;
  game: string;
  name: string;
  set: string;
  set_name: string;
  number: string;
  rarity: string;
  type: string;
  colors: string[];
}
```

- [ ] **Step 2: `CollectionScreen.tsx`** — fetch on open, cached per id for the screen's lifetime. Add `GetCardInfo` to the bindings import and `CardInfo` to the `@/types` import, then add state + effect right after the `openCard` state:

```tsx
  // Metadata of the card shown in the dialog; null = none in the bundle.
  const [infos, setInfos] = useState<Record<string, CardInfo | null>>({});
  useEffect(() => {
    if (!openCard || openCard in infos) return;
    let cancelled = false;
    GetCardInfo(openCard)
      .then((info) => { if (!cancelled) setInfos((prev) => ({ ...prev, [openCard]: info as CardInfo })); })
      .catch(() => { if (!cancelled) setInfos((prev) => ({ ...prev, [openCard]: null })); });
    return () => { cancelled = true; };
  }, [openCard]);
```

and pass it to the dialog: `info={openCard ? infos[openCard] ?? null : null}`.

- [ ] **Step 3: `CardCopiesDialog.tsx`** — add `info: CardInfo | null;` to `Props` (import the type), destructure it, and replace the `<DialogHeader>` block with:

```tsx
        <DialogHeader>
          {info && info.name ? (
            <>
              <DialogTitle>{info.name}</DialogTitle>
              <p className="font-mono text-xs n-n500">
                {cardId} · {copies.length} {copies.length !== 1 ? "copies" : "copy"}
              </p>
            </>
          ) : (
            <DialogTitle className="font-mono">
              {cardId} · {copies.length} {copies.length !== 1 ? "copies" : "copy"}
            </DialogTitle>
          )}
          {info && (
            <div className="flex flex-wrap gap-1.5 pt-1">
              {info.set_name && (
                <span className="n-tag n-tag-neutral">
                  {info.set_name}
                  {info.set && <span className="font-mono n-n500">&nbsp;{info.set}</span>}
                </span>
              )}
              {!info.set_name && info.set && <span className="n-tag n-tag-neutral font-mono">{info.set}</span>}
              {info.number && <span className="n-tag n-tag-neutral font-mono">#{info.number}</span>}
              {info.rarity && <span className="n-tag n-tag-neutral">{info.rarity}</span>}
              {info.type && <span className="n-tag n-tag-neutral">{info.type}</span>}
              {info.colors.map((c) => (
                <span key={c} className="n-tag n-tag-accent">{c}</span>
              ))}
            </div>
          )}
        </DialogHeader>
```

- [ ] **Step 4: Gates** — from `frontend/`: `npx tsc --noEmit` (0 errors) and `npm run build`.

- [ ] **Step 5: Commit:**

```bash
git add frontend/src/types.ts frontend/src/components/CollectionScreen.tsx frontend/src/components/CardCopiesDialog.tsx
git commit -m "feat: card details dialog shows bundle metadata"
```

---

### Task 3: Packager copies metadata and reports coverage

**Files:**
- Modify: `scripts/embeddingtest/package_app_model.py`
- Create: `scripts/embeddingtest/test_package_app_model.py`

**Interfaces:**
- Produces (pure, unit-tested): `metadata_coverage(labels: list[str], manifest: dict[str, str], metadata: dict[str, dict]) -> dict[str, list[str]]` — for every game that has a metadata file, the index labels of that game (per the manifest) missing from it.
- Also produces `fill_variant_fallbacks(labels, manifest, metadata) -> dict[str, int]`: for labels missing from their game's metadata whose base id (variant suffix `_p<N>` / `_r<N>` stripped) IS present, copies the base entry under the variant id (same name/set/rarity — a parallel art of the same card); returns the count added per game. Runs BEFORE the coverage report.
- CLI: `--metadata GAME=FILE` (repeatable, defaults below), `--skip-metadata`.

- [ ] **Step 1: Failing test** — create `scripts/embeddingtest/test_package_app_model.py`:

```python
import unittest

from package_app_model import fill_variant_fallbacks, metadata_coverage


class MetadataCoverageTest(unittest.TestCase):
    def test_missing_labels_per_game(self):
        labels = ["OP01-025", "OP01-026", "sv01-020", "1-1", "weird-1"]
        manifest = {
            "OP01-025": "onepiece/OP01-025.webp",
            "OP01-026": "onepiece/OP01-026.webp",
            "sv01-020": "pokemon/sv01-020.webp",
            "1-1": "lorcana/1-1.webp",
        }
        metadata = {
            "onepiece": {"OP01-025": {"name": "Zoro"}},
            "pokemon": {},
        }
        got = metadata_coverage(labels, manifest, metadata)
        # Only games WITH a metadata file are reported; weird-1 (not in the
        # manifest) and lorcana (no file) are ignored.
        self.assertEqual(got, {"onepiece": ["OP01-026"], "pokemon": ["sv01-020"]})

    def test_variant_fallback_copies_base_entry(self):
        labels = ["OP01-025", "OP01-025_p2", "OP01-025_r1", "OP01-030_p1"]
        manifest = {l: f"onepiece/{l}.webp" for l in labels}
        metadata = {"onepiece": {"OP01-025": {"name": "Zoro", "rarity": "SR"}}}
        added = fill_variant_fallbacks(labels, manifest, metadata)
        self.assertEqual(added, {"onepiece": 2})
        self.assertEqual(metadata["onepiece"]["OP01-025_p2"], {"name": "Zoro", "rarity": "SR"})
        self.assertEqual(metadata["onepiece"]["OP01-025_r1"], {"name": "Zoro", "rarity": "SR"})
        # No base card -> still missing, reported by the coverage check.
        self.assertEqual(metadata_coverage(labels, manifest, metadata), {"onepiece": ["OP01-030_p1"]})


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run** from `scripts/embeddingtest/`: `python -m unittest test_package_app_model -v` → FAIL (ImportError).

- [ ] **Step 3: Implement.** In `package_app_model.py`:

Docstring: add the line
```
  - metadata/<game>.json           : card id -> {name, set, set_name, number,
                                       rarity, type, colors}, copied from each
                                       fetcher's cards.json (core of the bundle)
```

Add after `convert_image`:

```python
def metadata_coverage(labels, manifest, metadata):
    """Index labels of each game (per the manifest) missing from its metadata."""
    by_game = {}
    for label in labels:
        rel = manifest.get(label, "")
        game = rel.split("/", 1)[0] if "/" in rel else ""
        if game in metadata and label not in metadata[game]:
            by_game.setdefault(game, []).append(label)
    return {g: sorted(m) for g, m in by_game.items()}


VARIANT_SUFFIX = re.compile(r"_[pr]\d+$")


def fill_variant_fallbacks(labels, manifest, metadata):
    """Give variant ids (OP01-025_p2) the metadata of their base card when the
    source lacks the variant itself. Mutates `metadata`; returns added per game."""
    added = {}
    for label in labels:
        rel = manifest.get(label, "")
        game = rel.split("/", 1)[0] if "/" in rel else ""
        if game not in metadata or label in metadata[game]:
            continue
        base = VARIANT_SUFFIX.sub("", label)
        if base != label and base in metadata[game]:
            metadata[game][label] = dict(metadata[game][base])
            added[game] = added.get(game, 0) + 1
    return added
```

(add `import re` to the module imports)

Add the CLI options next to `--images`:

```python
    ap.add_argument("--metadata", nargs="+", metavar="GAME=FILE",
                    default=["onepiece=../fetch/onepiece/cards.json",
                             "riftbound=../fetch/riftbound/cards.json",
                             "lorcana=../fetch/lorcana/cards.json",
                             "pokemon=../fetch/pokemon/cards.json"],
                    help="card metadata per game (fetcher cards.json); missing files are skipped")
    ap.add_argument("--skip-metadata", action="store_true")
```

Add a step 5 after the images block (before `print("done.")`), which also works with `--skip-images` by reading the existing manifest from `dest`:

```python
    # 5. metadata — one file per game, kept next to (not under) images/ so it
    #    ships in the bundle core even when a game's image pack is absent.
    if not args.skip_metadata:
        meta_dest = dest / "metadata"
        meta_dest.mkdir(exist_ok=True)
        metadata = {}
        for spec in args.metadata:
            game, _, f = spec.partition("=")
            if not game or not f:
                ap.error(f"--metadata expects GAME=FILE, got {spec!r}")
            src_file = Path(f)
            if not src_file.exists():
                print(f"  metadata: {game}: {src_file} not found, skipped")
                continue
            cards = json.loads(src_file.read_text(encoding="utf-8"))
            (meta_dest / f"{game}.json").write_text(
                json.dumps(cards, indent=1, sort_keys=True, ensure_ascii=False), encoding="utf-8")
            metadata[game] = cards
            print(f"  metadata: {game}: {len(cards)} cards")
        manifest_path = dest / "images" / "manifest.json"
        manifest = json.loads(manifest_path.read_text()) if manifest_path.exists() else {}
        for game, n in fill_variant_fallbacks(labels, manifest, metadata).items():
            (meta_dest / f"{game}.json").write_text(
                json.dumps(metadata[game], indent=1, sort_keys=True, ensure_ascii=False), encoding="utf-8")
            print(f"  metadata: {game}: {n} variant ids filled from their base card")
        for game, missing in metadata_coverage(labels, manifest, metadata).items():
            print(f"  WARNING: {game}: {len(missing)} index labels without metadata "
                  f"(e.g. {missing[:5]})")
```

- [ ] **Step 4: Run** the unit test → PASS. Confirm the module still imports without the heavy deps at top level: from `scripts/embeddingtest/`, `python -c "import package_app_model"`.

- [ ] **Step 5: Commit:**

```bash
git add scripts/embeddingtest/package_app_model.py scripts/embeddingtest/test_package_app_model.py
git commit -m "feat: package card metadata per game with coverage warnings"
```

---

### Task 4: One Piece metadata fetcher (punk-records)

**Files:**
- Create: `scripts/fetch/onepiece/main.go`
- Create: `scripts/fetch/onepiece/README.md`
- Create: `scripts/fetch/onepiece/.gitignore` (content: `cache/`)

**Interfaces:**
- Produces: `scripts/fetch/onepiece/cards.json` in the common format. Source: **punk-records** (github.com/buhbbl/punk-records, a vegapull-generated dataset refreshed regularly — generated 2026-08-28 at plan time, OP-01→OP-17, EB-01→EB-04, PRB-01/02, ST-01→ST-36, promos), two raw files:
  - `english/index/cards_by_id.json` — object keyed by card id (variant ids such as `EB01-001_p1` are their own keys): `{card_id, name, rarity, category, colors, pack_id, cost, power, counter, types, attributes, keywords, img_url}`.
  - `english/packs.json` — **object** keyed by pack id: `{id, raw_title, title_parts: {prefix, title, label}}`; `title_parts` fields may be null and labels are occasionally odd (`OP14-EB04`), so set names use a label map first and the card's own `pack_id` as fallback.
  (The older `Coko7/vegapull-records` stops at OP-10 — do not use it.)

- [ ] **Step 1: Write `main.go`** (pure download + transform; no images — the SAMPLE-watermarked images stay sourced as today):

```go
// One Piece card metadata fetcher (punk-records dataset).
//
// Reads the English dataset of github.com/buhbbl/punk-records (generated with
// vegapull from the official card list) and writes cards.json in the app's
// common metadata format. Card ids in the dataset already carry the variant
// suffixes (OP01-006_p3, EB01-006_r1) used by our index labels.
//
// Usage:
//
//	go run .            # download the two source files into cache/, write cards.json
//	go run . -offline   # rebuild cards.json from cache/ without downloading
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	rawBase  = "https://raw.githubusercontent.com/buhbbl/punk-records/main/english"
	cacheDir = "cache"
	outFile  = "cards.json"
)

type pack struct {
	ID         string `json:"id"`
	RawTitle   string `json:"raw_title"`
	TitleParts *struct {
		Title string `json:"title"`
		Label string `json:"label"` // "OP-01", "EB-02", "ST-01", "PRB-01"
	} `json:"title_parts"`
}

type sourceCard struct {
	Name     string   `json:"name"`
	Rarity   string   `json:"rarity"`
	Category string   `json:"category"`
	Colors   []string `json:"colors"`
	PackID   string   `json:"pack_id"`
}

// meta is the app's common metadata record (see the card metadata spec).
type meta struct {
	Name    string   `json:"name"`
	Set     string   `json:"set"`
	SetName string   `json:"set_name"`
	Number  string   `json:"number"`
	Rarity  string   `json:"rarity"`
	Type    string   `json:"type"`
	Colors  []string `json:"colors"`
}

var client = &http.Client{Timeout: 120 * time.Second}

// fetch downloads rel (a path under rawBase) into cache/ unless offline, then
// returns its bytes.
func fetch(rel string, offline bool) ([]byte, error) {
	path := filepath.Join(cacheDir, filepath.Base(rel))
	if offline {
		return os.ReadFile(path)
	}
	resp, err := client.Get(rawBase + "/" + rel)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, rel)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, os.WriteFile(path, data, 0o644)
}

// setCode turns a pack label ("OP-01") into the id prefix ("OP01").
func setCode(label string) string {
	return strings.ReplaceAll(label, "-", "")
}

// split returns the set prefix and the number of an id: "OP01-006_p3" ->
// "OP01", "006" (the variant suffix is dropped from the number only).
func split(id string) (set, number string) {
	set, rest, _ := strings.Cut(id, "-")
	number, _, _ = strings.Cut(rest, "_")
	return set, number
}

func main() {
	offline := flag.Bool("offline", false, "rebuild from cache/ without downloading")
	flag.Parse()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		fmt.Println("cannot create cache dir:", err)
		os.Exit(1)
	}

	data, err := fetch("packs.json", *offline)
	if err != nil {
		fmt.Println("packs.json:", err)
		os.Exit(1)
	}
	var packs map[string]pack
	if err := json.Unmarshal(data, &packs); err != nil {
		fmt.Println("packs.json:", err)
		os.Exit(1)
	}
	byLabel := map[string]string{} // "OP01" -> "ROMANCE DAWN"
	byPack := map[string]string{}  // "569001" -> "Straw Hat Crew"
	for id, p := range packs {
		if p.TitleParts == nil {
			continue
		}
		if p.TitleParts.Title != "" {
			byPack[id] = p.TitleParts.Title
		}
		if p.TitleParts.Label != "" && p.TitleParts.Title != "" {
			byLabel[setCode(p.TitleParts.Label)] = p.TitleParts.Title
		}
	}

	data, err = fetch("index/cards_by_id.json", *offline)
	if err != nil {
		fmt.Println("cards_by_id.json:", err)
		os.Exit(1)
	}
	var source map[string]sourceCard
	if err := json.Unmarshal(data, &source); err != nil {
		fmt.Println("cards_by_id.json:", err)
		os.Exit(1)
	}

	cards := make(map[string]meta, len(source))
	noSetName := 0
	for id, c := range source {
		set, number := split(id)
		setName, ok := byLabel[set]
		if !ok {
			setName = byPack[c.PackID] // odd labels such as "OP14-EB04"
		}
		if setName == "" {
			noSetName++
		}
		colors := c.Colors
		if colors == nil {
			colors = []string{}
		}
		cards[id] = meta{
			Name: c.Name, Set: set, SetName: setName, Number: number,
			Rarity: c.Rarity, Type: c.Category, Colors: colors,
		}
	}

	// encoding/json sorts map keys, so the file is deterministic and diff-friendly.
	out, err := os.Create(outFile)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer out.Close()
	enc := json.NewEncoder(out)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cards); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Done: %d cards in %s (%d packs, %d cards without a set name)\n", len(cards), outFile, len(packs), noSetName)
}
```

- [ ] **Step 2: Run it:** `cd scripts/fetch/onepiece && go run .` — expect `Done: ~4800 cards`. Then verify coverage against the index:

```bash
python -c "
import json; c=json.load(open('cards.json')); m=json.load(open('../../../models/tcg/images/manifest.json'))
op=[l for l,p in m.items() if p.startswith('onepiece/') or l.startswith('PRB')]
miss=[l for l in op if l not in c]; print(len(op),'OP labels,',len(miss),'missing, e.g.',miss[:10])"
```

Expected at plan time: ~167 missing, all variant ids (`OP01-006_p2`, `EB01-014_p2`…) whose base card exists — the packager's base-id fallback (Task 3) covers them. Report the exact count; do not hand-edit cards.json. (`PRB*` labels are One Piece cards that the current app manifest still files under riftbound until the next packaging — hence the `or` in the filter.)

- [ ] **Step 3: README.md** — short: the source (punk-records, refreshed by its maintainer; run `go run .` again to pick up new sets), the two commands, the id-suffix note, that images are NOT fetched here, and that `Coko7/vegapull-records` is stale (OP-10) and must not be used.

- [ ] **Step 4: Commit** (cards.json is committed; cache/ is ignored):

```bash
git add scripts/fetch/onepiece/main.go scripts/fetch/onepiece/README.md scripts/fetch/onepiece/.gitignore scripts/fetch/onepiece/cards.json
git commit -m "feat: one piece metadata fetcher from punk-records"
```

---

### Task 5: Lorcana fetcher writes cards.json

**Files:**
- Modify: `scripts/fetch/lorcana/main.go`
- Modify: `scripts/fetch/lorcana/README.md` (one paragraph)

**Interfaces:**
- Produces: `scripts/fetch/lorcana/cards.json`. Lorcast per-set cards already carry `name`, `version` (subtitle, e.g. "On Human Legs"), `rarity`, `ink`, `inks` (multi-ink cards), `type` (array), `collector_number`, `set{code,name}`.

- [ ] **Step 1: Extend the `card` struct** (keep `ImageURIs`):

```go
type card struct {
	CollectorNumber string   `json:"collector_number"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Rarity          string   `json:"rarity"`
	Ink             string   `json:"ink"`
	Inks            []string `json:"inks"`
	Type            []string `json:"type"`
	Set             struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"set"`
	ImageURIs struct {
		Digital map[string]string `json:"digital"`
	} `json:"image_uris"`
}

// meta is the app's common metadata record (see the card metadata spec).
type meta struct {
	Name    string   `json:"name"`
	Set     string   `json:"set"`
	SetName string   `json:"set_name"`
	Number  string   `json:"number"`
	Rarity  string   `json:"rarity"`
	Type    string   `json:"type"`
	Colors  []string `json:"colors"`
}

const metaFile = "cards.json"
```

- [ ] **Step 2: Add a `-meta` mode.** New functions:

```go
// cardID mirrors the image file naming: <set>-<collector_number> with "/"
// replaced ("1-207", "P1-4"), so metadata keys equal index labels.
func cardID(setCode, collectorNumber string) string {
	return setCode + "-" + strings.ReplaceAll(collectorNumber, "/", "-")
}

func fetchMeta(sets []string) error {
	cards := map[string]meta{}
	for _, code := range sets {
		var list []card
		if err := getJSON(fmt.Sprintf("%s/sets/%s/cards", apiBase, code), &list); err != nil {
			fmt.Printf("  %s: set fetch failed: %v\n", code, err)
			continue
		}
		for _, c := range list {
			name := c.Name
			if c.Version != "" {
				name += " - " + c.Version
			}
			colors := c.Inks
			if len(colors) == 0 && c.Ink != "" {
				colors = []string{c.Ink}
			}
			if colors == nil {
				colors = []string{}
			}
			cards[cardID(code, c.CollectorNumber)] = meta{
				Name: name, Set: code, SetName: c.Set.Name, Number: c.CollectorNumber,
				Rarity: c.Rarity, Type: strings.Join(c.Type, " / "), Colors: colors,
			}
		}
		fmt.Printf("  %s: %d cards\n", code, len(list))
		time.Sleep(delay)
	}
	out, err := os.Create(metaFile)
	if err != nil {
		return err
	}
	defer out.Close()
	enc := json.NewEncoder(out)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cards); err != nil {
		return err
	}
	fmt.Printf("Done: %d cards in %s\n", len(cards), metaFile)
	return nil
}
```

Use `cardID` in `fetchSet` too (replace the inline `id := setCode + "-" + ...` line) so both modes share one rule. In `main`, add `metaFlag := flag.Bool("meta", false, "write cards.json (metadata) instead of downloading images")` and, right after the `sets` slice is resolved (before `os.MkdirAll(imagesDir...)`):

```go
	if *metaFlag {
		if err := fetchMeta(sets); err != nil {
			fmt.Println("metadata failed:", err)
			os.Exit(1)
		}
		return
	}
```

- [ ] **Step 3: Build and run:** `cd scripts/fetch/lorcana && go build ./... && go run . -meta` (~22 sets, a few seconds). Coverage check:

```bash
python -c "
import json; c=json.load(open('cards.json')); m=json.load(open('../../../models/tcg/images/manifest.json'))
lo=[l for l,p in m.items() if p.startswith('lorcana/')]
miss=[l for l in lo if l not in c]; print(len(lo),'labels,',len(miss),'missing, e.g.',miss[:10])"
```

Expected: 0 missing (both modes derive ids through `cardID`).

- [ ] **Step 4: README** — add a "Metadata" paragraph with the `-meta` command and the `name - version` convention.

- [ ] **Step 5: Commit:**

```bash
git add scripts/fetch/lorcana/main.go scripts/fetch/lorcana/README.md scripts/fetch/lorcana/cards.json
git commit -m "feat: lorcana fetcher writes card metadata"
```

---

### Task 6: Pokémon fetcher writes cards.json (per-card detail, resumable)

**Files:**
- Modify: `scripts/fetch/pokemon/main.go`
- Modify: `scripts/fetch/pokemon/README.md` (one paragraph)

**Interfaces:**
- Produces: `scripts/fetch/pokemon/cards.json`. TCGdex `GET /v2/en/cards/<id>` returns `{id, localId, name, category ("Pokemon"|"Trainer"|"Energy"), rarity, types [..] (Pokémon only), set {id, name}, ...}`. The per-set listing only has id/localId/name/image, hence one request per card (~21.8k at 80 ms ≈ 30 min) — the run is resumable: existing entries in cards.json are skipped and the file is flushed every 200 cards.

- [ ] **Step 1: Add types** next to the existing ones:

```go
type cardDetail struct {
	ID       string   `json:"id"`
	LocalID  string   `json:"localId"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Rarity   string   `json:"rarity"`
	Types    []string `json:"types"`
	Set      struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"set"`
}

// meta is the app's common metadata record (see the card metadata spec).
type meta struct {
	Name    string   `json:"name"`
	Set     string   `json:"set"`
	SetName string   `json:"set_name"`
	Number  string   `json:"number"`
	Rarity  string   `json:"rarity"`
	Type    string   `json:"type"`
	Colors  []string `json:"colors"`
}

const (
	metaFile   = "cards.json"
	flushEvery = 200
)
```

- [ ] **Step 2: Add the `-meta` mode:**

```go
func loadMeta() map[string]meta {
	cards := map[string]meta{}
	if data, err := os.ReadFile(metaFile); err == nil {
		_ = json.Unmarshal(data, &cards)
	}
	return cards
}

// saveMeta writes atomically (tmp + rename) so an interrupted flush never
// leaves a truncated cards.json behind.
func saveMeta(cards map[string]meta) error {
	tmp := metaFile + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cards); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, metaFile)
}

// fetchMeta fills cards.json for every card of the given sets, one detail
// request per card (the set listing lacks rarity/category/types). Resumable:
// ids already present are skipped, progress is flushed every flushEvery cards.
func fetchMeta(lang string, sets []string) error {
	cards := loadMeta()
	fmt.Printf("%d cards already in %s\n", len(cards), metaFile)
	pending := 0
	for _, setID := range sets {
		var detail setDetail
		if err := getJSON(fmt.Sprintf("%s/%s/sets/%s", apiBase, lang, setID), &detail); err != nil {
			fmt.Printf("  %s: set fetch failed: %v\n", setID, err)
			continue
		}
		added := 0
		for _, c := range detail.Cards {
			if _, ok := cards[c.ID]; ok {
				continue
			}
			var d cardDetail
			if err := getJSON(fmt.Sprintf("%s/%s/cards/%s", apiBase, lang, c.ID), &d); err != nil {
				fmt.Printf("    %s: %v\n", c.ID, err)
				time.Sleep(delay)
				continue
			}
			colors := d.Types
			if colors == nil {
				colors = []string{}
			}
			cards[c.ID] = meta{
				Name: d.Name, Set: d.Set.ID, SetName: d.Set.Name, Number: d.LocalID,
				Rarity: d.Rarity, Type: d.Category, Colors: colors,
			}
			added++
			pending++
			if pending >= flushEvery {
				if err := saveMeta(cards); err != nil {
					return err
				}
				pending = 0
			}
			time.Sleep(delay)
		}
		fmt.Printf("  %s: %d new (%d cards)\n", setID, added, len(detail.Cards))
	}
	if err := saveMeta(cards); err != nil {
		return err
	}
	fmt.Printf("Done: %d cards in %s\n", len(cards), metaFile)
	return nil
}
```

In `main`: `metaFlag := flag.Bool("meta", false, "write cards.json (metadata) instead of downloading images")`, and after `sets` is resolved (before `os.MkdirAll(imagesDir...)`):

```go
	if *metaFlag {
		if err := fetchMeta(*lang, sets); err != nil {
			fmt.Println("metadata failed:", err)
			os.Exit(1)
		}
		return
	}
```

- [ ] **Step 3: Build + smoke on one set:** `cd scripts/fetch/pokemon && go build ./... && go run . -meta -sets sv01` (≈ 258 cards, ~30 s). Check `cards.json` has `sv01-020` = "Smoliv", set_name "Scarlet & Violet", type "Pokemon", colors ["Grass"], rarity "Common". Do NOT run the full catalog in this task (Task 8 does; ~30 min).

- [ ] **Step 4: README** paragraph ("Metadata": the command, resumability, runtime).

- [ ] **Step 5: Commit** (commit the partial `cards.json` from the smoke run — Task 8 completes it):

```bash
git add scripts/fetch/pokemon/main.go scripts/fetch/pokemon/README.md scripts/fetch/pokemon/cards.json
git commit -m "feat: pokemon fetcher writes card metadata (resumable)"
```

---

### Task 7: Riftbound fetcher writes cards.json from an enriched parsedcards.json

**Files:**
- Modify: `scripts/fetch/riftbound/main.go`
- Modify: `scripts/fetch/riftbound/README.md`

**Interfaces:**
- Consumes: `parsedcards.json` entries, extended by the user's manual extraction with optional `name`, `rarity`, `domains` (string array — Riftbound's colours), `type`. Entries without `name` are skipped by `-meta` (today's file has none, so the first run writes an empty object — expected until the user redoes the extraction).
- Produces: `scripts/fetch/riftbound/cards.json`.

- [ ] **Step 1: Extend `Card` and add the metadata writer:**

```go
type Card struct {
	ID         string   `json:"id"`
	PublicCode string   `json:"publicCode"`
	ImageURL   string   `json:"imageUrl"`
	Name       string   `json:"name,omitempty"`
	Rarity     string   `json:"rarity,omitempty"`
	Domains    []string `json:"domains,omitempty"`
	Type       string   `json:"type,omitempty"`
}

// meta is the app's common metadata record (see the card metadata spec).
type meta struct {
	Name    string   `json:"name"`
	Set     string   `json:"set"`
	SetName string   `json:"set_name"`
	Number  string   `json:"number"`
	Rarity  string   `json:"rarity"`
	Type    string   `json:"type"`
	Colors  []string `json:"colors"`
}

const metaFile = "cards.json"

// setNames maps the lowercase set prefix of an id to its printed name.
// Extend when a new set ships (unknown prefixes get an empty set_name).
var setNames = map[string]string{
	"ogn": "Origins",
	"ogs": "Origins",
	"sfd": "Spiritforged",
	"unl": "Unleashed",
	"ven": "Vendetta",
}

// split returns the set prefix and number of an id: "ogn-299-star-298" ->
// "ogn", "299"; "PRB02-003_p1" -> "PRB02", "003".
func split(id string) (set, number string) {
	set, rest, _ := strings.Cut(id, "-")
	number, _, _ = strings.Cut(rest, "-")
	number, _, _ = strings.Cut(number, "_")
	return set, number
}

func writeMeta(cards []Card) error {
	out := map[string]meta{}
	for _, c := range cards {
		if c.Name == "" {
			continue
		}
		set, number := split(c.ID)
		colors := c.Domains
		if colors == nil {
			colors = []string{}
		}
		out[c.ID] = meta{
			Name: c.Name, Set: strings.ToUpper(set), SetName: setNames[strings.ToLower(set)],
			Number: number, Rarity: c.Rarity, Type: c.Type, Colors: colors,
		}
	}
	f, err := os.Create(metaFile)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return err
	}
	fmt.Printf("Done: %d cards with metadata in %s (%d entries had no name)\n", len(out), metaFile, len(cards)-len(out))
	return nil
}
```

Add `"flag"` to the imports. Restructure the top of `main` so flags parse first, then the file loads, then the `-meta` early return — the image download loop below stays byte-for-byte:

```go
func main() {
	metaFlag := flag.Bool("meta", false, "write cards.json (metadata) from parsedcards.json instead of downloading images")
	flag.Parse()
	start := time.Now()

	data, err := os.ReadFile(inputJSON)
	if err != nil {
		panic(err)
	}
	var cards []Card
	if err := json.Unmarshal(data, &cards); err != nil {
		panic(err)
	}
	fmt.Printf("Loaded %d cards\n", len(cards))

	if *metaFlag {
		if err := writeMeta(cards); err != nil {
			panic(err)
		}
		return
	}
	// ... existing image download code unchanged from here ...
```

- [ ] **Step 2: README** — extend the "To refresh / add a NEW SET" instructions: when extracting the card array from the gallery JS chunk, keep for every card `name`, `rarity`, `domains` (array) and `type` alongside `id/publicCode/imageUrl`; document `go run . -meta`; state that today's `parsedcards.json` carries no names yet, so `cards.json` stays empty until the next extraction; note the `setNames` map to maintain (unknown prefixes get an empty set_name). PRB01/PRB02 are NOT Riftbound (One Piece premium boosters, moved to the onepiece images on 2026-09-02).

- [ ] **Step 3: Build + run:** `cd scripts/fetch/riftbound && go build ./... && go run . -meta` → `Done: 0 cards with metadata ... (1189 entries had no name)` and a `cards.json` containing `{}`. Confirm `go run .` (image mode) still skips the existing images exactly as before (Ctrl-C after a few lines is fine).

- [ ] **Step 4: Commit:**

```bash
git add scripts/fetch/riftbound/main.go scripts/fetch/riftbound/README.md scripts/fetch/riftbound/cards.json
git commit -m "feat: riftbound fetcher writes card metadata from parsedcards.json"
```

---

### Task 8: Fetch, package, verify (controller + user)

Not a subagent task. In order:

- [ ] Run the full Pokémon metadata fetch: `cd scripts/fetch/pokemon && go run . -meta` (~30 min, resumable; re-run if interrupted). Commit the completed `cards.json` (`chore: pokemon metadata catalog`).
- [ ] (User) Redo the Riftbound gallery extraction keeping name/rarity/domains/type, replace `parsedcards.json`, run `go run . -meta`, commit both files. Can happen later — everything else works without it.
- [ ] Package: `cd scripts/embeddingtest && python package_app_model.py --skip-images` (images are up to date; the metadata step runs regardless). Read the coverage lines: onepiece and lorcana must report 0 missing (or the exact stragglers), pokemon 0 after the full fetch, riftbound "not found, skipped" or 0 cards until the extraction is redone.
- [ ] `go test ./... -count=1` — `TestLookupCardInfoRealBundle` must now PASS (not skip).
- [ ] `wails dev`: open a Pokémon, a Lorcana and a One Piece card from the collection screen — name as title, id subtitle, tags row; open a Riftbound card — dialog unchanged (no metadata yet).
- [ ] Update the distribution memory note: `metadata/` is part of `core.zip`.
- [ ] The packaging run also regenerates the manifest with the 39 `PRB01/PRB02` images now under `images/onepiece/` (they were misfiled under riftbound) — confirm they show under One Piece in the app afterwards.
- [ ] Out of scope, follow-up: OP15–OP17 (and any newer sets) are NOT in the index yet — that is an index refresh (fetch the new images, `add_to_index.py`, repackage), not metadata.

---

## Self-Review (done at plan-writing time)

- **Spec coverage:** format + location → Tasks 1/3; sources per game → Tasks 4–7 (OP vegapull, Lorcana Lorcast fields, Pokémon per-card detail, Riftbound extended extraction); packager copy + coverage warning → Task 3; Go loader/lookup/binding + edge cases (absent dir, missing id, hostile game, alt-art ids untouched) → Task 1; dialog rendering with/without info → Task 2; tests listed in the spec → Tasks 1, 3; manual pass → Task 8.
- **Placeholders:** none; every code step is complete. Task 7's `setNames` intentionally leaves unknown Riftbound set names empty — documented, user-maintained.
- **Type consistency:** `CardInfo` JSON tags (Task 1) = TS `CardInfo` (Task 2) = fetcher `meta` structs (Tasks 4–7) = packager expectations (Task 3). `GetCardInfo` is named identically in Go, `App.d.ts` and the CollectionScreen import. Fetcher id derivations mirror their image-file naming so keys equal index labels (verified for OP against vegapull ids and for Lorcana via the shared `cardID` rule).
