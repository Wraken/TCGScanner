package main

import (
	"image"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestMarginExcludesTwins is the core guarantee of the canonical grouping: when
// a card has a reprint or variant in the index, the match margin must be
// measured against a genuinely different card, not against the near-identical
// twin sitting just behind it. Without grouping these pairs retrieve each other
// at ~0.98, so a naive top-2 margin collapses to almost zero and a perfect
// match reads as "ambiguous". With grouping the twin is skipped and the margin
// reflects the real runner-up.
func TestMarginExcludesTwins(t *testing.T) {
	m := loadTestModel(t)

	// Pairs the index actually contains: cross-set reprints and a star foil.
	pairs := [][2]string{
		{"ogn-042-298", "ven-r02"},
		{"ogn-214-298", "ven-r06"},
		{"sfd-t03", "unl-t05"},
		{"ven-194-166", "ven-194-star-166"},
	}

	refDir := testRefDir
	for _, pair := range pairs {
		refPath := filepath.Join(refDir, pair[0]+".png")
		f, err := os.Open(refPath)
		if err != nil {
			t.Skipf("reference image missing: %s", refPath)
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", refPath, err)
		}

		emb, err := m.embedCrop(img, img.Bounds())
		if err != nil {
			t.Fatalf("embed %s: %v", pair[0], err)
		}

		idx, score, margin, _ := m.match(emb)
		gotCanon := canonicalCard(m.labels[idx])
		wantCanon := canonicalCard(pair[0])

		// Top-1 must be the card itself or its twin (same canonical).
		if gotCanon != wantCanon {
			t.Errorf("%s: top-1 is %s (canon %s), want canon %s",
				pair[0], m.labels[idx], gotCanon, wantCanon)
			continue
		}

		// The raw runner-up should be the twin — confirm the situation the
		// grouping is meant to handle actually occurs.
		rawSecond, rawSecondScore := m.rawSecondBest(emb, idx)
		twinIsSecond := canonicalCard(m.labels[rawSecond]) == wantCanon

		if float64(margin) < m.marginMin {
			t.Errorf("%s: margin %.3f < %.3f — twin not excluded? (raw #2 = %s @ %.3f, sameCanon=%t)",
				pair[0], margin, m.marginMin, m.labels[rawSecond], rawSecondScore, twinIsSecond)
			continue
		}

		naiveMargin := score - rawSecondScore
		t.Logf("%-18s top1=%s score=%.3f  margin=%.3f (naive top-2 would be %.3f, twin=%s @ %.3f)",
			pair[0], m.labels[idx], score, margin, naiveMargin, m.labels[rawSecond], rawSecondScore)
	}
}

// rawSecondBest returns the best reference other than index skip, ignoring
// canonical grouping — the naive runner-up.
func (m *EmbeddingModel) rawSecondBest(query []float32, skip int) (int, float32) {
	type hit struct {
		i int
		s float32
	}
	hits := make([]hit, 0, m.n)
	for i := 0; i < m.n; i++ {
		if i == skip {
			continue
		}
		row := m.vectors[i*m.dim : (i+1)*m.dim]
		var s float32
		for j, v := range row {
			s += v * query[j]
		}
		hits = append(hits, hit{i, s})
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].s > hits[b].s })
	return hits[0].i, hits[0].s
}

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
