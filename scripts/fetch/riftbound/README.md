# Riftbound card fetcher

Downloads card images from the official Riot CDN, driven by `parsedcards.json`
(id, publicCode, imageUrl).

    go run main.go        # downloads into images/ (skips existing files)

`parsedcards.json` was parsed out of the official card gallery
(https://playriftbound.com/fr-fr/card-gallery/): the gallery is a client-side
app that inlines the FULL card catalog in one of its JS bundles (Sanity CMS
data, hence the cmsassets.rgpub.io/sanity/.../game_data_live image URLs).
To refresh / add a NEW SET: open the gallery, DevTools > Sources (or Network,
JS filter), search all files for `publicCode` or `imageUrl` (Ctrl+Shift+F);
the matching chunk holds the card array -- extract it, map to
[{id, publicCode, imageUrl}], replace/append in parsedcards.json.
main.go re-runs incrementally (existing files are skipped).
Note: the site anti-bot walls plain curl; use a real browser for this step.

Current content: 664 cards (ogn 352, ogs 24, sfd 288).
Images land in scripts/images/riftbound/ in the main pipeline (copy or point
--input there).
