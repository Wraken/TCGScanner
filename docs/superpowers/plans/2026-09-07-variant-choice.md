# Variant Choice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a scan matches several printings with identical artwork, surface them as candidates and let the user pick the right one in a popup instead of never locking (or locking silently on one printing).

**Architecture:** The Go embedding pipeline already scores the query against every reference and keeps every reference vector in memory; `match` additionally returns the references within the margin of the winner whose *reference* vector is near-identical to the winner's (cosine ≥ 0.9) — the candidates. `Prediction` carries them and is never confident when there are two or more. The frontend hook waits for the same candidate set on three consecutive frames, pauses scanning and opens a dialog that shows each printing with its metadata; picking one behaves like the confident lock. Spec: `docs/superpowers/specs/2026-09-07-variant-choice-design.md`.

**Tech Stack:** Go 1.26 (`go test ./...`; model tests skip without the local bundle), Wails v2 bindings (`wails generate module`, `frontend/wailsjs/` gitignored), React 18 + TypeScript + shadcn Dialog (`npx tsc --noEmit`).

**Conventions:** UI copy in English. Never commit `models/`, `collection.db`, `frontend/wailsjs/`. Commit messages end with the trailer used by today's commits (`git log -3`). Branch `main`. Run `go vet .` and `go test .` separately (`go vet` fails on pre-existing `unsafe.Pointer` warnings in `pkg/tflite` and `pkg/ort`).

---

## File map

| File | Responsibility |
|---|---|
| `model.go` | `Prediction.Candidates`, `Candidate` |
| `model_embedding.go` | config knobs + defaults, `match` returns candidates, `confident`, `dot`, `Predict` fills candidates |
| `model_match_test.go` (new) | synthetic-vector tests of `match`/`confident`, config defaults |
| `model_embedding_test.go` | `testRefDir` → `scripts/embeddingtest/images/riftbound`, `loadRefImage` helper |
| `model_margin_test.go` | adapt to the new `match` signature; twins surface as candidates |
| `frontend/src/types.ts` | `Candidate`, `Prediction.candidates` |
| `frontend/src/hooks/usePrediction.ts` | stability tracking, `choice`, `preselected`, `pickCandidate`, `dismissChoice` |
| `frontend/src/components/VariantChoiceDialog.tsx` (new) | the popup |
| `frontend/src/components/ScanScreen.tsx` | renders the dialog, confidence line |

---

### Task 1: Candidates in the Go matcher

**Files:**
- Modify: `model.go`, `model_embedding.go`, `model_embedding_test.go`, `model_margin_test.go`
- Create: `model_match_test.go`

- [ ] **Step 1: Write the failing tests**

Create `model_match_test.go`:

