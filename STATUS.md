# Project status — 2026-10-08

Two repositories, one product: the desktop scanner (this repo) and the Android
app (`E:\TCGScannerMobile`, https://github.com/Wraken/TCGScannerMobile, private).
This file is the "where were we" note; the detailed docs are linked from each
section. Previous note: 2026-09-21 (in git history).

## TCGScanner (desktop, this repo)

**Branch `main` = `origin/main` at `59e0e92`, pushed 2026-10-07. Working tree
clean**, apart from four untracked `brag-output*/` folders (see Housekeeping).

**Shipped since 2026-09-21** (three commits, all pushed):

| Commit | What |
|---|---|
| `ed29496` | Nocturne redesign after the impeccable audit (11/20 before): a notice for every backend error, in-app dialogs instead of `window.prompt/confirm`, scan screen accessibility, two-step copy delete, dead UI kit and deps dropped; `PRODUCT.md`, `DESIGN.md`, `.impeccable/design.json`; window minimum 1024×640. |
| `83e015e` | Cardmarket mapper `scripts/fetch/cardmarket/` → `cardmarket.json` (26,639 cards + 2,185 One Piece non-English twins). Spec `docs/superpowers/specs/2026-10-07-cardmarket-prices-design.md`; the mapper's `README.md` explains the sources, the One Piece shelves and the visual tie-break. |
| `59e0e92` | Prices in the app: `prices.go` fetches Cardmarket's daily guides into `./prices/` (once a day, ETag, atomic replace, `settings.prices_enabled`, event `prices:updated`); Home value stat + per-game values + status line (Refresh / Turn off); grid price per card + view total; copies dialog trend/foil/other-languages lines. `package_app_model.py` ships `cardmarket.json` with a coverage gate. Verified live with `wails dev` on 2026-10-07. |

Mapping coverage on 2026-10-07 (unchanged 2026-10-08): Riftbound 1,187/1,189; Lorcana 3,153/3,192;
Pokémon 19,557/21,065 physical cards (2,480 TCG Pocket ids excluded);
One Piece 2,742/4,843 plus 2,064 parallel printings waiting on the visual step.

**Desktop PC, 2026-10-08:** pulled; the mapper ran here for all four
games with the CardTrader token (same numbers as the laptop), so `cache/`
and `report.txt` now exist on this PC too. Fixed on the way: a single-game
build (`-games pokemon,lorcana`) dropped 59 Riftbound entries, `gameOfID`
classified ids by shape and the signed `ogn-299-star-298` / token `sfd-t03`
ids fell into "Pokémon = everything else"; it now classifies by the
fetchers' `cards.json` lists. **Decision: the One Piece printings are
settled by hand, not by the visual tie-break** (owner wants zero errors).
Built for it: `scripts/fetch/cardmarket/review/` (`review.py`, stdlib, a
local page: choice mode for the 2,064 ambiguous printings, check mode for
the 2,742 plain cards; every verdict written at once to `manual.json`) and
a `manual` source in the mapper (before blueprint and visual, after
overrides; "none" verdicts stay unresolved). Verified live in the browser
and through an offline mapper run; Go and Python tests green.

**Rule C, the pack names the expansion (same day):** the manual pass hit a
wall, 330 of the 408 `_r` reprints have the very same artwork as their base
card, so no eye can tell them apart. punk-records' pack of each printing
(now `pack` in `scripts/fetch/onepiece/cards.json`, not copied by the
packager) names the CardTrader expansion; the mapper narrows a variant's
candidates to that expansion, reprint = the unmarked print, parallels =
the marked ones, and settles the print when both sides count exactly one
(`resolve.go` rule C, source `pack`; a manual pick it disagrees with is
listed as `disagree` in the report). Offline rebuild: One Piece 3,860/4,843
mapped (was 2,742), 1,060 by the rule, 861 ambiguous left at 5.4 candidates
on average. 10 of the owner's first 59 manual picks disagreed with the
rule, all PRB-02 reprints or foils picked as their EB-01 original
(identical art): the owner had all 59 cleared (backup in the assistant's
scratchpad only). Rebuilt with the rule alone: 3,841/4,843 mapped, 1,099 by
the rule, 880 left for the review tool.

**Price reference selector (shipped 2026-10-08):** spec
`docs/superpowers/specs/2026-10-08-price-reference-selector-design.md`. The
identical twins (same artwork, a treatment no image shows) are settled in
the app by the owner, card in hand. Mapper: `pixel_tiebreak.py` settles the
different-artwork printings into `visual.json` (491 settled, 271 tied, 45 twin,
73 low of 880; `pixel_status.json` orders the review tool's queue, "low"
first) and `cardmarket.json` ships `candidates` for the remaining 330 ids
(product, non-English twin, label, CardTrader scan URL; never both in `cards`
and `candidates`). App: `price_refs` table (one product per card id, outranks
the bundle), bindings `GetPriceCandidates` / `SetPriceRef` / `ClearPriceRef`
/ `GetPriceRefs` / `GetPriceCandidateImage` (scans fetched on demand into
`./prices/images/`); UI in three commits: the "no ref" chip on tiles and in
the copies dialog, the `PriceReferenceDialog` (our image vs the scans, with
prices, keys 1-9 then A-M pick), the twin's reference and price under each id in the
scan duplicate selector. Note: `scripts/fetch/cardmarket/manual.json`
(untracked) still holds the 149 hand picks made on 2026-10-08 after the
cleanup; the mapper applies them first (140 mapped, so the rebuild shows
`visual 395` rather than 491), and whether to keep them is the owner's call:
delete the file, rebuild offline and recommit to drop them. Re-runs of
`pixel_tiebreak.py` merge into `visual.json` (picks of cards no longer in the
report are kept), and a card settles only when every candidate has a scan;
checked 2026-10-08: none of the 491 picks sat on an incomplete field.

**Collection sync through Google Drive (code complete 2026-10-19, owner's
Google setup pending):** spec
`docs/superpowers/specs/2026-10-19-collection-sync-google-drive-design.md`.
Each device appends to one append-only journal, `journal-<device id>.jsonl`,
in a visible `TCGScanner` folder of the owner's Google Drive (`drive.file`
scope, no server). The journal records the events since the migration: stable
uids on collections and cards, and the bootstrap written once from the
existing rows. Merge: last writer wins per entity by `(at, device)`, removals
are final, price-reference clears are remembered. Sync cycle: at start, on
window focus, after changes (debounced 5 s), on Sync now, retried while
offline; one batch per cycle; the first connection asks the merge question.
Settings screen: Connect, status line, devices, Sync now, Disconnect. Tests:
Go unit tests against a fake Drive and a fake Google token server
(`sync_*_test.go`), frontend `tsc` + Vite; `-race` could not run on this PC.

