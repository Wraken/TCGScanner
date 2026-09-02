# One Piece card metadata fetcher (punk-records)

Reads the English dataset of [buhbbl/punk-records](https://github.com/buhbbl/punk-records)
(generated with vegapull from the official card list, refreshed by its
maintainer after every new set) and writes `cards.json` in the app's common
metadata format (`name, set, set_name, number, rarity, type, colors`).

    go run .            # download packs.json + cards_by_id.json into cache/, write cards.json
    go run . -images    # same, then download every card image into images/ (resumable)
    go run . -offline   # rebuild cards.json from cache/ without downloading

Re-run `go run .` whenever punk-records picks up a new set -- both source
files and `cards.json` are rebuilt from scratch (deterministic, diff-friendly:
`encoding/json` sorts map keys).

## Id suffixes

Card ids in the dataset already carry the variant suffixes used by our index
labels (`OP01-006_p3`, `EB01-006_r1`, `PRB02-001_p1`, ...). `cards.json` is
keyed by these exact ids. A small number of *pure* variant-suffixed labels in
the app's image manifest (parallel art, alt rarities without their own base
card entry) are not separate rows in punk-records -- the packager fills those
in from their base card.

## Images

`-images` downloads the **official card images** (`en.onepiece-cardgame.com`),
which are SAMPLE-watermarked -- that's the app's display policy for this game.
De-watermarked copies used for training live in
`scripts/embeddingtest/images/onepiece/`, sourced separately.

## Do not use

[`Coko7/vegapull-records`](https://github.com/Coko7/vegapull-records) is
stale (stuck at OP-10) -- punk-records is the maintained dataset and the only
one this fetcher reads.
