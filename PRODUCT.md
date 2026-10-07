# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

The desktop app is a Wails binary (Go backend, React/TypeScript UI in the system webview: WebView2 on Windows, WebKitGTK on Linux). The Android app lives in a separate repository (`TCGScannerMobile`) and is out of scope for this record.

## Users

A solo collector digitising their own physical collection at home, card after card, with a webcam set up next to the keyboard. They want each card named correctly with as little handling as possible, then filed and exported (riftbound.gg, spreadsheets).

## Product Purpose

TCGScanner names a physical trading card held up to a webcam, down to the exact printing, and keeps a collection of the copies the user owns (finish, printed language, optional custom collection), exportable to CSV. Success: a collection that matches the cards in the binder, built without typing ids.

## Positioning

Everything runs on the user's machine: no account, no server of ours, no upload. Identification uses a local vision model (YOLO detector, embedding model, nearest-neighbour search over about 31,000 printings across Riftbound, One Piece, Lorcana and Pokémon). Free and open source (MIT).

## Operating Context

- Scanning session: the user holds or lays cards in front of the webcam, the app locks on a confident match, the user confirms finish and language, saves, next card.
- When several printings share the same artwork, the app asks which one is in hand instead of guessing.
- Collections are browsed per game, plus custom collections the user creates; copies are edited per row (finish, language, collection, delete).
- Export writes a CSV through the OS save dialog; presets exist (Default, riftbound.gg, Simple).
- Windows and Linux; the window opens maximised but can be resized freely.

## Capabilities and Constraints

- Card artwork, metadata and the model come from a local bundle under `./models/<model>/` (about 2 GB, not committed); a downloadable bundle is planned, not built.
- Supported games: Riftbound, One Piece, Lorcana, Pokémon. Card ids follow the bundle file stems (`ogn-001-298`, `OP01-001`, `1-1`, `sv01-020`).
- Planned, undecided: a link with the mobile app through a user-owned cloud folder (for example a Drive folder), keeping the no-account, no-server-of-ours promise.
- Planned: Cardmarket prices from public daily files (spec not written). No price or value is shown today and none may be invented.
- UI language: English today; French is planned, so new strings should be easy to extract (no text baked into images, no concatenated sentences).

## Brand Commitments

- Name: TCGScanner.
- The repository is public: no trademarked game logos; the game tiles use in-house artwork (`frontend/src/assets/games/*.svg`).

## Evidence on Hand

- README claims: local-only processing, about 5 scans per second, the four supported games, CSV presets.
- `STATUS.md`: 30,999 printings in the current index; offline lock-rate figures per game (see `scripts/ml/RETRAIN.md`).
- No testimonials, user counts, press or benchmarks against other products exist; do not fabricate any.

## Product Principles

1. Local first: nothing leaves the machine unless the user points it somewhere they own. One named exception: the daily download of Cardmarket's public price guides (a plain GET of four files, no account, no data sent, can be turned off on Home).
2. Never fail silently: a wrong or failed action is always visible, and an uncertain identification asks instead of guessing.
3. The card in hand is the source of truth: the user can always correct what the model says.
4. Open and honest: only claim what the code and data support.

## Accessibility & Inclusion

Target WCAG 2.2 AA: text contrast, visible keyboard focus, labelled controls, announced status changes, and a reduced-motion alternative for continuous animation.
