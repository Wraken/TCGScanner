# Variant choice for identical printings — design

**Date:** 2026-09-07
**Status:** approved design, pending implementation plan

## Goal

When the scanned card matches several printings that share the same artwork
(cross-set reprints, One Piece parallels, Riftbound star foils, Lorcana
enchanted…), the matcher cannot tell them apart: only the collector number,
set symbol and copyright line differ, and the webcam cannot read them. Today
such cards either never lock (margin ≈ 0 between the twins) or lock silently
on one printing when an alias folds the twins together. Instead, the app
detects the ambiguity, shows the candidate printings side by side and lets the
user pick the right one by reading the bottom of the physical card.

## Decisions (user, 2026-09-07)

- The popup shows **every printing with identical artwork**, including
  same-set variants (One Piece `_pN`, Riftbound `-star-`), because those are
  the versions whose price differs. Reprints are no longer folded away.
- In a batch, the popup **preselects the last version picked** for the same
  group of candidates; Enter confirms. Nothing is applied silently, nothing is
  persisted across app restarts.
- Baseline knowledge: `scripts/embeddingtest/index_twins.py` (Lorcana 18.8 %
  of cards have a twin at cosine ≥ 0.9, Pokémon 9.8 %, Riftbound 1.1 %, One
  Piece 0 % once `_pN` are folded).

## Detection (Go, embedding pipeline)

`match` already scores the query against every reference (`m.scores`) and the
model keeps every L2-normalised reference vector in memory (`m.vectors`).
After the top hit `idx` is known:

- **Candidates** = every reference `i` with
  `scores[i] >= scores[idx] - marginMin` **and** either
  `dot(vectors[idx], vectors[i]) >= twinSim` (the two *references* are
  near-identical, so the frame is not what makes them close) **or**
  `canonicalCard(labels[i]) == canonicalCard(labels[idx])` (a declared
  printing of the same face — One Piece `_pN`/`_rN`, Riftbound `-star-`,
  `reprintAliases` — is always offered when it is within the margin: 13 One
  Piece `_r1` pairs with identical art sit just under 0.9). `idx` itself
  always qualifies. Sorted by score, descending, capped at `maxCandidates`.
- Candidates are reported only when the top score is at least `scoreFloor`
  (a blurry frame with many weak neighbours is not an ambiguity) and there
  are **at least two** of them.
- `Confident` becomes
  `score >= scoreFloor && margin >= marginMin && len(candidates) < 2`.
  The margin rule is unchanged (still measured against the best *different
  canonical* card), so unaliased cross-set reprints keep a near-zero margin
  and aliased/folded twins keep their full margin — in both cases the
  candidate rule is what blocks the lock and surfaces the choice.

New config knobs in `EmbeddingConfig`, with Go defaults when missing or ≤ 0
(`config.json` in the bundle need not change; the packager is untouched):

```go
TwinSim       float64 `json:"twin_sim"`        // default 0.9, same bar as index_twins.py
MaxCandidates int     `json:"max_candidates"`  // default 10 (the index holds twin sets of 8)
```

`Prediction` gains:

```go
// Candidates lists the printings this frame cannot tell apart: every
// reference within MarginMin of the top hit whose reference vector is itself
// near-identical to the winner's (cosine >= TwinSim). Present only when there
// are at least two; the UI then asks the user which printing it is. Sorted by
// score, best first, capped at MaxCandidates. Only the embedding pipeline
// sets it.
Candidates []Candidate `json:"candidates,omitempty"`

type Candidate struct {
	CardID string  `json:"card_id"`
	Score  float64 `json:"score"`
}
```

`match` returns the candidate index list alongside `idx, score, margin`
(signature `match(query) (idx int, score, margin float32, candidates []int)`);
`Predict` maps indices to labels/scores. `canonicalCard` and `reprintAliases`
stay as they are: they still serve the margin and `index_twins.py`, they just
no longer make a twin lock silently.

## Stability and state (frontend, `usePrediction`)

- A prediction with `candidates.length >= 2` is *pending*. Consecutive
  ambiguous frames whose candidate sets **overlap** (share at least one card
  id) count together; **3 in a row** (`CHOICE_STABLE_FRAMES`) open the choice
  with the latest set, best first. Sets are compared by overlap, not equality:
  a twin hovering at the margin boundary flickers in and out between frames
  and must not starve the count. A frame with unrelated candidates or no
  card resets it. This mirrors the existing auto-lock caution.
- While `choice !== null` scanning pauses exactly like `locked` (the interval
  effect returns early; `choice` joins its dependencies).