**Owner's steps (desktop PC):**

1. Google Cloud console, once (spec "Google side"): create a project with the
   Drive API enabled; configure the OAuth consent screen as External, app name
   `TCGScanner`, scopes `drive.file`, `openid`, `email`, publishing status
   **In production**; create an OAuth client of type **Desktop app**. Paste
   its client id and client secret into `sync_google.go` (`googleClientID`,
   `googleClientSecret`) and rebuild.
2. Live pass (spec "Testing", "Live, by the owner"): run two copies of the
   built app from two folders, each with its own `collection.db`, signed in to
   the same account. Scan on one and see it on the other after a focus change;
   remove on one and see it gone on the other; on a third copy that already
   has cards, check the merge question.
3. The mobile app's M2 implements the same journal format: see the spec's "The
   journal format" section.
4. Still open from the previous list: the decision on
   `scripts/fetch/cardmarket/manual.json` (keep, or delete, then rebuild
   offline and recommit), the review tool's 73 "low" cards, and the
   price-reference live pass (repackage, then the One Piece "no ref" chip,
   copies dialog and letter keys).

**Backlog (by priority)**

1. Prices, leftovers. One Piece: 29 plain cards (21 promos, EB04-036,
   OP12-118/119, 5 starter reprints) → `overrides.json` by hand. Riftbound: 2
   tokens Cardmarket does not sell. Lorcana: 39 (C2/P2/cp promos, "Coconut",
   `P1-25ja`/`P1-25zh` left unpriced on purpose). Pokémon: 1,508 without a
   TCGdex id (Gym Heroes/Challenge, XY trainer kits, BW/DP/Wizards/Nintendo
   promos, Shiny Vault, Sandstorm's shifted ids); CardTrader has ids but
   disagrees with TCGdex on 5 % of same-named cards, so only behind a visual
   check, later. To confirm on Cardmarket's site (its bot check blocked the
   read): shelf 5230 = "Promotion Cards", 5511 = its non-English twin.