```go
package main

import (
	"math"
	"testing"
)

// synthIndex builds an EmbeddingModel with hand-made reference vectors (rows
// are L2-normalised here) and no ONNX sessions: enough to exercise match and
// confident, which only touch labels, canon, vectors and scores.
func synthIndex(t *testing.T, dim int, labels []string, rows [][]float32, maxCandidates int) *EmbeddingModel {
	t.Helper()
	if len(labels) != len(rows) {
		t.Fatalf("%d labels for %d rows", len(labels), len(rows))
	}
	vectors := make([]float32, 0, len(rows)*dim)
	for _, r := range rows {
		if len(r) != dim {
			t.Fatalf("row %v has %d dims, want %d", r, len(r), dim)
		}
		vectors = append(vectors, unit(r)...)
	}
	canon := make([]string, len(labels))
	for i, l := range labels {
		canon[i] = canonicalCard(l)
	}
	return &EmbeddingModel{
		dim: dim, n: len(labels), labels: labels, canon: canon, vectors: vectors,
		scores: make([]float32, len(labels)),
		scoreFloor: defaultScoreFloor, marginMin: defaultMarginMin,
		twinSim: float32(defaultTwinSim), maxCandidates: maxCandidates,
	}
}

func unit(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	n = math.Sqrt(n)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}

func labelsOf(m *EmbeddingModel, idx []int) []string {
	out := make([]string, len(idx))
	for i, j := range idx {
		out[i] = m.labels[j]
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Eight dims: references live in the first four, so a query on dim 5 scores 0
// against everything (below the floor).
//   x-001 / y-001 : near-identical references (cosine 0.99) — twins.
//   d-003 / e-004 : cosine 0.8 — similar but NOT twins.
//   f-005 / g-005 : cosine 0.91 — twins, used with a frame that favours f.
func twinIndex(t *testing.T) *EmbeddingModel {
	return synthIndex(t, 8,
		[]string{"x-001", "y-001", "d-003", "e-004", "f-005", "g-005"},
		[][]float32{
			{1, 0, 0, 0, 0, 0, 0, 0},
			{0.99, 0.141, 0, 0, 0, 0, 0, 0},
			{0, 0, 1, 0, 0, 0, 0, 0},
			{0, 0, 0.6, 0.8, 0, 0, 0, 0},
			{0, 0, 0, 0, 0, 0, 1, 0},
			{0, 0, 0, 0, 0, 0, 0.91, 0.4146},
		}, defaultMaxCandidates)
}

func TestMatchTwinsAreCandidates(t *testing.T) {
	m := twinIndex(t)
	idx, score, margin, cand := m.match(unit([]float32{1, 0, 0, 0, 0, 0, 0, 0}))
	if m.labels[idx] != "x-001" {
		t.Fatalf("top-1 = %s, want x-001", m.labels[idx])
	}
	if got := labelsOf(m, cand); !equalStrings(got, []string{"x-001", "y-001"}) {
		t.Errorf("candidates = %v, want [x-001 y-001] (best first)", got)
	}
	if m.confident(score, margin, len(cand)) {
		t.Errorf("confident with two candidates (score %.3f margin %.3f)", score, margin)
	}
}

func TestMatchSimilarButDifferentCardsAreNotCandidates(t *testing.T) {
	m := twinIndex(t)
	// d scores 0.9, e scores 0.889: within the margin, but their references
	// are only 0.8 apart — a genuinely ambiguous frame, not a printing issue.
	idx, score, margin, cand := m.match(unit([]float32{0, 0, 0.9, 0.436, 0, 0, 0, 0}))
	if m.labels[idx] != "d-003" {
		t.Fatalf("top-1 = %s, want d-003", m.labels[idx])
	}
	if cand != nil {
		t.Errorf("candidates = %v, want none", labelsOf(m, cand))
	}
	if m.confident(score, margin, len(cand)) {
		t.Errorf("confident with margin %.3f", margin)
	}
}

func TestMatchTwinOutsideMarginLocks(t *testing.T) {
	m := twinIndex(t)
	// The frame clearly favours f (0.954) over its twin g (0.744): no
	// candidates, and the margin rule locks as before.
	idx, score, margin, cand := m.match(unit([]float32{0, 0, 0, 0, 0, 0, -0.3, 0.954}))
	if m.labels[idx] != "g-005" && m.labels[idx] != "f-005" {
		t.Fatalf("top-1 = %s", m.labels[idx])
	}
	if cand != nil {
		t.Errorf("candidates = %v, want none", labelsOf(m, cand))
	}
	if !m.confident(score, margin, len(cand)) {
		t.Errorf("not confident: score %.3f margin %.3f", score, margin)
	}
}

func TestMatchBelowFloorHasNoCandidates(t *testing.T) {
	m := twinIndex(t)
	_, score, _, cand := m.match(unit([]float32{0, 0, 0, 0, 1, 0, 0, 0}))
	if score >= float32(m.scoreFloor) {
		t.Fatalf("query should score below the floor, got %.3f", score)
	}
	if cand != nil {
		t.Errorf("candidates = %v below the floor, want none", labelsOf(m, cand))
	}
}

func TestMatchCandidatesCappedAndSorted(t *testing.T) {
	// Five near-identical printings, cap at 3: the winner first, then the
	// best-scoring twins.
	labels := []string{"p-0", "p-1", "p-2", "p-3", "p-4"}
	rows := make([][]float32, len(labels))
	for k := range rows {
		rows[k] = []float32{1, float32(k) * 0.01, 0, 0, 0, 0, 0, 0}
	}
	m := synthIndex(t, 8, labels, rows, 3)
	idx, _, _, cand := m.match(unit([]float32{1, 0, 0, 0, 0, 0, 0, 0}))
	if m.labels[idx] != "p-0" {
		t.Fatalf("top-1 = %s, want p-0", m.labels[idx])
	}
	if got := labelsOf(m, cand); !equalStrings(got, []string{"p-0", "p-1", "p-2"}) {
		t.Errorf("candidates = %v, want [p-0 p-1 p-2]", got)
	}
}

func TestEmbeddingConfigDefaults(t *testing.T) {
	cfg := EmbeddingConfig{}
	applyConfigDefaults(&cfg)
	if cfg.TwinSim != defaultTwinSim || cfg.MaxCandidates != defaultMaxCandidates ||
		cfg.ScoreFloor != defaultScoreFloor || cfg.MarginMin != defaultMarginMin || cfg.ImgSize != defaultEmbeddingImgSize {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	cfg = EmbeddingConfig{TwinSim: 0.95, MaxCandidates: 1}
	applyConfigDefaults(&cfg)
	if cfg.TwinSim != 0.95 {
		t.Errorf("explicit twin_sim overridden: %v", cfg.TwinSim)
	}
	if cfg.MaxCandidates != defaultMaxCandidates {
		t.Errorf("max_candidates < 2 must fall back to the default, got %d", cfg.MaxCandidates)
	}
}
```

