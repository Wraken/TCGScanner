"""Review tool for the Cardmarket mapper: a local page to settle, by eye,
which CardTrader printing each of our card ids is.

    python review.py --game onepiece              # serves http://127.0.0.1:8765
    python review.py --game onepiece --report ../report.txt --port 8765

Choice mode: every card the mapper's report lists as ambiguous, one at a
time, our reference image next to one tile per candidate printing. A
printing's English and non-English products share one image, so the tile
is the printing; the mapper chooses the shelf (resolve.go, visualShelf).
Check mode: the game's plain cards the mapper resolved on its own, our
image next to the product's preview, to flag the ones that do not match;
a flagged card gets the choice screen with every printing of its number.

Every verdict is written to ../manual.json at once (the mapper's
loadManual reads it: {"picks": {id: product}, "none": [id, ...]}), so the
page resumes where it stopped. Card images are downloaded on demand into
../cache/images/: CardTrader's originals (470x656), not the 180px previews
visual_tiebreak.py embeds. Standard library only; runs from Windows or WSL.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import threading
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent
MAPPER = HERE.parent
CACHE = MAPPER / "cache"
IMAGES = CACHE / "images"
MANUAL = MAPPER / "manual.json"
CARDTRADER = "https://cardtrader.com"
MAPPING = MAPPER / "cardmarket.json"
UA = "TCGScanner cardmarket mapper (personal ML project)"

GAME_PREFIXES = {
    "onepiece": ("OP", "ST", "EB", "PRB", "P-"),
    "riftbound": ("ogn-", "sfd-", "unl-", "ven-"),
}
CARDTRADER_GAME = {"onepiece": 15, "riftbound": 22}
AMBIGUOUS = re.compile(r"^ambiguous\s+(\S+)\s+candidates \[([^\]]*)\]")
CLEAR = object()  # save_pick verdict: forget the card


# ── Pure parts (tested) ───────────────────────────────────────────────────


def read_ambiguous(text: str, prefixes: tuple[str, ...]) -> dict[str, list[int]]:
    """card id -> candidate Cardmarket product ids, in the report's order."""
    out: dict[str, list[int]] = {}
    for line in text.splitlines():
        m = AMBIGUOUS.match(line)
        if m and m.group(1).startswith(prefixes):
            out[m.group(1)] = [int(x) for x in m.group(2).split()]
    return out


def base_number(num: str) -> str:
    """The collector number without its variant letter: OP01-025a -> OP01-025
    (the mapper's variantBase)."""
    start = num.rfind("-") + 1
    end = start
    while end < len(num) and num[end].isdigit():
        end += 1
    return num if end == start else num[:end]


def image_url(b: dict) -> str | None:
    """The blueprint's full-size image: the original behind image.url
    (470x656 for a card) rather than image_url, which is the 180px preview."""
    url = ((b.get("image") or {}).get("url") or "").strip()
    if url:
        return url if url.startswith("http") else CARDTRADER + url
    return b.get("image_url") or None


def index_blueprints(exps: list[dict], blueprints_by_exp: dict[int, list[dict]]) -> dict:
    """by_cm: product -> blueprint; by_base: base number -> blueprints;
    exp_name: expansion id -> name; bp: blueprint id -> blueprint."""
    ix = {"by_cm": {}, "by_base": {}, "exp_name": {}, "bp": {}}
    for e in exps:
        ix["exp_name"][e["id"]] = e.get("name") or e.get("code") or ""
    for bps in blueprints_by_exp.values():
        for b in bps:
            ix["bp"][b["id"]] = b
            for cm in b.get("card_market_ids") or []:
                if cm and cm not in ix["by_cm"]:
                    ix["by_cm"][cm] = b
            num = ((b.get("fixed_properties") or {}).get("collector_number") or "").strip().upper()
            if num:
                ix["by_base"].setdefault(base_number(num), []).append(b)
    return ix


def _tile(b: dict | None, ix: dict, products: list[int]) -> dict:
    if b is None:
        return {"blueprint": None, "products": products, "version": "", "expansion": "", "number": "", "preview": None}
    return {
        "blueprint": b["id"],
        "products": products,
        "version": (b.get("version") or "").strip(),
        "expansion": ix["exp_name"].get(b.get("expansion_id"), ""),
        "number": ((b.get("fixed_properties") or {}).get("collector_number") or "").strip(),
        "preview": f"/preview/{b['id']}" if image_url(b) else None,
    }


def group_candidates(products: list[int], ix: dict) -> list[dict]:
    """One tile per printing (blueprint), carrying every candidate product of
    it; a product on no cached blueprint gets a tile of its own, no image."""
    tiles: dict = {}
    order: list = []
    for p in products:
        b = ix["by_cm"].get(p)
        key = b["id"] if b else ("none", p)
        if key not in tiles:
            tiles[key] = _tile(b, ix, [])
            order.append(key)
        tiles[key]["products"].append(p)
    return [tiles[k] for k in order]