- `pickCandidate(cardId)`: remembers the pick under **every id of the shown
  set** (`lastPick[id] = cardId`, a `Map` in a ref, session-only — the set
  shown for a group varies with margin flicker, its ids do not), records
  `pickedCardID`, clears `choice`, then behaves like the confident lock:
  `GetCardImage(cardId)` → `cardImage`, `setEditedCardID(cardId)`. The Card
  ID field, Finish, Language and Save work unchanged from there.
- `dismissChoice()`: clears `choice`, resets the pending counter and records
  the dismissed ids as *suppressed*: frames whose candidates overlap them are
  ignored, and a non-ambiguous frame whose `card_id` is one of them (blurry,
  below the score floor) keeps the suppression; only unrelated candidates,
  another card or no card lift it. Scanning resumes.
- A `Predict` still in flight when scanning pauses (choice opened, lock,
  camera or model change) is discarded: its late result must not lock behind
  the dialog.
- `resetCard()` also clears `choice`, the pending counter and the suppressed
  key. `lastPick` survives `resetCard` (that is the whole point of the batch
  memory).
- The hook exposes `choice: Candidate[] | null`, `preselected: string | null`
  (a remembered pick for any id of the set when it is still offered, else the
  best candidate), `pickedCardID`, `pickCandidate`, `dismissChoice`.

## Popup (`VariantChoiceDialog`, new component)

A shadcn `Dialog`, opened when `choice !== null`, closing = dismiss.

- Title "Which printing is it?", subtitle "These cards share the same
  artwork — check the number at the bottom of your card."
- One tile per candidate (2 to 10; the content scrolls beyond one screen), in
  a responsive grid: the card image via
  `GetCardImage` rendered with `CardImage` (never cropped), then the metadata
  from `GetCardInfo`: set name + set code, `#number`, rarity as tags, and the
  card id in mono as fallback when there is no metadata. Score is not shown
  (it is meaningless between twins).
- The preselected tile carries the accent border; clicking any tile picks it;
  **Enter** picks the preselected tile; **Escape** or the close button
  dismisses. A footer button "None of these" dismisses too.
- Images and infos are fetched when the dialog opens and cached per card id
  for the dialog's lifetime (a batch reopens on the same ids).

`ScanScreen` renders the dialog from `pred.choice`; while candidates are
pending but not yet stable, the confidence line reads
`"NN% · N printings match"` instead of `"NN% confidence"`, and after a pick it
reads `"Printing chosen manually"`. Header and footer of the dialog stay
fixed; the tile grid (200 px tiles, up to 3 columns) scrolls.

## Frontend types

`Prediction.candidates?: Candidate[]`, `Candidate { card_id: string; score:
number }`.

## Testing

Go (`go test ./...`):
- `model_match_test.go` (new): `match` on a hand-built `EmbeddingModel`
  (synthetic 4-dim vectors, no ONNX): two near-identical references within the
  margin → both are candidates, `Confident` false; a runner-up within the
  margin whose reference is *not* near-identical → no candidates (genuine
  ambiguity between different cards stays "not confident, no popup"); a twin
  whose score falls outside the margin → no candidates; top score below the
  floor → no candidates; cap at `MaxCandidates`; sort order.
- `model_margin_test.go`: the existing `TestMarginExcludesTwins` keeps
  passing (margin unchanged); a new assertion on the same four real pairs:
  `Predict`-level `Candidates` contains both ids of the pair and `Confident`
  is false (skips when the reference images are missing, like today).
- Config defaults: missing/zero `twin_sim` and `max_candidates` fall back to
  0.9 and 6.

Frontend: `tsc --noEmit`; live pass in `wails dev` with a Riftbound card that
has a star variant (e.g. ven-194-166 vs its star) or a Lorcana reprint: the
popup opens after ~0.6 s of steady framing, Enter picks the preselected
version, Save records it; a second card of the same group preselects the last
pick; Escape resumes scanning without reopening on the same card.

## Out of scope (noted for later)

- Applying the last pick silently, or persisting picks across restarts.
- Reading the collector number (constrained recognition among the candidate
  numbers) to pick automatically.
- A manual "choose version" button on a non-ambiguous match.
- Cleaning `reprintAliases` / `canonicalCard` (still used by the margin and
  by `index_twins.py`).
- Writing `twin_sim` / `max_candidates` into the packager's `config.json`.

## Review notes (2026-09-07, Fable quality review of the matcher)

- Popup rate on a clean scan ≈ share of labels with any neighbour ≥ 0.9:
  Lorcana 18.8 % and Pokémon 9.8 % (previously unlockable), One Piece 6.4 %
  and Riftbound 5.9 % (mostly `_r1` / `-star-` printings that used to lock
  silently on one version).
- 0.9 measures the embedding, not the artwork: a few Pokémon Pocket
  "shiny background" cards of different Pokémon sit above the bar. They were
  unlockable before; a popup is strictly better than hanging.