In `model_embedding_test.go`, point the reference dir at where the twin pairs' images live today and add a loader:

```go
const (
	testModelDir = "models/tcg"
	// The Riftbound sources: every twin pair used by the tests lives here.
	testRefDir = "scripts/embeddingtest/images/riftbound"
)

// loadRefImage decodes testRefDir/<id>.png, skipping the test when the local
// source images are not present.
func loadRefImage(t *testing.T, id string) image.Image {
	t.Helper()
	path := filepath.Join(testRefDir, id+".png")
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("reference image missing: %s", path)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}
```

In `model_margin_test.go`, adapt the existing call `idx, score, margin := m.match(emb)` to `idx, score, margin, _ := m.match(emb)` and append:

```go
// The other half of the twin story: the margin ignores a twin (so the winner
// looks confident), but the app must not lock on one printing silently. Both
// twins come back as candidates and the match is not confident.
func TestTwinsSurfaceAsCandidates(t *testing.T) {
	m := loadTestModel(t)
	pairs := [][2]string{
		{"ogn-042-298", "ven-r02"},
		{"ogn-214-298", "ven-r06"},
		{"sfd-t03", "unl-t05"},
		{"ven-194-166", "ven-194-star-166"},
	}
	for _, pair := range pairs {
		img := loadRefImage(t, pair[0])
		emb, err := m.embedCrop(img, img.Bounds())
		if err != nil {
			t.Fatalf("embed %s: %v", pair[0], err)
		}
		idx, score, margin, cand := m.match(emb)
		got := map[string]bool{}
		for _, i := range cand {
			got[m.labels[i]] = true
		}
		if !got[pair[0]] || !got[pair[1]] {
			t.Errorf("%s: candidates %v, want both %s and %s (top1=%s score=%.3f margin=%.3f)",
				pair[0], labelsOf(m, cand), pair[0], pair[1], m.labels[idx], score, margin)
			continue
		}
		if m.confident(score, margin, len(cand)) {
			t.Errorf("%s: confident despite %d candidates", pair[0], len(cand))
		}
		t.Logf("%-18s candidates=%v score=%.3f margin=%.3f", pair[0], labelsOf(m, cand), score, margin)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestMatch|TestEmbeddingConfigDefaults|TestTwinsSurfaceAsCandidates|TestMarginExcludesTwins' .`
Expected: build failure (`undefined: defaultTwinSim`, `m.match` returns 3 values, `m.confident undefined`, `applyConfigDefaults undefined`).

- [ ] **Step 3: `model.go` — the candidate list**

After the `Box` field of `Prediction`:

```go
	// Candidates lists the printings this frame cannot tell apart: every
	// reference within MarginMin of the top hit whose reference vector is
	// itself near-identical to the winner's (cosine >= TwinSim). Present only
	// when there are at least two; the UI then asks the user which printing
	// it is. Sorted by score, best first, capped at MaxCandidates. Only the
	// embedding pipeline sets it.
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Candidate is one printing of an ambiguous match.
type Candidate struct {
	CardID string  `json:"card_id"`
	Score  float64 `json:"score"`
}
```

- [ ] **Step 4: `model_embedding.go` — config, match, confident, Predict**

Constants, next to `defaultScoreFloor` / `defaultMarginMin`:

```go
	// Two references whose vectors agree at least this much are printings of
	// the same artwork (the bar index_twins.py uses): a frame cannot separate
	// them, so they are offered as candidates instead of fought over.
	defaultTwinSim = 0.9
	// Upper bound on the candidates reported for one frame.
	defaultMaxCandidates = 6
```