def our_number(card: dict) -> str:
    """The collector number as CardTrader prints it (the mapper's ourNumber):
    One Piece "OP01-025", Riftbound "007A" / "299S"."""
    s, n = card.get("set", ""), card.get("number", "").upper()
    if s and s == s.upper():
        return f"{s.upper()}-{n}"
    return n.replace("*", "S")


def candidates_for_number(card_id: str, cards: dict, ix: dict) -> list[dict]:
    """Every printing CardTrader lists on the card's number, for a plain card
    flagged in check mode."""
    base = base_number(our_number(cards[card_id]))
    return [_tile(b, ix, [cm for cm in b.get("card_market_ids") or [] if cm]) for b in ix["by_base"].get(base, [])]


def choice_queue(ambiguous: dict[str, list[int]], manual: dict, status: dict[str, str] | None = None) -> list[str]:
    """The cards still to settle, "low" ones first (our image matches no
    candidate: worth a look) then the rest, each in the report's order."""
    done = set(manual.get("picks", {})) | set(manual.get("none", []))
    left = [i for i in ambiguous if i not in done]
    if not status:
        return left
    return [i for i in left if status.get(i) == "low"] + [i for i in left if status.get(i) != "low"]


def check_rows(mapping: dict[str, int], cards: dict, ix: dict, prefixes: tuple[str, ...]) -> list[dict]:
    """The game's plain cards with a product: our image next to its preview."""
    rows = []
    for card_id in sorted(mapping):
        if "_" in card_id or not card_id.startswith(prefixes) or card_id not in cards:
            continue
        product = mapping[card_id]
        b = ix["by_cm"].get(product)
        rows.append({
            "id": card_id,
            "name": cards[card_id].get("name", ""),
            "product": product,
            "blueprint": b["id"] if b else None,
            "preview": f"/preview/{b['id']}" if b and image_url(b) else None,
            "ref": f"/ref/{card_id}",
        })
    return rows


def _read_manual_raw(path: Path) -> dict:
    try:
        return json.loads(Path(path).read_text("utf-8"))
    except FileNotFoundError:
        return {}


def load_manual(path: Path) -> dict:
    data = _read_manual_raw(path)
    picks = {k: int(v) for k, v in (data.get("picks") or {}).items() if not k.startswith("_")}
    return {"picks": picks, "none": list(data.get("none") or [])}


_save_lock = threading.Lock()


def save_pick(path: Path, card_id: str, verdict) -> dict:
    """verdict: a product id, None for "no candidate matches", CLEAR to forget.
    Written whole, atomically, keeping any "_id" comment keys."""
    path = Path(path)
    with _save_lock:
        data = _read_manual_raw(path)
        picks = dict(data.get("picks") or {})
        none = [i for i in (data.get("none") or []) if i != card_id]
        picks.pop(card_id, None)
        if verdict is None:
            none.append(card_id)
        elif verdict is not CLEAR:
            picks[card_id] = int(verdict)
        data["picks"] = dict(sorted(picks.items()))
        data["none"] = sorted(none)
        tmp = path.with_name(path.name + ".tmp")
        tmp.write_text(json.dumps(data, indent=1, ensure_ascii=False) + "\n", "utf-8")
        os.replace(tmp, path)
    return load_manual(path)


# ── Data loading ──────────────────────────────────────────────────────────


def load_game_index(game: str) -> dict:
    exps = json.loads((CACHE / "cardtrader_expansions.json").read_text("utf-8"))
    mine = [e for e in exps if e.get("game_id") == CARDTRADER_GAME[game]]
    by_exp = {}
    for e in mine:
        f = CACHE / f"cardtrader_blueprints_{e['id']}.json"
        if f.exists():
            by_exp[e["id"]] = json.loads(f.read_text("utf-8"))
    if not by_exp:
        sys.exit(f"no CardTrader blueprints for {game} under {CACHE}: run the mapper with CARDTRADER_TOKEN first")
    return index_blueprints(mine, by_exp)


