# Pokemon card image fetcher (TCGdex)

Uses the open TCGdex API (https://tcgdex.dev) -- full catalog, no auth, no
anti-scraping. Enumerates sets properly (no guessing set codes):

    GET /v2/<lang>/sets           -> set list
    GET /v2/<lang>/sets/<id>      -> cards[] with image base URLs
    <image>/high.png              -> ~600x825 scan

    go run .                      # every English set (~24k cards, ~1h)
    go run . -sets base1,sv03     # chosen sets
    go run . -lang fr             # French scans
    go run . -list                # print all set ids

Files: images/<card.id>.png (base1-1.png, sv03-125.png -- globally unique
stable ids). Resumable (existing skipped), 80ms/req. Copy into
scripts/images/pokemon/ for the training pipeline.