`EmbeddingConfig` gains:

```go
	TwinSim       float64 `json:"twin_sim"`
	MaxCandidates int     `json:"max_candidates"`
```

Replace the inline defaulting in `NewEmbeddingModel` (the three `if cfg.X <= 0` blocks after the `json.Unmarshal`) with a call to a new function, and initialise `cfg` with `EmbeddingConfig{}`:

```go
// applyConfigDefaults fills every knob a bundle's config.json may omit.
func applyConfigDefaults(cfg *EmbeddingConfig) {
	if cfg.ImgSize == 0 {
		cfg.ImgSize = defaultEmbeddingImgSize
	}
	if cfg.ScoreFloor <= 0 {
		cfg.ScoreFloor = defaultScoreFloor
	}
	if cfg.MarginMin <= 0 {
		cfg.MarginMin = defaultMarginMin
	}
	if cfg.TwinSim <= 0 {
		cfg.TwinSim = defaultTwinSim
	}
	if cfg.MaxCandidates < 2 {
		cfg.MaxCandidates = defaultMaxCandidates
	}
}
```

```go
	cfg := EmbeddingConfig{}
	if data, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config.json: %w", err)
		}
	}
	applyConfigDefaults(&cfg)
```

`EmbeddingModel` gains two fields next to `marginMin`, set in the constructor:

```go
	twinSim       float32
	maxCandidates int
```

```go
		twinSim:       float32(cfg.TwinSim),
		maxCandidates: cfg.MaxCandidates,
```

`match` becomes:

```go
// match returns the closest reference vector, its cosine similarity, the
// margin by which it leads the best reference of a *different* card, and the
// candidates the frame cannot tell apart (Prediction.Candidates). Both sides
// are L2-normalized, so the dot product is the cosine.
//
// The margin, rather than the absolute score, is what tells a confident match
// from an ambiguous one: alternate printings of the winning card are excluded
// via the canonical id, so the runner-up is a genuinely different card. When
// there is no other card in the index (a single-card model), the margin is the
// winner's own score.
//
// Candidates are the references within marginMin of the winner whose own
// reference vector is near-identical to the winner's (cosine >= twinSim):
// printings that share the artwork, which no frame can separate. They are
// reported only when the winner clears scoreFloor and there is at least one
// such twin; the list starts with the winner, continues best first and is
// capped at maxCandidates. nil otherwise.
func (m *EmbeddingModel) match(query []float32) (idx int, score, margin float32, candidates []int) {
	// One pass to score everything, so the margin pass is plain comparisons.
	for i := 0; i < m.n; i++ {
		m.scores[i] = dot(m.vectors[i*m.dim:(i+1)*m.dim], query)
	}

	idx, score = -1, float32(-2)
	for i, s := range m.scores {
		if s > score {
			score, idx = s, i
		}
	}
	if idx < 0 {
		return -1, 0, 0, nil
	}

	winner := m.canon[idx]
	rival := float32(-2)
	for i, s := range m.scores {
		if s > rival && m.canon[i] != winner {
			rival = s
		}
	}
	if rival < -1 {
		margin = score // no different card in the index
	} else {
		margin = score - rival
	}

	if score < float32(m.scoreFloor) {
		return idx, score, margin, nil
	}
	top := m.vectors[idx*m.dim : (idx+1)*m.dim]
	for i, s := range m.scores {
		if i == idx || s < score-float32(m.marginMin) {
			continue
		}
		if dot(top, m.vectors[i*m.dim:(i+1)*m.dim]) >= m.twinSim {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return idx, score, margin, nil
	}
	sort.Slice(candidates, func(a, b int) bool { return m.scores[candidates[a]] > m.scores[candidates[b]] })
	candidates = append([]int{idx}, candidates...)
	if len(candidates) > m.maxCandidates {
		candidates = candidates[:m.maxCandidates]
	}
	return idx, score, margin, candidates
}

// confident is the lock rule: a clear score, a clear lead over every
// different card, and no printing the frame cannot tell apart.
func (m *EmbeddingModel) confident(score, margin float32, candidates int) bool {
	return score >= float32(m.scoreFloor) && margin >= float32(m.marginMin) && candidates < 2
}

// dot of two equal-length vectors; on L2-normalized inputs, the cosine.
func dot(a, b []float32) float32 {
	var s float32
	for j, v := range a {
		s += v * b[j]
	}
	return s
}
```

