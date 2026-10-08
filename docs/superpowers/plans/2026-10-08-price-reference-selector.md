# Price Reference Selector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner settle, in the app and with the card in hand, which Cardmarket product an unresolved card id is, from candidates the mapper ships with CardTrader's scans.

**Architecture:** The mapper (`scripts/fetch/cardmarket`, Go) gains a pixel tie-break script that settles the different-artwork cases into `visual.json`, and ships the still-ambiguous ids as `candidates` (product, non-English twin, label, scan URL) in `cardmarket.json`. The desktop app (Wails, Go + React) stores one chosen product per card id in a `price_refs` table, consults it before the bundle mapping, exposes bindings to list/choose/clear candidates and to fetch a candidate's scan through a local cache, and shows a warning chip, a chooser dialog, and labels in the scan duplicate selector, delivered in that order.

**Tech Stack:** Go 1.26 (app and mapper), SQLite via `modernc.org/sqlite`, Wails v2 bindings (`frontend/wailsjs`, regenerated with `wails generate module`), React + TypeScript + Vite (`frontend/`, `npm run build` = `tsc && vite build`), Python 3 stdlib + numpy + Pillow for the pixel tie-break (Windows python, no GPU).

**Spec:** `docs/superpowers/specs/2026-10-08-price-reference-selector-design.md`

## Global Constraints

- `cardmarket.json` keeps `"version": 1`; `candidates` is optional and readers treat a missing field as empty. An id is in `cards` or in `candidates`, never both.
- Candidate `label` is `<version or "Plain"> (<collector number>) · <CardTrader expansion name>`; `image` is the full URL `https://cardtrader.com` + the blueprint's `image.url`, `""` when none.
- The app accepts only `https://cardtrader.com/uploads/` image URLs, fetches one at a time with an explicit User-Agent, caches under `./prices/images/`, and shows "no image" on failure.
- Pixel tie-break thresholds: floor 0.7, margin 0.15; two ids of one base card in one pack picking the same printing are both dropped.
- UI strings are English; styling uses the Nocturne tokens and the existing `n-*` classes, no new UI kit.
- Price references are per card id, stored in `price_refs`; the manual picks, rule C and the tie-breaks stay in front of this feature.
- Delivery order of the app work: (1) chip, (2) chooser, (3) labels in the duplicate selector, each its own commit.
- Line endings: the repo stores LF; the Windows working tree is CRLF (`core.autocrlf`). Edit files in place; do not convert them.
- Commits end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.

---

## File structure

Mapper (`scripts/fetch/cardmarket/`):
- Create `pixel_tiebreak.py` — the pixel tie-break: report + cached scans → `visual.json` + `pixel_status.json`.
- Create `test_pixel_tiebreak.py` — tests of its pure parts.
- Modify `resolve.go` — `candidateView`, `candidateViews`, `twinOf` (shared with `nonEnglishTwins`), `gameResult.Candidates`.
- Modify `sources.go` — `ctBlueprint.Image`.
- Modify `main.go` — `mapping.Candidates`, write/delete per game, summary line.
- Modify `resolve_test.go` — tests of the candidate views.
- Modify `review/review.py`, `review/index.html`, `review/test_review.py` — queue order and status badge from `pixel_status.json`.
- Modify `README.md` — the tie-break and the candidates.

App, Go (repo root):
- Modify `db.go` — `price_refs` table in `schemaV2`.
- Create `price_refs.go` — `PriceRef`, `getPriceRefs`, `setPriceRef`, `clearPriceRef`.
- Create `price_refs_test.go`.
- Modify `prices.go` — `PriceCandidate` in the mapping, `CardPrice.Ref/Candidates`, `cardPrice` precedence, bindings `GetPriceCandidates`, `SetPriceRef`, `ClearPriceRef`, `GetPriceRefs`, `GetPriceCandidateImage`.
- Modify `prices_test.go`.
- Modify `app.go` — the `priceImageAllowFn` test hook on `App`.

App, frontend (`frontend/src/`):
- Modify `types.ts` — `CardPrice.ref/candidates`, `PriceCandidateView`, `PriceRef`.
- Modify `lib/prices.ts` — `referenceState`.
- Modify `style.css` — the `--n-warn` token.
- Modify `components/CollectionScreen.tsx` — the tile chip.
- Modify `components/CardCopiesDialog.tsx` — the header chip and the "Set reference" action.
- Create `components/PriceReferenceDialog.tsx` — the chooser.
- Modify `components/VariantChoiceDialog.tsx` — the label line.
- Regenerated: `frontend/wailsjs/go/main/App.d.ts`, `App.js`, `models.ts`.

Docs: `STATUS.md`.

---

### Task 1: Pixel tie-break script

**Files:**
- Create: `scripts/fetch/cardmarket/pixel_tiebreak.py`
- Create: `scripts/fetch/cardmarket/test_pixel_tiebreak.py`

**Interfaces:**
- Consumes: `review/review.py` — `read_ambiguous(text, prefixes)`, `image_url(blueprint)`, `base_number(num)`, `GAME_PREFIXES`, `CARDTRADER_GAME`, `IMAGES` (the shared `cache/images/` dir); the mapper's `report.txt`, `cache/cardtrader_expansions.json`, `cache/cardtrader_blueprints_<id>.json`; `../onepiece/cards.json` (`pack` per id); reference images `scripts/ml/images/<game>/<id>.png`.
- Produces: `visual.json` `{"floor": 0.7, "margin": 0.15, "picks": {id: {"product": int, "score": float, "lead": float}}}` (the format `loadVisual` in `sources.go` reads); `pixel_status.json` `{id: "settled" | "tied" | "twin" | "low" | "no image"}`; functions `decide(scored, floor, margin) -> (status, best)` and `twin_rule(picks, pack_of) -> (kept, twins)`.

- [ ] **Step 1: Write the failing tests**

```python
# scripts/fetch/cardmarket/test_pixel_tiebreak.py
"""python -m unittest test_pixel_tiebreak (from scripts/fetch/cardmarket)."""
import unittest

import pixel_tiebreak as pt


class DecideTest(unittest.TestCase):
    # scored: [(correlation, blueprint id, [products]), ...] best first
    def test_a_clear_lead_settles(self):
        status, best = pt.decide([(0.88, 1, [10, 11]), (0.31, 2, [12])], 0.7, 0.15)
        self.assertEqual(status, "settled")
        self.assertEqual(best, (0.88, 1, [10, 11]))

    def test_a_close_second_is_tied(self):
        status, best = pt.decide([(0.87, 1, [10]), (0.80, 2, [12])], 0.7, 0.15)
        self.assertEqual(status, "tied")
        self.assertIsNone(best)

    def test_nothing_resembling_ours_is_low(self):
        status, best = pt.decide([(0.62, 1, [10]), (0.20, 2, [12])], 0.7, 0.15)
        self.assertEqual(status, "low")
        self.assertIsNone(best)

    def test_a_single_candidate_still_needs_the_floor(self):
        self.assertEqual(pt.decide([(0.95, 1, [10])], 0.7, 0.15)[0], "settled")
        self.assertEqual(pt.decide([(0.5, 1, [10])], 0.7, 0.15)[0], "low")
        self.assertEqual(pt.decide([], 0.7, 0.15)[0], "no image")


class TwinRuleTest(unittest.TestCase):
    def test_two_ids_of_one_pack_on_one_printing_are_both_dropped(self):
        picks = {"OP01-006_p3": 7, "OP01-006_p4": 7, "OP01-006_p5": 8, "OP02-001_p1": 7}
        pack_of = {"OP01-006_p3": "PRB-01", "OP01-006_p4": "PRB-01", "OP01-006_p5": "PRB-01", "OP02-001_p1": "OP-02"}
        kept, twins = pt.twin_rule(picks, pack_of)
        self.assertEqual(kept, {"OP01-006_p5": 8, "OP02-001_p1": 7})
        self.assertEqual(twins, ["OP01-006_p3", "OP01-006_p4"])

    def test_a_reprint_and_a_parallel_may_share_nothing_but_the_base(self):
        # Different kinds (_r vs _p) never collide: rule C told them apart.
        kept, twins = pt.twin_rule({"EB01-009_r1": 3, "EB01-009_p1": 3}, {"EB01-009_r1": "PRB-02", "EB01-009_p1": "PRB-02"})
        self.assertEqual(twins, [])
        self.assertEqual(len(kept), 2)


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run the tests to verify they fail**

Run (Windows python, from `scripts/fetch/cardmarket`):
```
python -m unittest test_pixel_tiebreak
```
Expected: `ModuleNotFoundError: No module named 'pixel_tiebreak'`.

- [ ] **Step 3: Write the script**

```python
# scripts/fetch/cardmarket/pixel_tiebreak.py
"""Pixel tie-break for the ambiguous printings of one game.

    python pixel_tiebreak.py --game onepiece            # report.txt -> visual.json, pixel_status.json

For every card the mapper's report lists as ambiguous, our reference image
is compared to the CardTrader original of each candidate printing (Pearson
correlation of both images in grayscale 200x280). The best printing is kept
when it clears the floor and leads the next by the margin; the mapper
re-checks both when it applies visual.json. Two ids of one base card in one
pack that pick the same printing are identical twins (an artwork no image
can tell apart) and are both dropped. pixel_status.json records the
outcome per card for the review tool. Scans are cached in cache/images/,
the review tool's folder. Design: docs/superpowers/specs/
2026-10-08-price-reference-selector-design.md.
"""

