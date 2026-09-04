# Riftbound card fetcher

Extracts the card catalog from the official gallery into `parsedcards.json`,
downloads the card images from the Riot CDN, and writes the app's card
metadata (`cards.json`) from it.

    go run . -extract     # rewrites parsedcards.json from the live gallery
    go run .              # downloads into images/ (skips existing files)
    go run . -meta        # writes cards.json from parsedcards.json, no download

`parsedcards.json` comes from the official card gallery
(https://playriftbound.com/en-us/card-gallery/): the gallery is a Next.js
page that inlines the FULL card catalog (Sanity CMS data, hence the
cmsassets.rgpub.io/sanity/.../game_data_live image URLs) in its
`__NEXT_DATA__` script, so one GET is the whole extraction:

    go run . -extract     # rewrites parsedcards.json from the live gallery
    go run .              # then the images of any new set (existing skipped)
    go run . -meta        # then cards.json

Each entry keeps {id, publicCode, imageUrl, name, setName, rarity, domains,
type}: `domains` is Riftbound's colour list, `setName` the gallery's set
label ("Proving Grounds" for OGS), `type` the card type labels joined with
" / ". The page is fetched with a browser-like User-Agent; if the site's
bot filter ever answers non-200, the fallback is the browser: open the
gallery, DevTools > Elements, copy the content of
`<script id="__NEXT_DATA__">` into a file and feed it to `extractGallery`
(the parser only needs that script; see gallery.go and gallery_test.go).

## Metadata (cards.json)

`go run . -meta` maps every parsedcards.json entry that carries a `name` to
the app's common metadata record, keyed by card id. Ids equal the image file
names without extension (`ogn-001-298`, `ogn-299-star-298` for the star
variant), so cards.json keys match the index labels.

Output fields per card: `name`, `set`, `set_name`, `number`, `rarity`,
`type`, `colors`.

- `set` is the upper-cased id prefix (`OGN`), `number` is the printed
  collector number from `publicCode` (the substring after the first `-`, cut
  at `/` when present) -- `001` for `OGN-001/298`, `299*` for the star
  variant `OGN-299*/298`, `T03`/`R01`/`SP1` for token and promo codes like
  `SFD-T03`, `VEN-R01`, `VEN-SP1/006`; falls back to the second id segment
  when `publicCode` is empty. `colors` is `domains` as-is (`[]` when absent),
  `rarity`/`type` are copied through.
- `name` is normalised like the other fetchers: NBSP -> space, curly
  apostrophe -> straight.
- `set_name` is the gallery's set label (`setName`: Origins, Proving Grounds
  for OGS, Spiritforged, Unleashed, Vendetta); the `setNames` map in main.go
  is only the fallback for entries that predate `-extract`.
- Entries WITHOUT a name are skipped (nothing fails). Two Unleashed tokens
  (Buff, XP Tracker) have no card type on the gallery: their `type` is "".

PRB01/PRB02 ids are NOT Riftbound: they are One Piece premium boosters that
were misfiled here once. Do not add them to parsedcards.json or `setNames`.

Current content: 1189 cards (ogn 352, ogs 24, sfd 288, unl 288, ven 237).
Images land in scripts/images/riftbound/ in the main pipeline (copy or point
--input there).