(Add `"sort"` to the imports if it is not there yet.)

In `Predict`, replace the tail from `idx, score, margin := m.match(embedding)`:

```go
	idx, score, margin, cand := m.match(embedding)
	if idx < 0 {
		return &Prediction{DetectScore: float64(detectScore), Box: boxNorm}, nil
	}

	pred := &Prediction{
		CardID:      m.labels[idx],
		Confidence:  float64(score),
		Margin:      float64(margin),
		Confident:   m.confident(score, margin, len(cand)),
		DetectScore: float64(detectScore),
		Box:         boxNorm,
	}
	for _, i := range cand {
		pred.Candidates = append(pred.Candidates, Candidate{CardID: m.labels[i], Score: float64(m.scores[i])})
	}
	return pred, nil
```

- [ ] **Step 5: Run the suite**

Run: `gofmt -l . ; go test .` (and `go vet .` separately — only the pre-existing `unsafe.Pointer` warnings are tolerated). Expected: gofmt prints nothing (only files touched by this task matter; `main.go` may already be listed because of CRLF, ignore it); `TestMatch*`, `TestEmbeddingConfigDefaults` PASS; `TestMarginExcludesTwins` and `TestTwinsSurfaceAsCandidates` PASS with the local bundle (they log the four pairs) or SKIP cleanly if `models/tcg` or the images are absent; everything else still green.

- [ ] **Step 6: Commit**

```bash
git add model.go model_embedding.go model_match_test.go model_embedding_test.go model_margin_test.go
git commit -m "feat: matcher reports the printings a frame cannot tell apart"
```

---

### Task 2: Frontend — types and the prediction hook

**Files:**
- Regenerate: `frontend/wailsjs/` via `wails generate module` (gitignored)
- Modify: `frontend/src/types.ts`, `frontend/src/hooks/usePrediction.ts`

- [ ] **Step 1: Regenerate the bindings**

From the repo root: `wails generate module`. Verify `grep -n "candidates" frontend/wailsjs/go/models.ts` shows the field on `Prediction` and a `Candidate` class.

- [ ] **Step 2: Types** (`frontend/src/types.ts`)

Add before `Prediction`:

```ts
// One printing of an ambiguous match (see Prediction.candidates).
export interface Candidate {
  card_id: string;
  score: number;
}
```

and inside `Prediction`, after `box?: BoxNorm;`:

```ts
  // Printings this frame cannot tell apart (identical artwork), best first;
  // at least two when present. The UI asks the user which one it is.
  candidates?: Candidate[];
```

- [ ] **Step 3: Hook** (`frontend/src/hooks/usePrediction.ts`)

Type import: `import type { Candidate, Prediction } from "@/types";`

After `SCAN_INTERVAL_MS`:

```ts
// Frames in a row with the same ambiguous candidate set before the choice
// dialog opens — the same caution as the confident lock, against flicker.
const CHOICE_STABLE_FRAMES = 3;

// Order-independent identity of a candidate set.
function candidateKey(candidates: Candidate[]): string {
  return candidates.map((c) => c.card_id).sort().join("|");
}
```

State and refs, after `const [foil, setFoil] = useState(false);`:

```ts
  // Ambiguous printings waiting for the user's pick; null = no dialog.
  const [choice, setChoice] = useState<Candidate[] | null>(null);
  const pendingKeyRef = useRef("");
  const pendingCountRef = useRef(0);
  // A dismissed set stays suppressed until a frame shows another set or no card.
  const suppressedKeyRef = useRef("");
  // Last printing picked per candidate set; session-only, survives resetCard.
  const lastPickRef = useRef(new Map<string, string>());
```

Helpers, after `resetCard` (and `resetCard` itself gains two lines):

