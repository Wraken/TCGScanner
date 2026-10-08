# Cardmarket mapper

Builds `cardmarket.json`: card id → Cardmarket product id, for every game,
so the app can price a collection from Cardmarket's public daily price
guides without any account. The file is committed here and shipped in the
model bundle by `package_app_model.py` as `metadata/cardmarket.json`.

Design: `docs/superpowers/specs/2026-10-07-cardmarket-prices-design.md`.

## Why a table

Cardmarket's public files name products, they do not carry our card ids,
and name matching cannot tell the printings of one card apart (up to 20
One Piece products share a code, from 2 € to 11 000 €). So the mapping is
resolved once, by us, from sources that publish the exact Cardmarket id per
printing, checked against the day's catalog, and shipped. A card without a
sure product has no entry: the app shows no price rather than a wrong one.

## Sources

| Game | Step 1: published id | Step 2: CardTrader | Step 3: visual |
|---|---|---|---|
| Pokémon | TCGdex `pricing.cardmarket.idProduct`, one request per card, cached in `tcgdex_ids.json` | the rest | — |
| Lorcana | LorcanaJSON `externalLinks.cardmarketId` | the five Reign of Jafar Panorama prints, by hand (see step 4) | — |
| One Piece | — | plain cards | the `_pN` / `_rN` printings |
| Riftbound | — | everything (the number names the printing) | — |

CardTrader needs a free account: put the bearer token in `CARDTRADER_TOKEN`.
Without it the step is skipped and One Piece / Riftbound stay unresolved.
Game ids come from `-list-games` (Pokémon 5, One Piece 15, Lorcana 18,
Riftbound 22; `cardTraderGames` in `sources.go`).

**Step 2, how a blueprint becomes a product.** A CardTrader blueprint is
one printing: collector number as printed, a version label for the
non-plain printings, the Cardmarket ids of that printing. Our plain id
(`OP01-025`, no `_` suffix) takes the one blueprint of the set's home
expansion with exactly that number and no variant word in its version
(`resolve.go`: `variantWords`; CardTrader also writes the expansion code
or the rarity there, which do not count). Riftbound numbers already name
the printing (`007a`, showcases past the set size, `299*` signed), so the
exact number is enough there.

A One Piece blueprint lists one Cardmarket id per *shelf*: Cardmarket
sells every set twice, "Romance Dawn" and "Romance Dawn (Non-English)",
same metacard, prices apart by ×1.6 median. The English id is the one
whose shelf is English, read off the sealed-product names of
`products_nonsingles` (`loadShelfLanguages`). Dates do not tell the twins
apart (half were added Japanese-first). The second shelf is labelled
"(Non-English)" up to OP-13 and "(Asia Region Legal)" from OP-14 on (OP-06
and OP-13 carry both labels on one shelf), so both read as *foreign*; the
pre-release / tournament / anniversary packs are *event* shelves, never
English and never a twin. A shelf with no sealed product takes the
opposite language of the shelf sharing at least half of its single names
both ways: that is how the two promo shelves are told apart (the
non-English one sells a single "Exchange Meeting" theme pack, a Japanese
event; Cardmarket's site sits behind a bot check, so the names were not
read there).

**Non-English twins.** Each mapped One Piece card also ships, under
`cards_non_english`, the id of the same printing on the foreign shelf:
the other id of its blueprint that sits on a foreign shelf and carries
the very same product name (`nonEnglishTwins`). The app reads it for
copies whose language is not English. A printing CardTrader lists on the
foreign shelf only (8 promos) ships there alone: a Japanese copy is
priced, an English one is not, rather than wrongly. Measured 2026-10-07:
2,177 twins for 2,742 cards; the cards without one are the anniversary
sets and most promos, which Cardmarket sells on one shelf.

**Rule C, the pack names the expansion (One Piece variants).** Our
`_rN` reprints and `_pN` parallels carry, in `../onepiece/cards.json`, the
product they come from (`pack`: "PRB-02", "ST-34", "OP-01", the combined
"OP14-EB04", "PROMO"; punk-records' pack of the entry). CardTrader files
its blueprints by expansion and the expansion's name starts with the same
label (`expansionLabel`; the code is off for some decks, `st04` is
"ST-05"). So a variant's candidates are the blueprints of its pack's
expansion on its base number: the reprint is the one without a variant
marking (no letter after the number, no version word beyond "Reprint" /
"Non-Foil" / errata, `isVariantPrinting`), the parallels are the marked
ones ("Special Rare" counts as one). When the pack lists exactly one print
of the kind and we have exactly one variant of the kind in that pack, they
are the same printing (source `pack`); otherwise the prints of the kind
are the candidates listed for the review tool, and a variant without a
pack keeps every print of its number. A manual pick the rule disagrees
with is listed in the report (`disagree` lines) and still ships. Measured
2026-10-08 on the 2,064 ambiguous cards: 1,137 settled, the rest narrowed
to 4.8 candidates on average (6.7 before); 330 of the 408 reprints have
the very same artwork as their base card, which no eye could tell apart.

**Step 3, `visual_tiebreak.py`.** Our One Piece printings are numbered
(`_p1`, `_p2`); CardTrader's are lettered (`a`, `p`, `m`) and labelled,
and nothing maps one to the other. These printings differ by artwork,
which is what the embedding model separates: the script embeds our
reference image and every candidate blueprint's preview with the model
behind the index and keeps the closest candidate when it leads the next
by the app's own lock margin. It runs on the training PC (WSL, conda
`riftbound`, see `scripts/ml/RETRAIN.md`):

```
python visual_tiebreak.py --report report.txt --game onepiece     --model ../../ml/models/tcg --references ../../ml/images/onepiece --out visual.json
```

