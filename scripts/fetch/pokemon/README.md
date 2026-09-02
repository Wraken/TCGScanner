# Pokemon card image fetcher (TCGdex)

Uses the open TCGdex API (https://tcgdex.dev) -- full catalog, no auth, no
anti-scraping. Enumerates sets properly (no guessing set codes):

    GET /v2/<lang>/sets           -> set list
    GET /v2/<lang>/sets/<id>      -> cards[] with image base URLs
    GET /v2/<lang>/cards/<id>     -> full card detail (rarity, category, types)
    <image>/high.png              -> ~600x825 scan

    go run .                      # every English set (~24k cards, ~1h)
    go run . -sets base1,sv03     # chosen sets
    go run . -lang fr             # French scans
    go run . -list                # print all set ids
    go run . -meta                # write cards.json (metadata) instead of downloading images

Files: images/<card.id>.png (base1-1.png, sv03-125.png -- globally unique
stable ids). Resumable (existing skipped), 80ms/req. Copy into
scripts/images/pokemon/ for the training pipeline.

## Metadata

`go run . -meta` writes `cards.json`: a map of card id -> `{name, set,
set_name, number, rarity, type, colors}` (the app's common metadata format).
Ids match the image file names (`sv01-020`, `base1-1`), so the two modes
stay in sync automatically.

Unlike image mode, this is one request per card, not per set: the set
listing only carries `{id, image, localId, name}`, so rarity/category/types
need a `GET /v2/<lang>/cards/<id>` per card. That's ~21.8k requests for the
whole English catalog -- roughly 30 minutes at the fetcher's 80ms pacing.

The run is resumable and incremental: card ids already present in
`cards.json` are skipped, and progress is flushed to disk (atomically, via
tmp file + rename) every 200 new cards, so an interrupted run loses at most
a partial batch. On completion it prints how many requests failed and exits
non-zero if any did -- the already-fetched cards are still saved, so just
re-run the same command to fill in the gaps.

- `type` is the API's `category` (`"Pokemon"`, `"Trainer"`, or `"Energy"`).
- `colors` is the API's `types` array (elemental types, e.g. `["Grass"]`);
  `[]` for Trainer/Energy cards, which have none.
- Names are normalised: NBSP → space and curly `’` → straight `'`, so the
  corpus is uniform for later search/matching.

First run on this machine: TCGdex was unreachable on 2026-09-02 when this
was written; `cards.json` is produced by the packaging step (see the
metadata plan, Task 8).
