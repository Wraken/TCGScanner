# Project status — 2026-09-21

Two repositories, one product: the desktop scanner (this repo) and the Android
app (`E:\TCGScannerMobile`, https://github.com/Wraken/TCGScannerMobile, private).
This file is the "where were we" note; the detailed docs are linked from each
section.

## TCGScanner (desktop, this repo)

**Branch `main`, 88 commits ahead of `origin/main`, never pushed since August.**
Push is the owner's call.

**Uncommitted in the working tree** (all from the September retrain work, all
tested, waiting for a commit decision):

| File | Change |
|---|---|
| `scripts/ml/train.py` | `--dataset-dir`, `--resume`, best-weights checkpoints per phase, `train_log.csv`, the dummy pass builds the model |
| `scripts/ml/build_index.py` | references decoded one at a time (31 k refs no longer need 55 GB of RAM), duplicate-id check |
| `model_embedding_test.go` | `TCG_TEST_MODEL_DIR` env override; self-retrieval judged up to `canonicalCard` |
| `scripts/ml/RETRAIN.md` (new) | the retrain runbook + the September run log |
| `README.md` | link to the runbook, Lorcana id example `1-1` |
| `frontend/vite.config.ts` | Vite on port 3000 (5173 fell into a Hyper-V reserved range after a reboot) |

**Shipped and live**
- Embedding model retrained on all four games (30 999 classes, `models/tcg` on
  both sides since 2026-09-20; August model kept as `models/tcg.pre-retrain-2026-09`).
  Offline gains: lock rate Pokémon 82 → 89 %, Lorcana 75 → 80 %, One Piece 76 → 72 %,
  Riftbound unchanged; live pass convincing. Details: `scripts/ml/RETRAIN.md`.
- Variant-choice popup for identical-artwork printings (2026-09-07).
- Card language per copy (code complete; live pass of the scan selector, the
  Saved line, the copies dialog and the CSV Language field still to do).
- Card metadata for all games, Nocturne redesign, One Piece English catalogue
  (4 843 cleaned refs), Riftbound metadata via `-extract`.

**Backlog (unchanged, by priority as discussed)**
1. Pokémon images: 53 listed + 61 recoverable from the CDN (fetcher fallback not
   written); ~1 655 absent upstream.
2. Cardmarket prices from the public daily files (one source for all games;
   mapping via TCGdex `idProduct` for Pokémon, code-in-name for One Piece,
   name + expansion for Lorcana/Riftbound) — spec not written.
3. Foil-only finishes, persisted scan language, per-game language availability,
   rarity label prettifying.
4. Packager: Pokémon palette PNGs → webp with alpha (grey corners today; cosmetic).
5. Distribution (GitHub Releases: core + per-game image packs) — design decided,
   nothing built.

## TCGScanner Mobile (`E:\TCGScannerMobile`)

**Milestone 1 done on 2026-09-21** — 21 commits `ed1b50b..08307eb` on `master`,
pushed to GitHub, 130 Jest tests, `tsc` clean. Built by subagent-driven
development from the Claude Design handoff (`docs/design_handoff/`); spec
`docs/2026-09-21-mobile-app-m1-design.md`, plan `docs/2026-09-21-mobile-app-m1-plan.md`,
execution ledger with every ruling `.superpowers/sdd/2026-09-21-mobile-app-m1-plan/progress.md`
(git-ignored, local only).

What M1 contains: Nocturne theme + primitives; five tabs with the raised Scan
button opening the Scanner modal; stub Home / Collections / Export; Settings
(model row, Developer → the four lab screens); expo-sqlite collection database
(counts per game/id/language and finish, scan journal, undo, versioned
migrations, serialised transactions) also tested on real SQLite via sql.js;
metadata singleton for the four games (pushed with the models); the desktop's
identical-artwork candidate rule in the matcher + a per-game mask; a pure scan
session reducer; the full Scanner overlay (live detection frame, match sheet,
variant sheet with ×1/×2, session tray, toast with undo, game pill as a
recognition filter). Images are placeholders everywhere by decision.

**Not done, owner's steps (nothing ran on the phone yet):**
1. Two one-line fixes surfaced by the last re-review, not applied:
   `src/navigation/TabBar.tsx` — `hitSlop={{ top: 24 }}` on the Scan `Pressable`
   (its top strip ignores taps on Android); `src/screens/scanner/VariantSheet.tsx`
   — `numberOfLines={1}` on the `Add {id} ×1` label.
2. Native rebuild (new modules): `npx expo prebuild --platform android` then
   `npx expo run:android`.
3. Push the current model + metadata: `powershell -File scripts\push-models.ps1`
   (the phone still has the 28 362-card index).
4. Device pass: spec §9 checklist, including double-tap Undo, Edit ID with a
   lowercase id (`ogn-…`), Scanner after Developer → Camera, top half of the Scan
   button, keyboard vs sheet actions, tray/toast vs the gesture bar. Record the
   results under a new "Milestone 1 scanner" heading in `docs/RESULTS.md`.

**Next milestones:** M2 = Home, Collections, Grid, Detail (the repository and
metadata APIs they need exist and are tested); M3 = Export (CSV share sheet),
Settings (default language, threshold), model download instead of `adb`.
Deferred polish list in the ledger ("minor (deferred)" lines), all triaged as
"can wait" by the final review except the two fixes above.

## How to resume

- Desktop: `wails dev` (Vite on port 3000), `go test ./...`; retrain / index
  work in WSL `Ubuntu-22.04` with `conda activate riftbound`, never with the
  dataset under the WSL home (the vhdx lives on C:); runbook `scripts/ml/RETRAIN.md`.
- Mobile: from `E:\TCGScannerMobile`, `npm install`, `npx expo run:android`,
  `npm test`; models are pushed by `scripts/push-models.ps1`, not bundled;
  Expo 57 APIs are checked against the versioned docs (`AGENTS.md`).
- Both repos keep specs and plans under `docs/`; the assistant's memory of
  decisions lives outside the repos (Claude Code project memory).