from __future__ import annotations

import argparse
import collections
import io
import json
import re
import sys
import time
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / "review"))
from review import CARDTRADER_GAME, GAME_PREFIXES, IMAGES, base_number, image_url, read_ambiguous  # noqa: E402

FLOOR, MARGIN = 0.7, 0.15
UA = "TCGScanner cardmarket mapper (personal ML project)"
SIZE = (200, 280)


def decide(scored, floor, margin):
    """scored: [(correlation, blueprint id, products), ...], best first.
    Returns (status, best) with status settled | tied | low | no image."""
    if not scored:
        return "no image", None
    best = scored[0]
    if best[0] < floor:
        return "low", None
    lead = best[0] - (scored[1][0] if len(scored) > 1 else 0.0)
    if lead < margin:
        return "tied", None
    return "settled", best


def kind(card_id):
    return "p" if "_p" in card_id else "r"


def twin_rule(picks, pack_of):
    """picks: card id -> blueprint id. Two ids of one base card, one pack and
    one kind on one printing are twins: both dropped. Returns (kept, twins)."""
    groups = collections.defaultdict(list)
    for card_id, bp in picks.items():
        groups[(card_id.split("_")[0], pack_of.get(card_id, ""), kind(card_id), bp)].append(card_id)
    twins = sorted(c for g in groups.values() if len(g) > 1 for c in g)
    kept = {c: bp for c, bp in picks.items() if c not in set(twins)}
    return kept, twins


def load_blueprints(game):
    exps = json.loads((HERE / "cache" / "cardtrader_expansions.json").read_text("utf-8"))
    by_cm = {}
    for e in exps:
        if e.get("game_id") != CARDTRADER_GAME[game]:
            continue
        f = HERE / "cache" / f"cardtrader_blueprints_{e['id']}.json"
        if not f.exists():
            continue
        for b in json.loads(f.read_text("utf-8")):
            for cm in b.get("card_market_ids") or []:
                if cm:
                    by_cm.setdefault(cm, b)
    return by_cm


def fetch(b):
    url = image_url(b)
    if not url:
        return None
    dest = IMAGES / f"{b['id']}.jpg"
    if dest.exists() and dest.stat().st_size > 1000:
        return dest
    IMAGES.mkdir(parents=True, exist_ok=True)
    try:
        req = urllib.request.Request(url, headers={"User-Agent": UA})
        with urllib.request.urlopen(req, timeout=30) as r:
            data = r.read()
    except Exception as e:  # noqa: BLE001 - the card stays unresolved
        print(f"  image {b['id']}: {e}")
        return None
    if len(data) < 1000:
        return None
    dest.write_bytes(data)
    time.sleep(0.1)
    return dest


def main():
    import numpy as np
    from PIL import Image

    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--game", default="onepiece", choices=sorted(GAME_PREFIXES))
    ap.add_argument("--report", default=str(HERE / "report.txt"))
    ap.add_argument("--references", default=None, help="default scripts/ml/images/<game>")
    ap.add_argument("--out", default=str(HERE / "visual.json"))
    ap.add_argument("--status", default=str(HERE / "pixel_status.json"))
    args = ap.parse_args()
    refs = Path(args.references) if args.references else HERE.parents[1] / "ml" / "images" / args.game
    cards = json.loads((HERE.parent / args.game / "cards.json").read_text("utf-8"))
    pack_of = {i: c.get("pack", "") for i, c in cards.items()}
    amb = read_ambiguous(Path(args.report).read_text("utf-8"), GAME_PREFIXES[args.game])
    by_cm = load_blueprints(args.game)

    cache = {}

    def arr(path):
        if path not in cache:
            cache[path] = np.asarray(Image.open(path).convert("L").resize(SIZE), dtype=np.float32)
        return cache[path]

    def corr(a, b):
        a = a - a.mean()
        b = b - b.mean()
        return float((a * b).sum() / (np.sqrt((a * a).sum() * (b * b).sum()) + 1e-9))

    picks, best_of, status = {}, {}, {}
    for n, (card_id, cands) in enumerate(amb.items(), 1):
        ref = None
        for ext in ("png", "jpg", "webp"):
            if (refs / f"{card_id}.{ext}").exists():
                ref = refs / f"{card_id}.{ext}"
                break
        if ref is None:
            status[card_id] = "no image"
            continue
        scored, seen = [], set()
        for p in cands:
            b = by_cm.get(p)
            if not b or b["id"] in seen:
                continue
            seen.add(b["id"])
            path = fetch(b)
            if not path:
                continue
            scored.append((corr(arr(ref), arr(path)), b["id"], [cm for cm in b.get("card_market_ids") or [] if cm in cands]))
        scored.sort(reverse=True)
        st, best = decide(scored, FLOOR, MARGIN)
        status[card_id] = st
        if best:
            picks[card_id] = best[1]
            best_of[card_id] = (best, best[0] - (scored[1][0] if len(scored) > 1 else 0.0))
        if n % 100 == 0:
            print(f"  {n}/{len(amb)}", flush=True)

    kept, twins = twin_rule(picks, pack_of)
    for t in twins:
        status[t] = "twin"
    out = {"floor": FLOOR, "margin": MARGIN, "picks": {}}
    for card_id in sorted(kept):
        (score, _, products), lead = best_of[card_id]
        out["picks"][card_id] = {"product": products[0], "score": round(score, 4), "lead": round(lead, 4)}
    Path(args.out).write_text(json.dumps(out, indent=1) + "\n", "utf-8")
    Path(args.status).write_text(json.dumps(dict(sorted(status.items())), indent=1) + "\n", "utf-8")
    counts = collections.Counter(status.values())
    print(f"{args.game}: {len(amb)} ambiguous -> settled {len(out['picks'])}, "
          + ", ".join(f"{k} {v}" for k, v in sorted(counts.items()) if k != "settled"))
    print(f"wrote {args.out} and {args.status}")


if __name__ == "__main__":
    main()
```

Note: the mapper's `variantBase` strips the letter after the number the same way `base_number` does; `products[0]` is any product of the chosen printing, the mapper's `visualShelf` picks the English shelf from it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `python -m unittest test_pixel_tiebreak -v`
Expected: 6 tests, `OK`.

- [ ] **Step 5: Run the script on One Piece and check the mapper applies it**

Run (from `scripts/fetch/cardmarket`; the 2,021 scans are already in `cache/images/`, so this takes about a minute):
```
python pixel_tiebreak.py --game onepiece
go run . -games onepiece -offline -report report.txt
```
Expected: the script prints `onepiece: 880 ambiguous -> settled 491, low 73, tied 271, twin 45` (numbers within a few of these); the mapper's summary shows `visual 491` (or close) and `ambiguous 389`. `visual.json` and `pixel_status.json` exist in the mapper dir.

- [ ] **Step 6: Commit**

```bash
git add scripts/fetch/cardmarket/pixel_tiebreak.py scripts/fetch/cardmarket/test_pixel_tiebreak.py scripts/fetch/cardmarket/visual.json scripts/fetch/cardmarket/pixel_status.json scripts/fetch/cardmarket/cardmarket.json
git commit -m "feat(cardmarket): pixel tie-break settles the different-artwork printings into visual.json"
```

---

### Task 2: Candidates in `cardmarket.json`

**Files:**
- Modify: `scripts/fetch/cardmarket/sources.go` (the `ctBlueprint` type, around line 504)
- Modify: `scripts/fetch/cardmarket/resolve.go` (`gameResult`, `resolveGame`'s ambiguity branch, `nonEnglishTwins`, new helpers)
- Modify: `scripts/fetch/cardmarket/main.go` (`mapping`, `loadMapping`, the per-game write loop, `printSummary`)
- Test: `scripts/fetch/cardmarket/resolve_test.go`

**Interfaces:**
- Consumes: `packFixture()` from `resolve_test.go` (EB01-009 in EB-01 and PRB-02, odd products English), `visualShelf`, `pickShelf`, `nonZero`, `blueprintIndex.byCM`.
- Produces: `type candidateView struct{ Product, NonEnglish int; Label, Image string }` (json `product`, `non_english`, `label`, `image`); `gameResult.Candidates map[string][]candidateView`; `mapping.Candidates map[string][]candidateView` (json `candidates`); `func twinOf(product int, b ctBlueprint, shelf map[int]shelfLang, catalog map[int]cmProduct) int`; `func candidateViews(cands []int, ix *blueprintIndex, exps map[int]ctExpansion, shelf map[int]shelfLang, catalog map[int]cmProduct) []candidateView`.

- [ ] **Step 1: Write the failing tests**

Append to `resolve_test.go`:

```go
func TestCandidateViewsOnePerPrinting(t *testing.T) {
	cards, exps, bps, catalog, shelf := packFixture()
	bps[3].Image.URL = "/uploads/blueprints/image/4/pirate-foil.jpg" // blueprint 4, products 13/14
	// A second versioned print in PRB-02 makes our one _p2 ambiguous there.
	b := ctBlueprint{ID: 5, Version: "Manga Panel Alternate Art | Reprint", ExpansionID: 200, CardMarketIDs: []int{15, 16}}
	b.FixedProperties.CollectorNumber = "EB01-009m"
	bps = append(bps, b)
	catalog[15] = cmProduct{IDProduct: 15, Name: "Just Shut Up and Come with Us!!!! (EB01-009)", IDExpansion: 10}
	catalog[16] = cmProduct{IDProduct: 16, Name: "Just Shut Up and Come with Us!!!! (EB01-009)", IDExpansion: 11}
	r := resolveGame("onepiece", cards, nil, exps, bps, nil, manualPicks{}, nil, catalog, shelf, "2026-10-08")
	got := r.Candidates["EB01-009_p2"]
	want := []candidateView{
		{Product: 13, NonEnglish: 14, Label: "Pirate Foil (EB01-009f) · PRB-02: Premium Booster vol.2", Image: "https://cardtrader.com/uploads/blueprints/image/4/pirate-foil.jpg"},
		{Product: 15, NonEnglish: 16, Label: "Manga Panel Alternate Art | Reprint (EB01-009m) · PRB-02: Premium Booster vol.2", Image: ""},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("candidates of EB01-009_p2 = %+v, want %+v", got, want)
	}
	for id := range r.Candidates {
		if _, mapped := r.Mapped[id]; mapped {
			t.Errorf("%s is mapped and has candidates", id)
		}
	}
	if _, ok := r.Candidates["EB01-009_r1"]; ok {
		t.Error("a settled card carries candidates")
	}
}