```ts
  function resetCard() {
    if (cardImageTimerRef.current !== null) {
      clearTimeout(cardImageTimerRef.current);
      cardImageTimerRef.current = null;
    }
    setPrediction(null);
    setPredictionError("");
    setCardImage(null);
    setEditedCardID("");
    setFoil(false);
    setChoice(null);
    resetPending(true);
  }

  function resetPending(liftSuppression: boolean) {
    pendingKeyRef.current = "";
    pendingCountRef.current = 0;
    if (liftSuppression) suppressedKeyRef.current = "";
  }

  // Counts consecutive frames with the same candidate set; opens the choice
  // once it is stable. A different set lifts any suppression.
  function trackCandidates(candidates: Candidate[]) {
    const key = candidateKey(candidates);
    if (key === suppressedKeyRef.current) return;
    suppressedKeyRef.current = "";
    pendingCountRef.current = key === pendingKeyRef.current ? pendingCountRef.current + 1 : 1;
    pendingKeyRef.current = key;
    if (pendingCountRef.current >= CHOICE_STABLE_FRAMES) {
      pendingCountRef.current = 0;
      setChoice([...candidates].sort((a, b) => b.score - a.score));
    }
  }

  // The user's pick behaves like the confident lock. editedCardID is set in
  // the same tick as choice is cleared, so scanning never resumes in between.
  async function pickCandidate(cardId: string) {
    if (choice) lastPickRef.current.set(candidateKey(choice), cardId);
    setEditedCardID(cardId);
    setChoice(null);
    setCardImage(null);
    resetPending(true);
    const img = await GetCardImage(cardId).catch(() => null);
    setCardImage(img);
  }

  function dismissChoice() {
    if (choice) suppressedKeyRef.current = candidateKey(choice);
    pendingKeyRef.current = "";
    pendingCountRef.current = 0;
    setChoice(null);
  }

  // Preselected tile: the last pick for this set when still offered, else
  // the best-scoring printing.
  const preselected = choice
    ? (() => {
        const last = lastPickRef.current.get(candidateKey(choice));
        return last && choice.some((c) => c.card_id === last) ? last : choice[0].card_id;
      })()
    : null;
```

The scan effect: guard and dependencies gain `choice`:

```ts
    if (!modelLoaded || !selectedCamera || locked || choice) return;
```

```ts
  }, [modelLoaded, selectedCamera, locked, choice]);
```

Inside the effect, the branch handling a result becomes:

```ts
          if (!pred.card_id) {
            setPrediction(null);
            resetPending(true);
          } else {
            setPrediction(pred);
            // The backend decides when a prediction is certain enough to lock;
            // each model type uses its own scale (saturated softmax for the
            // classifier, score-plus-margin for the embedding pipeline).
            if (pred.confident) {
              resetPending(true);
              const img = await GetCardImage(pred.card_id).catch(() => null);
              setCardImage(img);
              setEditedCardID(pred.card_id);
            } else if (pred.candidates && pred.candidates.length >= 2) {
              trackCandidates(pred.candidates);
            } else {
              resetPending(true);
            }
          }
```

Return object gains `choice`, `preselected`, `pickCandidate`, `dismissChoice`.

- [ ] **Step 4: Type-check**

Run: `cd frontend && npx tsc --noEmit` — exit 0, no output.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types.ts frontend/src/hooks/usePrediction.ts
git commit -m "feat: prediction hook tracks ambiguous printings and the user's pick"
```

---

### Task 3: Frontend — the choice dialog

**Files:**
- Create: `frontend/src/components/VariantChoiceDialog.tsx`
- Modify: `frontend/src/components/ScanScreen.tsx`

- [ ] **Step 1: The dialog**

```tsx
import { useEffect, useRef, useState } from "react";
import { GetCardImage, GetCardInfo } from "../../wailsjs/go/main/App";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { CardImage } from "@/components/CardImage";
import type { Candidate, CardInfo } from "@/types";

interface Props {
  candidates: Candidate[] | null; // null = closed
  preselected: string | null;
  onPick: (cardId: string) => void;
  onDismiss: () => void;
}

