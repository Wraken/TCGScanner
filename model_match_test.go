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
		scores:     make([]float32, len(labels)),
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
//
//	x-001 / y-001 : near-identical references (cosine 0.99) — twins.
//	d-003 / e-004 : cosine 0.8 — similar but NOT twins.
//	f-005 / g-005 : cosine 0.91 — twins, used with a frame that favours f.
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
	idx, score, margin, cand := m.match(unit([]float32{0, 0, 0, 0, 0, 0, 0.954, -0.3}))
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
