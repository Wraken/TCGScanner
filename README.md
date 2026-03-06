# TCGScanner

An open-source desktop app to scan physical trading cards with a webcam, identify them using a machine learning model, and export your collection to CSV for import on sites like [riftbound.gg](https://riftbound.gg).

Built with [Wails](https://wails.io/) (Go + React/TypeScript) into a single native binary.

## Features

- **Live camera scanning** — point your webcam at a card and get a real-time prediction
- **ML-powered identification** — uses a TensorFlow Lite classification model (224×224 RGB)
- **Auto-lock** — automatically locks on a card when confidence reaches 99%
- **Foil / Normal toggle** — track card finish before saving
- **Collection management** — organize scans into collections, switch between them anytime
- **CSV export** — export any collection to CSV for import sites
- **Multi-model support** — drop any compatible model into `./models/` and select it at runtime
- **Multi-camera support** — choose between connected cameras

## Requirements

- [Go 1.21+](https://go.dev/)
- [Node.js 18+](https://nodejs.org/)
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation) — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- [TensorFlow headers](https://www.tensorflow.org/install/lang_c) — download and extract the TensorFlow C library to get the `tensorflow/` include directory
- A TFLite model + labels (see [Adding a model](#adding-a-model))

The TFLite shared library (`tensorflowlite_c.dll`) is included in the `tflite/` folder.

## Getting Started

### 1. Set up environment variables

Point CGO to the TensorFlow headers and the bundled TFLite library:

```bash
# Linux / macOS
export CGO_CFLAGS="-I/path/to/tensorflow"
export CGO_LDFLAGS="-L$(pwd)/tflite"
export LD_LIBRARY_PATH="$(pwd)/tflite:$LD_LIBRARY_PATH"

# Windows (PowerShell)
$env:CGO_CFLAGS = "-IC:\path\to\tensorflow"
$env:CGO_LDFLAGS = "-L$PWD\tflite"
$env:PATH += ";$PWD\tflite"
```

### 2. Build & run

```bash
# Install frontend dependencies
cd frontend && npm install && cd ..

# Live development (hot reload)
wails dev

# Build a production binary
wails build
```

## Project Structure

```
TCGScanner/
├── main.go          # App entry point
├── app.go           # Go backend API (Wails bindings)
├── model.go         # TFLite inference & image preprocessing
├── db.go            # SQLite schema, collections, CSV export
├── models/
│   └── riftbound/   # Example model
│       ├── model.tflite
│       ├── labels.json
│       └── images/  # Card images named {card_id}.png
├── frontend/
│   └── src/
│       └── App.tsx  # React UI
└── collection.db    # Local SQLite database (auto-created)
```

## Adding a Model

1. Create a folder under `./models/<name>/`
2. Place your `model.tflite` and `labels.json` inside
3. Add card images to `./models/<name>/images/` named `{card_id}.png` (or `.jpg`/`.webp`)
4. Launch the app and select your model from the dropdown

`labels.json` format:
```json
{ "0": "ogn-001-298", "1": "ogn-002-298", ... }
```

## CSV Export Format

| Normal Count | Foil Count | Card ID |
|--------------|------------|---------|
| 1 | 0 | ogn-001-298 |

## License

MIT
