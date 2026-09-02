# Riftbound card fetcher

Downloads card images from the official Riot CDN, driven by `parsedcards.json`
(id, publicCode, imageUrl -- plus name, rarity, domains, type once the
extraction below is redone), and writes the app's card metadata from it.

    go run .              # downloads into images/ (skips existing files)
    go run . -meta        # writes cards.json from parsedcards.json, no download

`parsedcards.json` was parsed out of the official card gallery
(https://playriftbound.com/fr-fr/card-gallery/): the gallery is a client-side
app that inlines the FULL card catalog in one of its JS bundles (Sanity CMS
data, hence the cmsassets.rgpub.io/sanity/.../game_data_live image URLs).
To refresh / add a NEW SET: open the gallery, DevTools > Sources (or Network,
JS filter), search all files for `publicCode` or `imageUrl` (Ctrl+Shift+F);
the matching chunk holds the card array -- extract it, map every card to
{id, publicCode, imageUrl, name, rarity, domains, type} -- keep `domains` as
an array, it is Riftbound's colour list -- and replace/append in
parsedcards.json. main.go re-runs incrementally (existing files are skipped).
Note: the site anti-bot walls plain curl; use a real browser for this step.

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
- `set_name` comes from the `setNames` map in main.go; extend it when a new
  set ships (an unknown prefix gets an empty set_name, nothing fails).
  Today: ogn/ogs = Origins, sfd = Spiritforged, unl = Unleashed,
  ven = Vendetta.
- Entries WITHOUT a name are skipped. The current parsedcards.json was
  extracted with id/publicCode/imageUrl only, so cards.json is `{}` until the
  gallery extraction is redone with the metadata fields above.

PRB01/PRB02 ids are NOT Riftbound: they are One Piece premium boosters that
were misfiled here once. Do not add them to parsedcards.json or `setNames`.

Current content: 1189 cards (ogn 352, ogs 24, sfd 288, unl 288, ven 237).
Images land in scripts/images/riftbound/ in the main pipeline (copy or point
--input there).