2. Settings screen: the prices on/off switch sits on Home's status line until
   it exists. With it: persisted scan language, foil-only finishes, per-game
   language availability, rarity label prettifying.
3. Animations: `/impeccable animate` plan written on 2026-10-07 (nine
   candidates), deferred by the owner. Recommended set: detection frame glides
   and its pulse stops once locked (1), lock ring on recognition (2), artwork
   reveal in the right panel (3), grid images fade in (6), status colour fade
   (8). Nothing implemented.
4. Pokémon images (unchanged): 53 listed + 61 recoverable from the CDN
   (fetcher fallback not written); ~1,655 absent upstream.
5. Packager: Pokémon palette PNGs → webp with alpha (cosmetic).
6. Distribution (GitHub Releases: core + per-game image packs): design decided,
   nothing built.
7. Lorcana Panorama prints of Winterspell / Wilds Unknown / Attack of the
   Vine! are absent from Lorcast, so from the fetcher and the index (Reign of
   Jafar's five are in, priced by hand).

**Housekeeping**

- `brag-output/`, `brag-output-2026-10-03-*/`: four /brag video runs, 650 MB,
  untracked. The `--voice` render was never approved. Keep one, git-ignore or
  delete: owner's call.
- The card-language live pass noted on 2026-09-21 (scan selector, Saved line,
  copies dialog, CSV Language field) was exercised during the redesign pass
  except the CSV field.

## TCGScanner Mobile (`E:\TCGScannerMobile`)

Unchanged since 2026-09-21 as far as this note knows: the E: drive is not on
the laptop, so nothing was checked. The 2026-09-21 state, kept here so it is
not lost:

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
   (the phone still has the 28 362-card index). The desktop bundle now also
   carries `metadata/cardmarket.json`; the mobile app does not read it yet.
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

- Desktop: `wails dev` (Vite on port 3000), `go test ./...`. The Wails CLI on
  the laptop is rebuilt with `golang.org/x/tools` v0.51 (`~/go/bin/wails.exe`)
  because Go 1.27 broke the stock one; after a change to the bound Go methods,
  `wails generate module` refreshes `frontend/wailsjs/`. Without a model
  bundle, a `models/dev/metadata/cardmarket.json` (copy of the mapper's file)
  is enough to see prices.
- Mapper: `cd scripts/fetch/cardmarket && go run . -offline -report report.txt`
  rebuilds from `cache/`; drop `-offline` to refetch (CardTrader needs
  `CARDTRADER_TOKEN` in the environment, never in the repo). `go test ./...`
  there too.
- Retrain / index work in WSL `Ubuntu-22.04` with `conda activate riftbound`,
  never with the dataset under the WSL home (the vhdx lives on C:); runbook
  `scripts/ml/RETRAIN.md`.
- Mobile: from `E:\TCGScannerMobile`, `npm install`, `npx expo run:android`,
  `npm test`; models are pushed by `scripts/push-models.ps1`, not bundled;
  Expo 57 APIs are checked against the versioned docs (`AGENTS.md`).
- Both repos keep specs and plans under `docs/`; the assistant's memory of
  decisions lives outside the repos (Claude Code project memory).
