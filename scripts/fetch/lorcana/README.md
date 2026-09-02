# Lorcana card fetcher

Downloads every Disney Lorcana card scan from the [Lorcast API](https://lorcast.com/docs/api)
(open, unauthenticated, Scryfall-style).

- Catalog: `GET https://api.lorcast.com/v0/sets` then `GET /v0/sets/<code>/cards`
- Images: `image_uris.digital.large` → 674×940 AVIF on `cards.lorcast.io` (no rate limit on the CDN)
- The CDN only serves AVIF and our training pipeline reads images with OpenCV
  (no AVIF support), so the fetcher decodes AVIF in pure Go
  ([gen2brain/avif](https://github.com/gen2brain/avif), wazero-based, no cgo)
  and writes PNG.

Files land in `images/` as `<set>-<collector_number>.png` (e.g. `1-207.png`,
`9-242.png`, `P1-4.png`) — unique, stable ids. Existing files are skipped:
the fetcher is resumable and incremental (re-run after each set release).

## Usage

```
go run .                 # every set (~3200 cards, ~2.4 GB of PNG)
go run . -sets 1,9,P1    # specific sets
go run . -list           # print all set codes
go run . -meta           # write cards.json (metadata) instead of downloading images
```

## Metadata

`go run . -meta` writes `cards.json`: a map of card id -> `{name, set,
set_name, number, rarity, type, colors}` (the app's common metadata format).
Ids match the image file names (`1-207`, `P1-4`), so the two modes stay in
sync automatically.

- `name` is `<name> - <version>` when the card has a version/subtitle (e.g.
  `"Ariel - On Human Legs"`); just `<name>` otherwise.
- `type` joins the API's `type` array with `" / "`.
- `colors` is the card's ink(s) (`inks` for multi-ink cards, else `[ink]`);
  `[]` if neither is present.
- Names are normalised: NBSP → space and curly `’` → straight `'`, so the
  corpus is uniform for later search/matching.

`-meta` always rewrites the whole catalog and fails (leaving the previous
`cards.json` untouched) if any set fetch fails, so don't combine it with
`-sets` for the committed file — a partial run would silently drop cards.

Runs in a few seconds (one request per set, same as image mode without the
downloads).

## Catalog as of 2026-08 (22 sets, 3192 cards)

Mainline: 1 The First Chapter (216), 2 Rise of the Floodborn (216),
3 Into the Inklands (226), 4 Ursula's Return (225), 5 Shimmering Skies (223),
6 Azurite Sea (222), 7 Archazia's Island (224), 8 Reign of Jafar (229),
9 Fabled (243), 10 Whispers in the Well (242), 11 Winterspell (242),
12 Wilds Unknown (242), 13 Attack of the Vine! (245).
Promos: P1 (41), P2 (39), P3 (56), D23 (9), cp (9), C2 (14), DIS (3),
Coconut (18), PD1 (8).

Note: the API also exposes per-card `prices` (TCGplayer) — useful later for
the price-estimation feature.
