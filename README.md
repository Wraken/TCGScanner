# TCGScanner

An open-source desktop app to scan physical trading cards with a webcam, identify them with a local vision model, and keep a collection you can export to CSV (for instance for [riftbound.gg](https://riftbound.gg)).

Built with [Wails](https://wails.io/) (Go + React/TypeScript) into a single native binary. Everything runs on your machine: no account, no server, no upload.

Supported games today: **Riftbound**, **One Piece**, **Lorcana**, **Pokémon**.

## Features

- **Live scanning** — a YOLO detector finds the card in the webcam frame, an embedding model turns the crop into a vector, and a nearest-neighbour search over the reference index names the card (≈5 scans/s).
- **Confident lock** — the UI locks when the best match clears a score floor *and* leads the best different card by a margin, so look-alikes never lock silently.
- **Variant choice** — when several printings share the same artwork (reprints, alt-art foils), the app shows them side by side and lets you pick the one in your hand.
- **Card details** — name, set, number, rarity, type and colours from the bundled metadata.
- **Collections** — one collection per game, plus custom collections you create; per copy: finish (normal / foil) and printed language.
- **CSV export** — presets (default, riftbound.gg, simple) or your own field selection, optionally split per language.
- **Multi-camera** — pick any connected webcam.

## Requirements

- [Go 1.26+](https://go.dev/)
- [Node.js 18+](https://nodejs.org/)
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation) — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- A model bundle under `./models/tcg/` (see [Model bundle](#model-bundle))

On Windows, ONNX Runtime is embedded in the binary. On Linux, install `libonnxruntime.so` (or point `ORT_LIB_PATH` at it).

## Build & run

```bash
cd frontend && npm install && cd ..

wails dev      # live development, hot reload
wails build    # production binary in build/bin/
```

`go test ./...` runs the backend tests; the model tests skip when no bundle is installed.

## Model bundle

The app loads everything from `./models/tcg/` (not committed, ~2 GB with the images of all four games):

| File | Role |
|---|---|
| `yolo.onnx` | card detector |
| `embedding_model.onnx` | 512-d embedding of the detected card (EfficientNetV2-S, ArcFace) |
| `index.bin`, `index_labels.json` | one reference vector per card printing, and its id |
| `search.onnx` | the same vectors as one MatMul node, so the search runs in ONNX Runtime |
| `config.json` | input size, score floor, lock margin, twin threshold |
| `images/<game>/<id>.webp`, `images/manifest.json` | card artwork shown in the app (One Piece ships the official SAMPLE-watermarked scans) |
| `images/<game>/_cover.webp` | optional artwork for the game tile |
| `metadata/<game>.json` | name, set, number, rarity, type, colours per card |

A downloadable bundle is planned (GitHub Releases of the data repository, core + per-game image packs). Until then, build it with the pipeline below.

Card ids are the file stems used everywhere: `ogn-001-298` (Riftbound), `OP01-001` / `OP01-001_p1` (One Piece and its parallel arts), `1-001` (Lorcana), `sv01-020` (Pokémon, TCGdex ids).

## Data pipeline (`scripts/`)

```
scripts/
  fetch/<game>/         catalogue + images per game (Go), see each README
  unwatermark/          One Piece: removes the SAMPLE watermark for the reference images
  embeddingtest/        the vision pipeline: training, index, packaging (Python)
```

### 1. Fetch catalogues and images

Each game has a fetcher in `scripts/fetch/<game>/` with its own README:

- **Pokémon** — TCGdex (`go run .` images, `go run . -meta` metadata)
- **Lorcana** — Lorcast (`go run .`, `go run . -meta`)
- **One Piece** — punk-records catalogue + official images (`go run .`, `go run . -images`, `-meta`)
- **Riftbound** — the official gallery (`go run . -extract`, `go run .`, `-meta`)

Images land in `scripts/fetch/<game>/images/` (gitignored), metadata in `scripts/fetch/<game>/cards.json` (committed).

### 2. Reference images for the index

The index references live in `scripts/embeddingtest/images/<game>/`, one PNG per card id. Riftbound, Lorcana and Pokémon images are used as fetched. One Piece scans carry a SAMPLE watermark that a physical card does not have, so they are cleaned first:

```bash
cd scripts/unwatermark
pip install opencv-python-headless numpy tqdm
python unwater_op_v3.py preview --input ../fetch/onepiece/images/OP12-020.png --params wm_en.npz
python unwater_op_v3.py remove  --input ../fetch/onepiece/images --output ../embeddingtest/images/onepiece --params wm_en.npz --ids new_ids.json
```

`wm_en.npz` is the watermark's opacity map estimated on the official English scans; translucent watermarks are inverted exactly, the opaque ones of the oldest sets are located by template matching and inpainted. The app itself displays the original watermarked scans.

### 3. Build or extend the index

```bash
cd scripts/embeddingtest
pip install faiss-cpu numpy tensorflow tqdm      # a GPU (WSL2 works) makes embedding ~20x faster
python add_to_index.py --name tcg --references images/onepiece   # embeds every image not yet in the index
python build_index.py  --name tcg                                # rebuilds from scratch instead
```

`add_to_index.py` never replaces an existing label: drop it from the index first if its reference image changed.

Retraining the embedding model itself is optional (`augment.py` then `train.py`): new cards only need their reference embedded.

### 4. Package for the app

```bash
cd scripts/embeddingtest
python package_app_model.py            # images + metadata + index -> ../../models/tcg
python package_app_model.py --skip-images
```

The packager converts the reference images to webp, writes the manifest and the metadata, and reports coverage: index labels without an image, images without an index reference, cards without metadata.

## CSV export

Fields are chosen in the export dialog; the default preset is:

| Normal Count | Foil Count | Card ID |
|---|---|---|
| 1 | 0 | ogn-001-298 |

Add the **Language** field to get one row per card and language.

## License

MIT