// Asks which printing a scanned card is when several share the artwork.
// The preselected tile takes focus on open, so Enter picks it; Escape, the
// close button and "None of these" dismiss and let scanning resume.
export function VariantChoiceDialog({ candidates, preselected, onPick, onDismiss }: Props) {
  // Artwork and metadata per card id, kept for the dialog's lifetime: a
  // batch reopens on the same printings.
  const [images, setImages] = useState<Record<string, string>>({});
  const [infos, setInfos] = useState<Record<string, CardInfo | null>>({});
  const preselectedRef = useRef<HTMLButtonElement | null>(null);
  const open = candidates !== null;

  useEffect(() => {
    if (!candidates) return;
    let cancelled = false;
    for (const c of candidates) {
      if (!(c.card_id in images)) {
        GetCardImage(c.card_id)
          .then((img) => { if (!cancelled) setImages((prev) => ({ ...prev, [c.card_id]: img })); })
          .catch(() => {});
      }
      if (!Object.hasOwn(infos, c.card_id)) {
        GetCardInfo(c.card_id)
          .then((info) => { if (!cancelled) setInfos((prev) => ({ ...prev, [c.card_id]: info as CardInfo })); })
          .catch(() => { if (!cancelled) setInfos((prev) => ({ ...prev, [c.card_id]: null })); });
      }
    }
    return () => { cancelled = true; };
  }, [candidates]);

  const columns = Math.min(candidates?.length ?? 1, 3);

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onDismiss()}>
      <DialogContent
        className="max-w-4xl"
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          preselectedRef.current?.focus();
        }}
      >
        <DialogHeader>
          <DialogTitle>Which printing is it?</DialogTitle>
          <p className="text-xs n-n500">
            These cards share the same artwork — check the number at the bottom of your card.
          </p>
        </DialogHeader>
        <div className="grid gap-4" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
          {(candidates ?? []).map((c) => {
            const info = infos[c.card_id] ?? null;
            const selected = c.card_id === preselected;
            return (
              <button
                key={c.card_id}
                type="button"
                ref={selected ? preselectedRef : undefined}
                className="flex flex-col gap-2 rounded-lg p-2 text-left"
                style={{ border: `1px solid ${selected ? "var(--n-accent)" : "var(--n-divider)"}` }}
                onClick={() => onPick(c.card_id)}
              >
                <CardImage src={images[c.card_id] ?? null} alt={c.card_id} adaptive className="w-full rounded-md" />
                <span className="font-mono text-xs n-n400">{c.card_id}</span>
                {info && (
                  <div className="flex flex-wrap gap-1.5">
                    {(info.set_name || info.set) && (
                      <span className="n-tag n-tag-neutral">
                        {info.set_name || info.set}
                        {info.set_name && info.set && <span className="font-mono n-n500">{info.set}</span>}
                      </span>
                    )}
                    {info.number && <span className="n-tag n-tag-neutral font-mono">#{info.number}</span>}
                    {info.rarity && <span className="n-tag n-tag-neutral">{info.rarity}</span>}
                  </div>
                )}
              </button>
            );
          })}
        </div>
        <DialogFooter>
          <button type="button" className="n-btn n-btn-secondary" onClick={onDismiss}>
            None of these
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 2: Wire it in `ScanScreen.tsx`**

Import: `import { VariantChoiceDialog } from "@/components/VariantChoiceDialog";`

The confidence line under the Card ID input becomes:

```tsx
          {pred.prediction && (
            <p className="mt-2 text-center text-xs n-n400">
              {pred.editedCardID !== pred.prediction.card_id && pred.editedCardID.trim()
                ? "ID corrected manually"
                : pred.prediction.candidates && pred.prediction.candidates.length >= 2
                  ? `${pct(pred.prediction.confidence)}% · ${pred.prediction.candidates.length} printings match`
                  : `${pct(pred.prediction.confidence)}% confidence`}
            </p>
          )}
```

Render the dialog as the last child of the component's root `<div className="flex h-full" ...>` (after the right-hand panel):

```tsx
      <VariantChoiceDialog
        candidates={pred.choice}
        preselected={pred.preselected}
        onPick={pred.pickCandidate}
        onDismiss={pred.dismissChoice}
      />
```

- [ ] **Step 3: Type-check**

Run: `cd frontend && npx tsc --noEmit` — exit 0.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/VariantChoiceDialog.tsx frontend/src/components/ScanScreen.tsx
git commit -m "ui: choose the printing when several share the artwork"
```

---

### Task 4: Verification (controller + user)

- [ ] `go test ./...` green from the repo root (the twin-pair tests must RUN, not skip, on this machine: `go test -run 'TestTwinsSurfaceAsCandidates|TestMarginExcludesTwins' -v .` logs the four pairs); `cd frontend && npx tsc --noEmit` clean.
- [ ] `wails dev` live pass by the user: frame a Riftbound card that has a star variant (or a Lorcana reprint): the confidence line reads "… · 2 printings match", the dialog opens after ~0.6 s, the best tile has the accent border and focus, Enter picks it, Save records that id; a second card of the same group preselects the last pick; Escape resumes scanning and does not reopen on the same card until it leaves the frame.
- [ ] Close the SDD ledger (`.superpowers/sdd/2026-09-07-variant-choice/progress.md`) and update memory (`project_reprint_twins_baseline`: the variant-choice UI shipped; aliases no longer lock silently).