func TestCandidateViewsProductOnNoBlueprint(t *testing.T) {
	_, exps, bps, catalog, shelf := packFixture()
	ix := indexBlueprints(exps, bps)
	got := candidateViews([]int{1, 2, 99}, ix, exps, shelf, catalog)
	want := []candidateView{
		{Product: 1, NonEnglish: 2, Label: "Plain (EB01-009) · EB-01: Memorial Collection"},
		{Product: 99, Label: "product 99"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("candidateViews = %+v, want %+v", got, want)
	}
	if candidateViews([]int{1}, nil, exps, shelf, catalog)[0].Label != "product 1" {
		t.Error("without blueprints every product is a bare product")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./... -run 'TestCandidateViews' ` (from `scripts/fetch/cardmarket`)
Expected: build failure, `undefined: candidateView`, `r.Candidates undefined`, `bps[3].Image undefined`.

- [ ] **Step 3: Add the image to the blueprint type**

In `sources.go`, inside `type ctBlueprint struct`, after `ImageURL`:

```go
	// Image.URL is the original scan, relative to https://cardtrader.com
	// (ImageURL is the 180px preview). Shipped to the app for the price
	// reference selector.
	Image struct {
		URL string `json:"url"`
	} `json:"image"`
```

- [ ] **Step 4: Add the views to the resolver**

In `resolve.go`, add to `gameResult` after `PackDisagree`:

```go
	// Candidates are the selectable printings of every ambiguous card,
	// shipped in cardmarket.json for the app's price reference selector
	// (spec 2026-10-08-price-reference-selector-design.md).
	Candidates map[string][]candidateView
```

Initialise it in `resolveGame`'s `gameResult{...}` literal: `Candidates: map[string][]candidateView{}`. In the same function, where an ambiguity is recorded:

```go
			if len(cands) > 1 {
				r.Ambiguous = append(r.Ambiguous, ambiguity{id, cands})
				r.Candidates[id] = candidateViews(cands, ix, exps, shelf, catalog)
			} else {
```

Replace the body of `nonEnglishTwins`'s inner loop with the shared helper:

```go
	for id, m := range r.Mapped {
		bps := ix.byCM[m.Product]
		if len(bps) != 1 {
			continue
		}
		if twin := twinOf(m.Product, bps[0], shelf, catalog); twin != 0 {
			r.NonEnglish[id] = twin
		}
	}
```

Add after `nonEnglishTwins`:

```go
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
```

- [ ] **Step 5: Ship them from main.go**

In `main.go`, add to `mapping` after `CardsNonEnglish`:

```go
	// Candidates are the selectable printings of every ambiguous card
	// (product, non-English twin, label, scan URL), for the app's price
	// reference selector. Absent in older files: readers treat it as empty.
	Candidates map[string][]candidateView `json:"candidates"`
```

In `loadMapping`: initialise `Candidates: map[string][]candidateView{}` in the literal and, after the `m.CardsNonEnglish == nil` guard, add `if m.Candidates == nil { m.Candidates = map[string][]candidateView{} }`.

In the per-game loop, after the `CardsNonEnglish` deletion loop:

```go
		for id := range m.Candidates {
			if gameOfID(id, game) {
				delete(m.Candidates, id)
			}
		}
```
and after `m.CardsNonEnglish[id] = p` loop:
```go
		for id, v := range res.Candidates {
			m.Candidates[id] = v
		}
```
Change the final line to:
```go
	fmt.Printf("%s written: %d cards, %d with a non-English twin, %d with candidates\n", outFile, len(m.Cards), len(m.CardsNonEnglish), len(m.Candidates))
```
In `printSummary`, after the `dropped by validation ...` line's `fmt.Println()`:
```go
	if len(r.Candidates) > 0 {
		fmt.Printf("  candidates shipped for %d cards\n", len(r.Candidates))
	}
```

- [ ] **Step 6: Run the whole suite**

Run: `gofmt -l main.go resolve.go sources.go resolve_test.go; go vet ./... && go test ./...`
Expected: `gofmt` prints nothing for those files, `ok cardmarket-map`.

- [ ] **Step 7: Rebuild One Piece and check the file**

Run: `go run . -games onepiece -offline -report report.txt` then:
```
python -c "import json;d=json.load(open('cardmarket.json',encoding='utf-8'));c=d['candidates'];print(len(c), next(iter(c.items())));print('overlap', len(set(c)&set(d['cards'])))"
```
Expected: about 389 ids with candidates, a sample like `('EB01-043_p1', [{'product': 804365, 'non_english': 0, 'label': 'Storage Box Set Promo (EB01-043) · One Piece Promos', 'image': 'https://cardtrader.com/uploads/...'}, ...])`, `overlap 0`.

- [ ] **Step 8: Document and commit**

In `scripts/fetch/cardmarket/README.md`, under `## Files`, add `- \`pixel_status.json\` — the pixel tie-break's outcome per card, read by the review tool` and in the "Step 3" paragraph add: "`pixel_tiebreak.py` is the tie-break in use (pixel correlation, no GPU, `cache/images/`); `visual_tiebreak.py` is the embedding alternative. The ids neither settles ship in `cardmarket.json` as `candidates` (product, non-English twin, label, scan URL) for the app's price reference selector, spec `docs/superpowers/specs/2026-10-08-price-reference-selector-design.md`."

```bash
git add scripts/fetch/cardmarket/README.md scripts/fetch/cardmarket/main.go scripts/fetch/cardmarket/resolve.go scripts/fetch/cardmarket/resolve_test.go scripts/fetch/cardmarket/sources.go scripts/fetch/cardmarket/cardmarket.json
git commit -m "feat(cardmarket): ship the candidate printings of ambiguous cards for the app's price reference selector"
```

---

### Task 3: Review tool reads the pixel status

**Files:**
- Modify: `scripts/fetch/cardmarket/review/review.py` (`choice_queue`, `App.__init__`, `App.state`, `App.card`)
- Modify: `scripts/fetch/cardmarket/review/index.html` (the card header)
- Test: `scripts/fetch/cardmarket/review/test_review.py`

**Interfaces:**
- Consumes: `pixel_status.json` from Task 1 (`{id: status}`).
- Produces: `choice_queue(ambiguous, manual, status=None)` — "low" cards first, then the rest, each group in report order; `App.card()` gains `"pixel": status or ""`.

- [ ] **Step 1: Write the failing test**

Append to `review/test_review.py`:

```python
class PixelStatusTest(unittest.TestCase):
    def test_low_cards_come_first_then_report_order(self):
        ambiguous = {"A_p1": [1], "B_p1": [1], "C_p1": [1], "D_p1": [1]}
        status = {"A_p1": "tied", "B_p1": "low", "C_p1": "twin", "D_p1": "low"}
        self.assertEqual(review.choice_queue(ambiguous, {"picks": {}, "none": []}, status), ["B_p1", "D_p1", "A_p1", "C_p1"])

    def test_without_a_status_file_the_order_is_unchanged(self):
        ambiguous = {"A_p1": [1], "B_p1": [1]}
        self.assertEqual(review.choice_queue(ambiguous, {"picks": {}, "none": []}), ["A_p1", "B_p1"])
```

- [ ] **Step 2: Run it to verify it fails**

Run: `python -m unittest test_review.PixelStatusTest` (from `review/`)
Expected: `TypeError: choice_queue() takes 2 positional arguments but 3 were given`.

- [ ] **Step 3: Implement**

In `review.py`, replace `choice_queue`:

```python
def choice_queue(ambiguous: dict[str, list[int]], manual: dict, status: dict[str, str] | None = None) -> list[str]:
    """The cards still to settle, "low" ones first (our image matches no
    candidate: worth a look) then the rest, each in the report's order."""
    done = set(manual.get("picks", {})) | set(manual.get("none", []))
    left = [i for i in ambiguous if i not in done]
    if not status:
        return left
    return [i for i in left if status.get(i) == "low"] + [i for i in left if status.get(i) != "low"]
```

In `App.__init__`, after `self.ambiguous = ...`:

```python
        status_path = MAPPER / "pixel_status.json"
        self.status = json.loads(status_path.read_text("utf-8")) if status_path.exists() else {}
```

In `App.state`, `queue = choice_queue(self.ambiguous, manual, self.status)`. In `App.card`, add `"pixel": self.status.get(card_id, ""),` next to `"pack"`.

In `index.html`, in `renderCard`, after the `.meta` div add:

```html
      ${c.pixel ? `<div class="meta" style="color:var(--warn)">${esc(PIXEL_HINT[c.pixel] || c.pixel)}</div>` : ""}
```
and near the top of the script:
```js
const PIXEL_HINT = {
  low: "Our image matches no candidate: look closely, it may be none of them",
  tied: "Same artwork on every candidate: no image can tell these apart",
  twin: "Twin of another id with this artwork: no image can tell these apart",
  "no image": "No reference image on our side",
};
```

- [ ] **Step 4: Run the tests**

Run: `python -m unittest` (from `review/`)
Expected: 13 tests, `OK`.

- [ ] **Step 5: Commit**

```bash
git add scripts/fetch/cardmarket/review/review.py scripts/fetch/cardmarket/review/index.html scripts/fetch/cardmarket/review/test_review.py
git commit -m "feat(cardmarket): review tool lists the pixel tie-break's doubtful cards first"
```

---

### Task 4: `price_refs` table

**Files:**
- Modify: `db.go` (`schemaV2`)
- Create: `price_refs.go`
- Create: `price_refs_test.go`

**Interfaces:**
- Consumes: `testDB(t)` (an in-memory migrated DB, see `prices_test.go`), `db.go`'s `schemaV2`.
- Produces: `type PriceRef struct{ CardID string; Product, NonEnglish int; Label, ChosenAt string }` (json `card_id`, `product`, `non_english`, `label`, `chosen_at`); `getPriceRefs(db *sql.DB) (map[string]PriceRef, error)`; `setPriceRef(db *sql.DB, r PriceRef) error` (upsert, stamps `ChosenAt` when empty); `clearPriceRef(db *sql.DB, cardID string) error`.

- [ ] **Step 1: Write the failing test**

```go
// price_refs_test.go
package main

import "testing"

func TestPriceRefsRoundTrip(t *testing.T) {
	db := testDB(t)
	refs, err := getPriceRefs(db)
	if err != nil || len(refs) != 0 {
		t.Fatalf("fresh db: %v, %v", refs, err)
	}
	if err := setPriceRef(db, PriceRef{CardID: "OP01-006_p4", Product: 799296, NonEnglish: 799297, Label: "Jolly Roger Foil (OP01-006j) · PRB-01"}); err != nil {
		t.Fatal(err)
	}
	// A second choice replaces the first.
	if err := setPriceRef(db, PriceRef{CardID: "OP01-006_p4", Product: 799298, Label: "Full Art (OP01-006f) · PRB-01"}); err != nil {
		t.Fatal(err)
	}
	refs, _ = getPriceRefs(db)
	got := refs["OP01-006_p4"]
	if got.Product != 799298 || got.NonEnglish != 0 || got.Label != "Full Art (OP01-006f) · PRB-01" || got.ChosenAt == "" {
		t.Errorf("after two sets: %+v", got)
	}
	if err := clearPriceRef(db, "OP01-006_p4"); err != nil {
		t.Fatal(err)
	}
	if err := clearPriceRef(db, "never-set"); err != nil {
		t.Errorf("clearing an unset id must be a no-op, got %v", err)
	}
	refs, _ = getPriceRefs(db)
	if len(refs) != 0 {
		t.Errorf("after clear: %v", refs)
	}
}

func TestPriceRefsMigrationIsIdempotent(t *testing.T) {
	db := testDB(t)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'price_refs'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("price_refs table: %d, %v", n, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run 'TestPriceRefs' ./...` (repo root)
Expected: `undefined: getPriceRefs`, `undefined: PriceRef`.

- [ ] **Step 3: Implement**

In `db.go`, append to `schemaV2` before the closing backtick:

```sql
	-- 2026-10-08: the owner's Cardmarket product per card id, for the ids the
	-- mapper ships candidates for (price_refs.go, the price reference selector).
	CREATE TABLE IF NOT EXISTS price_refs (
		card_id     TEXT PRIMARY KEY,
		product     INTEGER NOT NULL,
		non_english INTEGER NOT NULL DEFAULT 0,
		label       TEXT NOT NULL DEFAULT '',
		chosen_at   TEXT NOT NULL
	);
```

```go
// price_refs.go
package main

import (
	"database/sql"
	"time"
)

// PriceRef is the owner's choice of Cardmarket product for one card id,
// made in the price reference selector among the candidates the bundle
// ships (docs/superpowers/specs/2026-10-08-price-reference-selector-design.md).
// It outranks the bundle's own mapping for that id.
type PriceRef struct {
	CardID     string `json:"card_id"`
	Product    int    `json:"product"`
	NonEnglish int    `json:"non_english"`
	Label      string `json:"label"`
	ChosenAt   string `json:"chosen_at"` // RFC 3339, UTC
}

func getPriceRefs(db *sql.DB) (map[string]PriceRef, error) {
	rows, err := db.Query(`SELECT card_id, product, non_english, label, chosen_at FROM price_refs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]PriceRef{}
	for rows.Next() {
		var r PriceRef
		if err := rows.Scan(&r.CardID, &r.Product, &r.NonEnglish, &r.Label, &r.ChosenAt); err != nil {
			return nil, err
		}
		out[r.CardID] = r
	}
	return out, rows.Err()
}

func setPriceRef(db *sql.DB, r PriceRef) error {
	if r.ChosenAt == "" {
		r.ChosenAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := db.Exec(`INSERT INTO price_refs (card_id, product, non_english, label, chosen_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(card_id) DO UPDATE SET product = excluded.product, non_english = excluded.non_english, label = excluded.label, chosen_at = excluded.chosen_at`,
		r.CardID, r.Product, r.NonEnglish, r.Label, r.ChosenAt)
	return err
}

func clearPriceRef(db *sql.DB, cardID string) error {
	_, err := db.Exec(`DELETE FROM price_refs WHERE card_id = ?`, cardID)
	return err
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./...` (repo root)
Expected: all packages `ok` (the existing migration tests still pass: the statement is `IF NOT EXISTS`).

- [ ] **Step 5: Commit**

```bash
git add db.go price_refs.go price_refs_test.go
git commit -m "feat(prices): price_refs table, the owner's Cardmarket product per card id"
```

---

### Task 5: The mapping's candidates and the reference's precedence

**Files:**
- Modify: `prices.go` (`priceMapping`, `parsePriceMapping`, `CardPrice`, `cardPrice`, `GetCardPrices`, `GetGameValues`)
- Test: `prices_test.go`

**Interfaces:**
- Consumes: `getPriceRefs` (Task 4), `priceOf`.
- Produces: `type PriceCandidate struct{ Product, NonEnglish int; Label, Image string }` (json `product`, `non_english`, `label`, `image`); `priceMapping.Candidates map[string][]PriceCandidate`; `CardPrice.Ref bool` (json `ref`), `CardPrice.Candidates int` (json `candidates`); `func (a *App) priceRefs() map[string]PriceRef` (never nil; empty on error); `cardPrice(m *priceMapping, refs map[string]PriceRef, cardID string) (CardPrice, bool)` — returns `(CardPrice{Candidates: n}, true)` for an unpriced id with n candidates and no reference.

- [ ] **Step 1: Write the failing tests**

Append to `prices_test.go` and extend `testMapping`:

```go
// testMappingWithCandidates adds an ambiguous One Piece card with two printings.
const testMappingWithCandidates = `{
 "version": 1, "built_at": "2026-10-08",
 "games": {"onepiece": 18},
 "cards": {"OP05-119": 2001},
 "cards_non_english": {"OP05-119": 2002},
 "candidates": {"OP01-006_p4": [
   {"product": 3001, "non_english": 3002, "label": "Full Art (OP01-006f) · PRB-01: The Best", "image": "https://cardtrader.com/uploads/blueprints/image/1/f.jpg"},
   {"product": 3003, "non_english": 0, "label": "Jolly Roger Foil (OP01-006j) · PRB-01: The Best", "image": ""}
 ]}
}`

func TestCardPriceReferenceOutranksTheBundle(t *testing.T) {
	app, _ := pricesApp(t, testMappingWithCandidates)
	writeGuide(t, app, "onepiece", testGuide("2026-10-08T02:00:00+0200",
		priceRow{IDProduct: 2001, Trend: 8.33}, priceRow{IDProduct: 2005, Trend: 1.11},
		priceRow{IDProduct: 3001, Trend: 3.2, TrendFoil: 3.5}, priceRow{IDProduct: 3002, Trend: 2.0},
		priceRow{IDProduct: 3003, Trend: 15.2},
	))
	got, err := app.GetCardPrices([]string{"OP05-119", "OP01-006_p4"})
	if err != nil {
		t.Fatal(err)
	}
	// No reference yet: the ambiguous id is reported with its candidate count and no price.
	if p := got["OP01-006_p4"]; p.Trend != 0 || p.Candidates != 2 || p.Ref {
		t.Errorf("unset reference: %+v", p)
	}
	if err := setPriceRef(app.db, PriceRef{CardID: "OP01-006_p4", Product: 3001, NonEnglish: 3002, Label: "Full Art"}); err != nil {
		t.Fatal(err)
	}
	got, _ = app.GetCardPrices([]string{"OP05-119", "OP01-006_p4"})
	want := CardPrice{Trend: 3.2, TrendFoil: 3.5, TrendNonEnglish: 2.0, AsOf: "2026-10-08", Ref: true, Candidates: 2}
	if got["OP01-006_p4"] != want {
		t.Errorf("with a reference: %+v, want %+v", got["OP01-006_p4"], want)
	}
	// A reference outranks the bundle for a mapped id too.
	if err := setPriceRef(app.db, PriceRef{CardID: "OP05-119", Product: 2005}); err != nil {
		t.Fatal(err)
	}
	got, _ = app.GetCardPrices([]string{"OP05-119"})
	if p := got["OP05-119"]; p.Trend != 1.11 || !p.Ref || p.TrendNonEnglish != 0 {
		t.Errorf("reference over bundle: %+v", p)
	}
	// A reference to a product the day's guide lacks is unpriced, not wrong.
	if err := setPriceRef(app.db, PriceRef{CardID: "OP01-006_p4", Product: 4444}); err != nil {
		t.Fatal(err)
	}
	got, _ = app.GetCardPrices([]string{"OP01-006_p4"})
	if p := got["OP01-006_p4"]; p.Trend != 0 || !p.Ref || p.Candidates != 2 {
		t.Errorf("stale reference: %+v", p)
	}
}

func TestParsePriceMappingWithoutCandidates(t *testing.T) {
	m, err := parsePriceMapping([]byte(testMapping))
	if err != nil || m.Candidates == nil || len(m.Candidates) != 0 {
		t.Errorf("an older file must read as no candidates: %v, %v", m.Candidates, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -run 'TestCardPriceReference|TestParsePriceMappingWithout' ./...`
Expected: build failure, `unknown field Candidates`, `p.Ref undefined`.

- [ ] **Step 3: Implement**

In `prices.go`:

```go
// PriceCandidate is one selectable printing of a card the mapper could not
// settle: the product the app would price on, its non-English twin, a
// label and CardTrader's scan. Shipped in cardmarket.json under "candidates".
type PriceCandidate struct {
	Product    int    `json:"product"`
	NonEnglish int    `json:"non_english"`
	Label      string `json:"label"`
	Image      string `json:"image"`
}
```
Add to `priceMapping`: `Candidates map[string][]PriceCandidate `json:"candidates"``. In `parsePriceMapping`, add `if m.Candidates == nil { m.Candidates = map[string][]PriceCandidate{} }`.

Add to `CardPrice`:
```go
	// Ref: priced through the owner's reference (price_refs), not the bundle.
	Ref bool `json:"ref"`
	// Candidates: printings the owner can choose from in the price reference
	// selector; 0 when the bundle settled the id or has nothing for it.
	Candidates int `json:"candidates"`
```

Replace `cardPrice`:
```go
func (a *App) priceRefs() map[string]PriceRef {
	if a.db == nil {
		return map[string]PriceRef{}
	}
	refs, err := getPriceRefs(a.db)
	if err != nil {
		return map[string]PriceRef{}
	}
	return refs
}

// cardPrice prices one card id: the owner's reference when there is one,
// else the bundle's product and non-English twin. An id without a price
// but with candidates is reported (Candidates > 0, every column 0) so the
// UI can offer the selector; an id with neither is absent.
func (a *App) cardPrice(m *priceMapping, refs map[string]PriceRef, cardID string) (CardPrice, bool) {
	p := CardPrice{Candidates: len(m.Candidates[cardID])}
	product, twin := m.Cards[cardID], m.CardsNonEnglish[cardID]
	if r, ok := refs[cardID]; ok {
		product, twin, p.Ref = r.Product, r.NonEnglish, true
	}
	found := false
	if r, asOf, ok := a.priceOf(m, product); ok {
		p.Trend, p.TrendFoil, p.AsOf = r.Trend, r.TrendFoil, asOf
		found = true
	}
	if r, asOf, ok := a.priceOf(m, twin); ok {
		p.TrendNonEnglish, p.TrendNonEnglishFoil = r.Trend, r.TrendFoil
		if p.AsOf == "" {
			p.AsOf = asOf
		}
		found = true
	}
	if !found || (p.Trend == 0 && p.TrendFoil == 0 && p.TrendNonEnglish == 0 && p.TrendNonEnglishFoil == 0) {
		if p.Candidates > 0 {
			return CardPrice{Ref: p.Ref, Candidates: p.Candidates}, true
		}
		return CardPrice{}, false
	}
	return p, true
}
```
Update the callers: in `GetCardPrices` load `refs := a.priceRefs()` once before the loop and call `a.cardPrice(m, refs, id)`; in `GetGameValues` do the same where it calls `cardPrice` (search `a.cardPrice(` in `prices.go`; `copyValue` already returns false for an all-zero price, so a candidates-only entry adds nothing to the totals).

- [ ] **Step 4: Run the suite**

Run: `go test ./...`
Expected: all `ok`. `TestGetCardPrices` still passes: its mapping has no candidates, so `unpriced-1` stays absent.

- [ ] **Step 5: Commit**

```bash
git add prices.go prices_test.go
git commit -m "feat(prices): the owner's price reference outranks the bundle; candidates reported per card"
```

---

### Task 6: Bindings to list, choose and clear a reference

**Files:**
- Modify: `prices.go`
- Test: `prices_test.go`

**Interfaces:**
- Consumes: `cardPrice`, `priceRefs`, `setPriceRef`, `clearPriceRef`, `emitPricesUpdated`.
- Produces: `type PriceCandidateView struct{ PriceCandidate; Price CardPrice; Chosen bool }` (json: the candidate's fields plus `price`, `chosen`); `func (a *App) GetPriceCandidates(cardID string) ([]PriceCandidateView, error)`; `func (a *App) SetPriceRef(cardID string, product int) error`; `func (a *App) ClearPriceRef(cardID string) error`; `func (a *App) GetPriceRefs() (map[string]PriceRef, error)`.

- [ ] **Step 1: Write the failing test**

```go
func TestPriceReferenceBindings(t *testing.T) {
	app, _ := pricesApp(t, testMappingWithCandidates)
	writeGuide(t, app, "onepiece", testGuide("2026-10-08T02:00:00+0200",
		priceRow{IDProduct: 3001, Trend: 3.2, TrendFoil: 3.5}, priceRow{IDProduct: 3002, Trend: 2.0}, priceRow{IDProduct: 3003, Trend: 15.2},
	))
	views, err := app.GetPriceCandidates("OP01-006_p4")
	if err != nil || len(views) != 2 {
		t.Fatalf("candidates: %v, %v", views, err)
	}
	if views[0].Label != "Full Art (OP01-006f) · PRB-01: The Best" || views[0].Price.Trend != 3.2 || views[0].Price.TrendNonEnglish != 2.0 || views[0].Chosen {
		t.Errorf("first view: %+v", views[0])
	}
	if views[1].Price.Trend != 15.2 || views[1].Image != "" {
		t.Errorf("second view: %+v", views[1])
	}
	if err := app.SetPriceRef("OP01-006_p4", 4444); err == nil {
		t.Error("a product outside the candidates was accepted")
	}
	if err := app.SetPriceRef("OP01-006_p4", 3003); err != nil {
		t.Fatal(err)
	}
	refs, _ := app.GetPriceRefs()
	if r := refs["OP01-006_p4"]; r.Product != 3003 || r.Label != "Jolly Roger Foil (OP01-006j) · PRB-01: The Best" || r.NonEnglish != 0 {
		t.Errorf("stored reference: %+v", r)
	}
	views, _ = app.GetPriceCandidates("OP01-006_p4")
	if views[0].Chosen || !views[1].Chosen {
		t.Errorf("chosen marks: %v %v", views[0].Chosen, views[1].Chosen)
	}
	if err := app.ClearPriceRef("OP01-006_p4"); err != nil {
		t.Fatal(err)
	}
	if refs, _ := app.GetPriceRefs(); len(refs) != 0 {
		t.Errorf("after clear: %v", refs)
	}
	if views, err := app.GetPriceCandidates("OP05-119"); err != nil || len(views) != 0 {
		t.Errorf("a settled id has no candidates: %v, %v", views, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestPriceReferenceBindings ./...`
Expected: `app.GetPriceCandidates undefined`.

- [ ] **Step 3: Implement**

Append to `prices.go`:

```go
// PriceCandidateView is a candidate as the selector shows it: with today's
// price of its products and whether it is the owner's current choice.
type PriceCandidateView struct {
	PriceCandidate
	Price  CardPrice `json:"price"`
	Chosen bool      `json:"chosen"`
}

// GetPriceCandidates lists the printings the owner may choose for an id
// the bundle could not settle. Empty for a settled or unknown id.
func (a *App) GetPriceCandidates(cardID string) ([]PriceCandidateView, error) {
	m := a.priceMapping()
	if m == nil {
		return nil, nil
	}
	ref, hasRef := a.priceRefs()[cardID]
	var out []PriceCandidateView
	for _, c := range m.Candidates[cardID] {
		v := PriceCandidateView{PriceCandidate: c, Chosen: hasRef && ref.Product == c.Product}
		if r, asOf, ok := a.priceOf(m, c.Product); ok {
			v.Price.Trend, v.Price.TrendFoil, v.Price.AsOf = r.Trend, r.TrendFoil, asOf
		}
		if r, asOf, ok := a.priceOf(m, c.NonEnglish); ok {
			v.Price.TrendNonEnglish, v.Price.TrendNonEnglishFoil = r.Trend, r.TrendFoil
			if v.Price.AsOf == "" {
				v.Price.AsOf = asOf
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// SetPriceRef records the owner's choice: product must be one of the id's
// candidates, whose label and non-English twin are stored with it.
func (a *App) SetPriceRef(cardID string, product int) error {
	if a.db == nil {
		return fmt.Errorf("no database")
	}
	m := a.priceMapping()
	if m == nil {
		return fmt.Errorf("no price mapping loaded")
	}
	for _, c := range m.Candidates[cardID] {
		if c.Product == product {
			if err := setPriceRef(a.db, PriceRef{CardID: cardID, Product: c.Product, NonEnglish: c.NonEnglish, Label: c.Label}); err != nil {
				return err
			}
			a.emitPricesUpdated()
			return nil
		}
	}
	return fmt.Errorf("product %d is not a candidate of %s", product, cardID)
}

func (a *App) ClearPriceRef(cardID string) error {
	if a.db == nil {
		return fmt.Errorf("no database")
	}
	if err := clearPriceRef(a.db, cardID); err != nil {
		return err
	}
	a.emitPricesUpdated()
	return nil
}

func (a *App) GetPriceRefs() (map[string]PriceRef, error) {
	if a.db == nil {
		return map[string]PriceRef{}, nil
	}
	return getPriceRefs(a.db)
}
```

- [ ] **Step 4: Run the suite**

Run: `go test ./...`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
git add prices.go prices_test.go
git commit -m "feat(prices): bindings to list, choose and clear a card's Cardmarket reference"
```

---

### Task 7: Candidate scans through a local cache

**Files:**
- Modify: `prices.go`
- Modify: `app.go` (one test hook field on `App`, next to `priceGuideURLFn`)
- Test: `prices_test.go`

**Interfaces:**
- Consumes: `priceHTTPClient`, `writeFileAtomic`, `a.pricesDirPath()`.
- Produces: `func (a *App) GetPriceCandidateImage(url string) (string, error)` — a `data:image/jpeg;base64,...` URL, `""` with nil error when the fetch fails, an error for a URL outside `https://cardtrader.com/uploads/`; `App.priceImageAllowFn func(url string) bool` (tests only).

- [ ] **Step 1: Write the failing test**

```go
func TestGetPriceCandidateImage(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent")
		}
		if strings.HasSuffix(r.URL.Path, "missing.jpg") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte(strings.Repeat("J", 2000)))
	}))
	defer srv.Close()
	app, _ := pricesApp(t, testMappingWithCandidates)
	app.priceImageAllowFn = func(u string) bool { return strings.HasPrefix(u, srv.URL) }

	got, err := app.GetPriceCandidateImage(srv.URL + "/uploads/blueprints/image/1/f.jpg")
	if err != nil || !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Fatalf("first fetch: %q, %v", got[:min(len(got), 40)], err)
	}
	again, _ := app.GetPriceCandidateImage(srv.URL + "/uploads/blueprints/image/1/f.jpg")
	if again != got || atomic.LoadInt32(&hits) != 1 {
		t.Errorf("second call must come from the cache (hits %d)", hits)
	}
	if got, err := app.GetPriceCandidateImage(srv.URL + "/uploads/blueprints/image/1/missing.jpg"); got != "" || err != nil {
		t.Errorf("a missing image is empty and no error: %q, %v", got, err)
	}
	if _, err := app.GetPriceCandidateImage("https://example.com/x.jpg"); err == nil {
		t.Error("a URL outside CardTrader was accepted")
	}
	if _, err := os.Stat(filepath.Join(app.pricesDirPath(), "images")); err != nil {
		t.Errorf("cache dir: %v", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestGetPriceCandidateImage ./...`
Expected: `app.priceImageAllowFn undefined`.

- [ ] **Step 3: Implement**

In `app.go`'s `App` struct, after `priceGuideURLFn`:
```go
	priceImageAllowFn  func(url string) bool // tests: which image URLs may be fetched
```

In `prices.go`, add `"crypto/sha1"`, `"encoding/base64"`, `"encoding/hex"` to the imports and:

```go
const (
	priceImageAllowedPrefix = "https://cardtrader.com/uploads/"
	priceImageMaxBytes      = 4 << 20
)

func (a *App) priceImageAllowed(url string) bool {
	if a.priceImageAllowFn != nil {
		return a.priceImageAllowFn(url)
	}
	return strings.HasPrefix(url, priceImageAllowedPrefix)
}

// GetPriceCandidateImage returns a candidate's CardTrader scan as a data
// URL for the price reference selector. Fetched once (an explicit
// User-Agent, one request per call) and cached under ./prices/images/ by
// the URL's hash; "" when the fetch fails, so the dialog shows "no image".
// Only CardTrader's upload URLs are accepted.
func (a *App) GetPriceCandidateImage(url string) (string, error) {
	if !a.priceImageAllowed(url) {
		return "", fmt.Errorf("not a CardTrader image URL")
	}
	sum := sha1.Sum([]byte(url))
	path := filepath.Join(a.pricesDirPath(), "images", hex.EncodeToString(sum[:])+".jpg")
	data, err := os.ReadFile(path)
	if err != nil {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return "", nil
		}
		req.Header.Set("User-Agent", "TCGScanner desktop app (price reference images)")
		resp, err := priceHTTPClient.Do(req)
		if err != nil {
			return "", nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", nil
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, priceImageMaxBytes))
		if err != nil || len(data) < 1000 {
			return "", nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			_ = writeFileAtomic(path, data)
		}
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data), nil
}
```
(`strings` is not yet imported in `prices.go`; add it.)

- [ ] **Step 4: Run the suite**

Run: `go vet ./... && go test ./...`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
git add app.go prices.go prices_test.go
git commit -m "feat(prices): candidate scans fetched from CardTrader on demand, cached under prices/images"
```

---

### Task 8: Delivery 1 — the warning chip

**Files:**
- Modify: `frontend/src/types.ts` (`CardPrice`, new interfaces)
- Modify: `frontend/src/lib/prices.ts`
- Modify: `frontend/src/style.css` (the `--n-warn` token)
- Modify: `frontend/src/components/CollectionScreen.tsx` (the tile, around lines 376-392)
- Modify: `frontend/src/components/CardCopiesDialog.tsx` (the header, around lines 104-114)
- Regenerate: `frontend/wailsjs/go/main/*`

**Interfaces:**
- Consumes: `CardPrice.ref`, `CardPrice.candidates` from Task 5.
- Produces: `referenceState(price?: CardPrice): "priced" | "unset" | "absent"` in `lib/prices.ts`; types `PriceCandidateView`, `PriceRef` in `types.ts`.

- [ ] **Step 1: Regenerate the bindings**

Run from the repo root: `wails generate module`. If the CLI is missing on this machine, `wails dev` regenerates `frontend/wailsjs` on start (the laptop's note in STATUS.md: the stock CLI is rebuilt with `golang.org/x/tools` v0.51 because Go 1.27 broke it; on this PC Go is 1.26).
Expected: `frontend/wailsjs/go/main/App.d.ts` declares `GetPriceCandidates`, `SetPriceRef`, `ClearPriceRef`, `GetPriceRefs`, `GetPriceCandidateImage`; `models.ts` has `CardPrice.ref`, `CardPrice.candidates`, `PriceCandidateView`, `PriceRef`.

- [ ] **Step 2: Types and the helper**

In `types.ts`, add to `CardPrice` after `as_of`:
```ts
  // Priced through the owner's reference (price_refs), not the bundle.
  ref: boolean;
  // Printings the owner can choose from; 0 when settled or absent.
  candidates: number;
```
and after `GameValue`:
```ts
// One selectable printing of an unsettled card (see prices.go).
export interface PriceCandidateView {
  product: number;
  non_english: number;
  label: string;
  image: string; // CardTrader scan URL, "" when none
  price: CardPrice;
  chosen: boolean;
}

export interface PriceRef {
  card_id: string;
  product: number;
  non_english: number;
  label: string;
  chosen_at: string;
}
```

In `lib/prices.ts`:
```ts
export type ReferenceState = "priced" | "unset" | "absent";

// "unset": the bundle has printings to choose from but none chosen yet;
// "absent": no product and nothing to choose (not on Cardmarket).
export function referenceState(price: CardPrice | undefined): ReferenceState {
  if (!price) return "absent";
  if (price.trend > 0 || price.trend_foil > 0 || price.trend_non_english > 0 || price.trend_non_english_foil > 0) return "priced";
  return price.candidates > 0 ? "unset" : "absent";
}
```

- [ ] **Step 3: The tile chip**

In `CollectionScreen.tsx`, import `referenceState` from `@/lib/prices` and `TriangleAlert` from `lucide-react`. In the tile's price span, replace the `tilePrice` IIFE with:
```tsx
                    {(() => {
                      const p = prices[g.card_id];
                      const v = tilePrice(g, p);
                      if (v !== null) return <span className="n-n400">{formatEUR(v)}</span>;
                      if (collection.prices?.available && referenceState(p) === "unset") {
                        return (
                          <span className="inline-flex items-center gap-0.5" style={{ color: "var(--n-warn)" }} title="No Cardmarket reference: set it by hand in the card's details">
                            <TriangleAlert className="w-3 h-3" aria-hidden="true" />
                            no ref
                          </span>
                        );
                      }
                      return null;
                    })()}
```
`--n-warn` does not exist yet: add it to `frontend/src/style.css` in the `:root` block next to `--n-danger`, as `--n-warn: #f0b35a; /* warning: a reference to set, nothing broken */`. It is the review tool's warning colour, readable on the Nocturne background.

- [ ] **Step 4: The copies dialog chip**

In `CardCopiesDialog.tsx`, import `referenceState` and `TriangleAlert`; compute `const refState = referenceState(price);` next to `owned`; after the `{price && owned && (...)}` block add:
```tsx
          {refState !== "priced" && (
            <p className="m-0 text-sm pt-2 inline-flex items-center gap-1.5" style={{ color: refState === "unset" ? "var(--n-warn)" : "var(--n-neutral-500)" }}>
              <TriangleAlert className="w-4 h-4" aria-hidden="true" />
              {refState === "unset" ? "No Cardmarket reference, set it by hand." : "Not on Cardmarket."}
            </p>
          )}
```
`price` is `undefined` for an absent id, so the "Not on Cardmarket" line shows only when prices are on: guard the whole block with a new prop `pricesAvailable: boolean`, passed from `CollectionScreen` as `pricesAvailable={!!collection.prices?.available}`.

- [ ] **Step 5: Build and check live**

Run: `cd frontend && npm run build`
Expected: `tsc` clean, Vite build succeeds.
Then `wails dev` from the repo root with the rebuilt `cardmarket.json` copied to `models/dev/metadata/cardmarket.json` if no bundle is present: open a One Piece collection containing an unsettled id (e.g. scan or insert `OP01-006_p4`): the tile shows "no ref" in the warning colour, the copies dialog says "No Cardmarket reference, set it by hand."; a Pokémon card without a product says "Not on Cardmarket." in the dialog and nothing on the tile.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/types.ts frontend/src/lib/prices.ts frontend/src/components/CollectionScreen.tsx frontend/src/components/CardCopiesDialog.tsx frontend/wailsjs
git commit -m "feat(frontend): warn when a card has no Cardmarket reference"
```

---

### Task 9: Delivery 2 — the price reference dialog

**Files:**
- Create: `frontend/src/components/PriceReferenceDialog.tsx`
- Modify: `frontend/src/components/CardCopiesDialog.tsx` (the "Set reference" action)
- Modify: `frontend/src/components/CollectionScreen.tsx` (the dialog's state and wiring)

**Interfaces:**
- Consumes: `GetPriceCandidates`, `SetPriceRef`, `ClearPriceRef`, `GetPriceCandidateImage` (Tasks 6-7, via `wailsjs`), `CardImage`, `Dialog*` from `@/components/ui/dialog`, `useNotices`/`errorText`, `formatEUR`, `copyValue`, `PriceCandidateView`.
- Produces: `PriceReferenceDialog({ cardId, image, info, onClose }: { cardId: string | null; image: string | null; info: CardInfo | null; onClose: () => void })`; `CardCopiesDialog` prop `onSetReference?: () => void`.

- [ ] **Step 1: The dialog**

```tsx
// frontend/src/components/PriceReferenceDialog.tsx
import { useEffect, useState } from "react";
import { ClearPriceRef, GetPriceCandidateImage, GetPriceCandidates, SetPriceRef } from "../../wailsjs/go/main/App";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { CardImage } from "@/components/CardImage";
import { errorText, useNotices } from "@/components/Notices";
import { formatEUR } from "@/lib/prices";
import type { CardInfo, PriceCandidateView } from "@/types";

interface Props {
  cardId: string | null; // null = closed
  image: string | null;  // our reference image of the card
  info: CardInfo | null;
  onClose: () => void;
}

// "Trend €3.20 · foil €3.50 · other languages €2.00", the columns a candidate has.
function priceText(p: PriceCandidateView["price"]): string {
  const parts: string[] = [];
  if (p.trend > 0) parts.push(`Trend ${formatEUR(p.trend)}`);
  if (p.trend_foil > 0) parts.push(`foil ${formatEUR(p.trend_foil)}`);
  if (p.trend_non_english > 0) parts.push(`other languages ${formatEUR(p.trend_non_english)}`);
  return parts.length ? parts.join(" · ") : "No price today";
}

// Asks which Cardmarket printing a card id is, among the candidates the
// bundle ships, with CardTrader's scan and today's price per printing.
// The choice is stored per card id; digits pick a tile, Escape closes.
export function PriceReferenceDialog({ cardId, image, info, onClose }: Props) {
  const { notify } = useNotices();
  const [views, setViews] = useState<PriceCandidateView[]>([]);
  const [scans, setScans] = useState<Record<string, string | null>>({}); // url -> data URL, null = failed
  const [busy, setBusy] = useState(false);
  const open = cardId !== null;

  useEffect(() => {
    if (!cardId) return;
    let cancelled = false;
    setViews([]);
    GetPriceCandidates(cardId)
      .then((v) => { if (!cancelled) setViews((v ?? []) as PriceCandidateView[]); })
      .catch((e) => { if (!cancelled) notify("error", `Couldn't load the printings: ${errorText(e)}`); });
    return () => { cancelled = true; };
  }, [cardId]);

  // One scan at a time, in tile order; a failure leaves a "no image" box.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      for (const v of views) {
        if (!v.image || Object.hasOwn(scans, v.image)) continue;
        const data = await GetPriceCandidateImage(v.image).catch(() => "");
        if (cancelled) return;
        setScans((prev) => ({ ...prev, [v.image]: data || null }));
      }
    })();
    return () => { cancelled = true; };
  }, [views]);

  async function choose(product: number | null) {
    if (!cardId) return;
    setBusy(true);
    try {
      if (product === null) await ClearPriceRef(cardId);
      else await SetPriceRef(cardId, product);
      onClose();
    } catch (e) {
      notify("error", `Couldn't save the reference: ${errorText(e)}`);
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      const i = parseInt(e.key, 10);
      if (!isNaN(i) && i >= 1 && i <= views.length) choose(views[i - 1].product);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, views]);

  const columns = Math.min(Math.max(views.length, 1), 3);

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-5xl max-h-[88vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Which Cardmarket printing is {cardId}?</DialogTitle>
          <DialogDescription>
            {info?.name ? `${info.name}. ` : ""}Compare your card with CardTrader's scans: the foil pattern and the stamp show there. Press a tile's number to choose it.
          </DialogDescription>
        </DialogHeader>
        <div className="flex gap-6 min-h-0">
          <div className="w-[220px] shrink-0">
            <CardImage src={image} alt={cardId ?? ""} adaptive className="w-full rounded-md" />
            <p className="m-0 mt-2 text-xs n-n500">Our reference image</p>
          </div>
          <div className="grid gap-4 overflow-y-auto min-h-0 p-1 flex-1 content-start" style={{ gridTemplateColumns: `repeat(${columns}, minmax(180px, 1fr))` }}>
            {views.map((v, i) => {
              const scan = v.image ? scans[v.image] : null;
              return (
                <button
                  key={v.product}
                  type="button"
                  disabled={busy}
                  className="flex flex-col gap-2 rounded-lg p-2 text-left transition-colors hover:border-[var(--n-neutral-700)]"
                  style={{ border: `1px solid ${v.chosen ? "var(--n-accent)" : "var(--n-divider)"}` }}
                  onClick={() => choose(v.product)}
                >
                  {scan ? (
                    <img src={scan} alt="" className="w-full rounded-md" style={{ aspectRatio: "63 / 88", objectFit: "contain", background: "#000" }} />
                  ) : (
                    <div className="w-full rounded-md flex items-center justify-center n-n500 text-xs" style={{ aspectRatio: "63 / 88", background: "#000" }}>
                      {v.image && scan === undefined ? "Loading…" : "No image"}
                    </div>
                  )}
                  <span className="flex items-center gap-2">
                    <kbd className="n-tag n-tag-outline font-mono" style={{ fontSize: "10px", padding: "1px 7px" }}>{i + 1}</kbd>
                    <span className="text-sm">{v.label}</span>
                  </span>
                  <span className="text-xs n-n400 tabular-nums">{priceText(v.price)}</span>
                  {v.chosen && <span className="n-tag n-tag-outline" style={{ fontSize: "10px", padding: "1px 7px" }}>Chosen</span>}
                </button>
              );
            })}
            {views.length === 0 && <p className="m-0 text-sm n-n400">No printing to choose from.</p>}
          </div>
        </div>
        <DialogFooter>
          <button type="button" className="n-btn n-btn-secondary" disabled={busy} onClick={() => choose(null)}>
            None of these
          </button>
          <button type="button" className="n-btn n-btn-secondary" onClick={onClose}>
            Close
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 2: Wire it**

In `CardCopiesDialog.tsx`, add the prop `onSetReference?: () => void;` and, in the chip block from Task 8, when `refState === "unset" && onSetReference`, render after the text:
```tsx
              <button type="button" className="n-btn n-btn-secondary" style={{ padding: "2px 10px" }} onClick={onSetReference}>
                Set reference
              </button>
```
Also show the same button labelled "Change reference" when `price?.ref` is true (the owner may revisit a choice): add a line under the price block:
```tsx
          {price?.ref && onSetReference && (
            <p className="m-0 text-xs n-n500 pt-1">
              Priced on your Cardmarket reference.{" "}
              <button type="button" className="underline" onClick={onSetReference}>Change</button>
            </p>
          )}
```

In `CollectionScreen.tsx`: `const [refCard, setRefCard] = useState<string | null>(null);`, pass `onSetReference={() => { if (openCard) setRefCard(openCard); }}` to `CardCopiesDialog`, and render after it:
```tsx
      <PriceReferenceDialog
        cardId={refCard}
        image={refCard ? images[refCard] ?? null : null}
        info={refCard && Object.hasOwn(infos, refCard) ? infos[refCard] : null}
        onClose={() => setRefCard(null)}
      />
```
The prices refetch by themselves: `SetPriceRef` emits `prices:updated`, `useCollection` ticks `pricesVersion`, and the screen's effect refetches `GetCardPrices`.

- [ ] **Step 3: Build and check live**

Run: `cd frontend && npm run build`. Then `wails dev`: open the copies dialog of an unsettled One Piece id, click "Set reference": the dialog shows our image on the left and one tile per printing with CardTrader's scan (the Jolly Roger pattern is visible on the foil tile), label and price; press `2`: the dialog closes, the tile now shows the price, the copies dialog says "Priced on your Cardmarket reference. Change"; "None of these" clears it and the chip returns. Check `./prices/images/` holds one `.jpg` per scan shown.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/PriceReferenceDialog.tsx frontend/src/components/CardCopiesDialog.tsx frontend/src/components/CollectionScreen.tsx
git commit -m "feat(frontend): price reference selector, choose a card's Cardmarket printing from its candidates"
```

---

### Task 10: Delivery 3 — labels in the duplicate selector

**Files:**
- Modify: `frontend/src/components/VariantChoiceDialog.tsx`

**Interfaces:**
- Consumes: `GetPriceRefs`, `GetCardPrices` (wailsjs), `copyValue`, `formatEUR`, `PriceRef`, `CardPrice`.
- Produces: nothing new; the dialog's props are unchanged.

- [ ] **Step 1: Fetch the references and prices when the dialog opens**

In `VariantChoiceDialog.tsx`, import `GetCardPrices, GetPriceRefs` alongside `GetCardImage, GetCardInfo`, `copyValue, formatEUR` from `@/lib/prices`, and the `CardPrice, PriceRef` types. Add state:
```tsx
  const [refs, setRefs] = useState<Record<string, PriceRef>>({});
  const [prices, setPrices] = useState<Record<string, CardPrice>>({});
```
and an effect:
```tsx
  useEffect(() => {
    if (!candidates) return;
    let cancelled = false;
    const ids = candidates.map((c) => c.card_id);
    Promise.all([GetPriceRefs().catch(() => ({})), GetCardPrices(ids).catch(() => ({}))]).then(([r, p]) => {
      if (cancelled) return;
      setRefs((r ?? {}) as Record<string, PriceRef>);
      setPrices((p ?? {}) as Record<string, CardPrice>);
    });
    return () => { cancelled = true; };
  }, [candidates]);
```

- [ ] **Step 2: The label line**

Inside each tile, after the `{info && (...)}` block:
```tsx
                {(() => {
                  const ref = refs[c.card_id];
                  const price = prices[c.card_id];
                  if (ref) {
                    const v = copyValue({ foil: false, lang: "en" }, price);
                    return (
                      <span className="text-xs n-n400">
                        {ref.label}{v !== null ? ` · ${formatEUR(v)}` : ""}
                      </span>
                    );
                  }
                  if (price && price.candidates > 0) {
                    return <span className="text-xs" style={{ color: "var(--n-warn)" }}>Price reference not set</span>;
                  }
                  return null;
                })()}
```
Update the description: "These printings share the same artwork. Check the number printed at the bottom of your card, and the Cardmarket printing you chose for each."

- [ ] **Step 3: Build and check live**

Run: `cd frontend && npm run build`. Then `wails dev`: scan a card whose twins are listed (or trigger the dialog with a card having identical-artwork printings, e.g. `OP01-006_p3`/`_p4`): under the id of a twin with a reference, its label and price; under one without, "Price reference not set".

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/VariantChoiceDialog.tsx
git commit -m "feat(frontend): duplicate selector shows each twin's Cardmarket reference and price"
```

---

### Task 11: Status note

**Files:**
- Modify: `STATUS.md`

- [ ] **Step 1: Record the state**

In `STATUS.md`, under the desktop section, after the "Rule C" paragraph, insert (fill the date and the numbers printed by Task 1's run):

```markdown
**Price reference selector (shipped YYYY-MM-DD):** spec
`docs/superpowers/specs/2026-10-08-price-reference-selector-design.md`. The
identical twins (same artwork, a treatment no image shows) are settled in
the app by the owner, card in hand. Mapper: `pixel_tiebreak.py` settles the
different-artwork printings into `visual.json` (N settled, N tied, N twin,
N low of 880; `pixel_status.json` orders the review tool's queue, "low"
first) and `cardmarket.json` ships `candidates` for the rest (product,
non-English twin, label, CardTrader scan URL; never both in `cards` and
`candidates`). App: `price_refs` table (one product per card id, outranks
the bundle), bindings `GetPriceCandidates` / `SetPriceRef` / `ClearPriceRef`
/ `GetPriceRefs` / `GetPriceCandidateImage` (scans fetched on demand into
`./prices/images/`); UI in three commits: the "no ref" chip on tiles and in
the copies dialog, the `PriceReferenceDialog` (our image vs the scans, with
prices, digits pick), the twin's reference and price under each id in the
scan duplicate selector.
```

Replace the "Owner's steps" list with:

```markdown
**Owner's steps (desktop PC):**

1. Repackage the bundle with the usual `package_app_model.py` command: it
   copies `cardmarket.json`, candidates included, into `metadata/`.
2. Launch the app: a One Piece card with a "no ref" chip opens its copies
   dialog, "Set reference" shows the printings with CardTrader's scans and
   today's prices; choose with the card in hand.
3. The review tool (`cd scripts/fetch/cardmarket/review; python review.py`)
   now lists the 73 "low" cards first: the ones where our image matches no
   candidate, worth a look; the twins can be skipped, the app handles them.
4. After new picks, `cd scripts/fetch/cardmarket; go run . -games onepiece -offline -report report.txt`
   and commit `manual.json` + `cardmarket.json`; repackage.
```

- [ ] **Step 2: Commit**

```bash
git add STATUS.md
git commit -m "docs: project status, price reference selector"
```
