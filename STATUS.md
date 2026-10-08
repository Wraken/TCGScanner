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

Mapping coverage on 2026-10-07: Riftbound 1,187/1,189; Lorcana 3,153/3,192;
Pokémon 19,557/21,065 physical cards (2,480 TCG Pocket ids excluded);
One Piece 2,742/4,843 plus 2,064 parallel printings waiting on the visual step.

**Owner's steps on the training PC (none of this has run there yet):**

1. `git pull`.
2. Visual tie-break for the One Piece printings. The mapper's `cache/` is
   git-ignored and the CardTrader token lives on the laptop only, so copy
   `scripts/fetch/cardmarket/cache/` (32 MB, `previews/` not needed) and
   `report.txt` from the laptop, then in WSL (`conda activate riftbound`):
   ```
   cd scripts/fetch/cardmarket
   python visual_tiebreak.py --report report.txt --game onepiece --model ../../ml/models/tcg --references ../../ml/images/onepiece --out visual.json
   go run . -games onepiece -offline -report report.txt
   ```
   Commit `visual.json` and the rebuilt `cardmarket.json`. The script downloads
   the 5,954 CardTrader previews itself (public URLs, ~10 min).
3. Repackage the bundle with the usual `package_app_model.py` command: it now
   copies `cardmarket.json` into `metadata/`, prints the priced share per game
   and stops if a game would lose coverage (`--allow-price-regression` to
   force).
4. Launch the app: Home should show "Cardmarket prices from <date>" after the
   first background download (about 19 MB).

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