`visual.json` is committed (product, score, lead per card, and the
thresholds used); the mapper re-checks every pick against those
thresholds and only applies it to a card that is still ambiguous today.
A pick names a printing, not a shelf (the shelves of one printing share
one artwork, so they tie), and the mapper chooses the English shelf of the
picked blueprint itself (`visualShelf`).
Measured 2026-10-07: 2,064 cards, 5,954 previews to embed.

Step 4 is `overrides.json`: hand-checked picks, with the reason next to
each (`"_<id>"` keys are comments). They bypass the name check only.
Lorcana's `8-1f`… ids are the Reign of Jafar *Panorama* prints: a
foil-only second print of five cards with its own artwork and its own
product everywhere ("V2" on Cardmarket, `001p` "Panorama" on CardTrader,
not in LorcanaJSON). They are not the base card's foil column (Louie V1
trends 0.04 € / 3.61 € foil, V2 0.02 € / 1.91 €), so they are mapped by
hand from the CardTrader blueprint. Later sets have Panoramas too
(Winterspell, Wilds Unknown, Attack of the Vine!), but Lorcast does not
list them, so our fetcher has no id for them.

## Review tool: `manual.json`, every printing checked by eye

`review/review.py` serves a local page to settle the printings by hand,
one card at a time: our reference image next to one tile per candidate
printing (a printing's English and non-English products share one image,
so the tile is the printing and the mapper chooses the shelf, as for a
visual pick). Keys 1-9 and a-m pick a tile, 0 is "none of the candidates",
Backspace goes back, S skips. A second mode lists the game's plain cards
the mapper resolved on its own, our image next to the product's preview,
to flag the ones that do not match and choose the right printing. Every
verdict is written to `manual.json` at once, so a pass can stop and resume.

```
cd review
python review.py --game onepiece      # needs ../report.txt and the cache/ of a run with the token
python -m unittest                    # its tests
```

`manual.json` is `{"picks": {"<card id>": <product id>}, "none": [...]}`
and is committed. The mapper applies a pick before the automatic blueprint
choice and before `visual.json` (only `overrides.json` outranks it), checks
the product against the day's catalog like any other, and counts it as
`manual` in the summary; a "none" verdict keeps the card unresolved
instead of listing it as ambiguous again. Decision of 2026-10-08: the One
Piece printings are settled by hand, not by the visual tie-break.

## Usage

```
go run .                       # all games (Pokémon first run: ~30 min of TCGdex requests)
go run . -games lorcana        # one game; the others keep their entries
go run . -offline              # rebuild from cache/ without the network
go run . -retry-missing        # ask TCGdex again for ids it had no product for
go run . -list-games           # CardTrader's game ids, to fill cardTraderGames
go run . -report report.txt    # the full per-card lists
```

Each run replaces the game's entries wholesale (a card that lost its product
must not keep yesterday's) and prints, per game: mapped by source, dropped
by validation, ambiguous (several candidate products: see the report, then
`overrides.json`), unresolved, and skipped Pokémon TCG Pocket ids (mobile
only, never priced).

## What the catalog check catches

Measured on the first Pokémon run (2026-10-07): TCGdex's `idProduct` is
wrong for whole expansions, not just odd cards. For Team Magma vs Team Aqua
(`ex4`) it returns the ids of Sandstorm (Cardmarket expansion 1540 instead
of 1542), so "Team Aqua's Cacturne" would have been priced as "Armaldo"; the
trainer kits (`tk-*`), `pl1`-`pl3` and `2021swsh` are shifted the same way.
324 cards were refused that way on the first pass, all listed in the
report. They are left for CardTrader, never patched by hand from a guess.

A second shape the name check alone misses: one TCGdex id on two cards
whose names contain each other ("Mew" / "Mewtwo", "Paras" / "Parasect",
"Pikachu" / "Pikachu Star"). The builder refuses any product two
differently-named cards claim (43 products on the first run); a product two
same-named cards claim (Jungle's holo and plain prints) is one product with
two price columns and is kept.

## CardTrader for Pokémon and Lorcana: measured, not used

CardTrader's Pokémon and Lorcana blueprints are downloaded too, but the
builder does not resolve those games from them yet. Measured 2026-10-07 on
the 484 Pokémon cards both TCGdex and CardTrader place: they disagree on
23 (5 %), and the disagreements are between same-named cards
("Patrat [Collect | Slam]" vs "Patrat [Reckless Charge]", two Oddish
levels), which the name check cannot see. CardTrader would add about 210
of the 1,216 unresolved Pokémon cards; without a tie-break that is better
than a coin toss on those 5 %, that gain is not worth the wrong prices it
would carry. The visual tie-break could serve here too (the disagreeing
cards differ by artwork or level), which is the next step for Pokémon.

Their expansion codes differ from ours (`g1` for `gym1`, `ch1` for Lorcana
set `1`); the join would be by `set_name`, which matches 156 of our 214
Pokémon sets and 13 of 22 Lorcana sets by exact name.

## Validation

Every product must exist in that day's `products_singles_<game>.json` and
its name must agree with the card's after normalisation (`names.go`: case,
accents, dash and quote variants, Cardmarket's `[…]`/`(…)` suffixes). A
failed check drops the entry and lists it; it never ships.

## Files

- `cardmarket.json` — the output, committed
- `tcgdex_ids.json` — TCGdex lookups, committed (0 = TCGdex has no product)
- `visual.json` — the tie-break's picks, committed
- `manual.json` — the review tool's hand picks, committed
- `review/` — the review tool (`review.py`, `index.html`, tests)
- `overrides.json` — hand picks, committed
- `cache/` — downloaded upstreams and previews, gitignored; `-offline` reads from it
- `report*.txt` — gitignored