class App:
    def __init__(self, game: str, report: Path, references: Path):
        self.game = game
        self.prefixes = GAME_PREFIXES[game]
        self.references = references
        self.cards = json.loads((MAPPER.parent / game / "cards.json").read_text("utf-8"))
        self.ix = load_game_index(game)
        self.ambiguous = read_ambiguous(report.read_text("utf-8"), self.prefixes)
        status_path = MAPPER / "pixel_status.json"
        self.status = json.loads(status_path.read_text("utf-8")) if status_path.exists() else {}
        mapping = json.loads(MAPPING.read_text("utf-8")) if MAPPING.exists() else {"cards": {}}
        self.check = check_rows(mapping.get("cards", {}), self.cards, self.ix, self.prefixes)
        self.manual_path = MANUAL

    def state(self) -> dict:
        manual = load_manual(self.manual_path)
        queue = choice_queue(self.ambiguous, manual, self.status)
        return {
            "game": self.game,
            "total": len(self.ambiguous),
            "queue": queue,
            "settled": len(self.ambiguous) - len(queue),
            "manual": manual,
            "check_total": len(self.check),
        }

    def card(self, card_id: str) -> dict | None:
        c = self.cards.get(card_id)
        if c is None:
            return None
        if card_id in self.ambiguous:
            tiles = group_candidates(self.ambiguous[card_id], self.ix)
        else:
            tiles = candidates_for_number(card_id, self.cards, self.ix)
        return {
            "id": card_id,
            "name": c.get("name", ""),
            "set": c.get("set", ""),
            "set_name": c.get("set_name", ""),
            "number": c.get("number", ""),
            "rarity": c.get("rarity", ""),
            "pack": c.get("pack", ""),
            "pixel": self.status.get(card_id, ""),
            "ref": f"/ref/{card_id}",
            "tiles": tiles,
        }

    def reference(self, card_id: str) -> Path | None:
        if card_id not in self.cards:
            return None
        for ext in ("png", "jpg", "jpeg", "webp"):
            p = self.references / f"{card_id}.{ext}"
            if p.exists():
                return p
        return None

    def preview(self, bp_id: int) -> Path | None:
        b = self.ix["bp"].get(bp_id)
        url = image_url(b) if b else None
        if not url:
            return None
        dest = IMAGES / f"{bp_id}.jpg"
        if dest.exists() and dest.stat().st_size > 1000:
            return dest
        IMAGES.mkdir(parents=True, exist_ok=True)
        req = urllib.request.Request(url, headers={"User-Agent": UA})
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                data = r.read()
        except Exception as e:  # noqa: BLE001 - the tile shows "no preview"
            print(f"preview {bp_id}: {e}")
            return None
        if len(data) < 1000:
            return None
        dest.write_bytes(data)
        return dest


# ── HTTP ──────────────────────────────────────────────────────────────────

MIME = {".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp"}


class Handler(BaseHTTPRequestHandler):
    app: App

    def log_message(self, fmt, *args):  # quiet: one line per image otherwise
        pass

    def _json(self, obj, status=200):
        body = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _file(self, path: Path | None, mime: str | None = None):
        if path is None or not path.exists():
            self.send_response(404)
            self.end_headers()
            return
        data = path.read_bytes()
        self.send_response(200)
        self.send_header("Content-Type", mime or MIME.get(path.suffix.lower(), "application/octet-stream"))
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "max-age=86400")
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        url = urllib.parse.urlparse(self.path)
        q = urllib.parse.parse_qs(url.query)
        path = url.path
        if path == "/":
            self._file(HERE / "index.html", "text/html; charset=utf-8")
        elif path == "/api/state":
            self._json(self.app.state())
        elif path == "/api/card":
            card = self.app.card(q.get("id", [""])[0])
            self._json(card if card else {"error": "unknown card"}, 200 if card else 404)
        elif path == "/api/check":
            self._json(self.app.check)
        elif path.startswith("/ref/"):
            self._file(self.app.reference(urllib.parse.unquote(path[5:])))
        elif path.startswith("/preview/"):
            try:
                self._file(self.app.preview(int(path[9:])))
            except ValueError:
                self._file(None)
        else:
            self._file(None)

    def do_POST(self):
        if self.path != "/api/pick":
            self._file(None)
            return
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n) or b"{}")
        card_id = body.get("id")
        if not card_id or card_id not in self.app.cards:
            self._json({"error": "unknown card"}, 400)
            return
        if body.get("clear"):
            verdict = CLEAR
        elif body.get("none"):
            verdict = None
        else:
            verdict = int(body.get("product", 0))
            if verdict <= 0:
                self._json({"error": "product must be a positive id"}, 400)
                return
        save_pick(self.app.manual_path, card_id, verdict)
        self._json(self.app.state())


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--game", default="onepiece", choices=sorted(GAME_PREFIXES))
    ap.add_argument("--report", default=str(MAPPER / "report.txt"), help="the mapper's -report file")
    ap.add_argument("--references", default=None, help="dir of our reference images (default scripts/ml/images/<game>)")
    ap.add_argument("--port", type=int, default=8765)
    args = ap.parse_args()
    references = Path(args.references) if args.references else MAPPER.parents[1] / "ml" / "images" / args.game
    report = Path(args.report)
    if not report.exists():
        sys.exit(f"{report}: not found; run the mapper with -report first")
    app = App(args.game, report, references)
    Handler.app = app
    st = app.state()
    print(f"{args.game}: {st['total']} cards to settle, {st['settled']} done; {st['check_total']} plain cards to check")
    print(f"manual picks: {app.manual_path}")
    print(f"open http://127.0.0.1:{args.port}/  (Ctrl-C to stop)")
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
