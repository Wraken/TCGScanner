# scripts/fetch/cardmarket/pixel_tiebreak.py
"""Pixel tie-break for the ambiguous printings of one game.

    python pixel_tiebreak.py --game onepiece            # report.txt -> visual.json, pixel_status.json

For every card the mapper's report lists as ambiguous, our reference image
is compared to the CardTrader original of each candidate printing (Pearson
correlation of both images in grayscale 200x280). The best printing is kept
when it clears the floor and leads the next by the margin; the mapper
re-checks both when it applies visual.json. Two ids of one base card in one
pack that pick the same printing are identical twins (an artwork no image
can tell apart) and are both dropped. pixel_status.json records the
outcome per card for the review tool. Scans are cached in cache/images/,
the review tool's folder. Design: docs/superpowers/specs/
2026-10-08-price-reference-selector-design.md.

A card settles only on a complete field: when one candidate product has no
scan (on no blueprint, on several, or the download failed) the card is
"no image" and gets no pick.

Re-runs merge: the report only lists the cards still ambiguous (the mapper
applies visual.json), so picks and statuses of cards absent from it are
kept and only the listed cards are recomputed. Delete visual.json and
pixel_status.json, and pass a report built without them, for a fresh run.
"""

from __future__ import annotations

import argparse
import collections
import json
import sys
import time
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / "review"))
from review import CARDTRADER_GAME, GAME_PREFIXES, IMAGES, image_url, read_ambiguous  # noqa: E402

FLOOR, MARGIN = 0.7, 0.15
UA = "TCGScanner cardmarket mapper (personal ML project)"
SIZE = (200, 280)


def decide(scored, floor, margin):
    """scored: [(correlation, blueprint id, products), ...], best first.
    Returns (status, best) with status settled | tied | low | no image."""
    if not scored:
        return "no image", None
    best = scored[0]
    if best[0] < floor:
        return "low", None
    lead = best[0] - (scored[1][0] if len(scored) > 1 else 0.0)
    if lead < margin:
        return "tied", None
    return "settled", best


def settle(scored, missing, floor, margin):
    """decide, on a complete field only: missing counts the candidates that
    could not be scored (no blueprint, several, or no scan). Comparing the
    best against an incomplete field could settle on the wrong printing."""
    if missing:
        return "no image", None
    return decide(scored, floor, margin)


def merge_picks(existing, fresh, in_report):
    """A re-run's result: entries of ids absent from the report are kept,
    ids in the report take the fresh entry (or none when it no longer has
    one). Works for picks and for statuses alike."""
    out = {k: v for k, v in existing.items() if k not in in_report}
    out.update({k: v for k, v in fresh.items() if k in in_report})
    return dict(sorted(out.items()))


def kind(card_id):
    return "p" if "_p" in card_id else "r"


def twin_rule(picks, pack_of):
    """picks: card id -> blueprint id. Two ids of one base card, one pack and
    one kind on one printing are twins: both dropped. Returns (kept, twins)."""
    groups = collections.defaultdict(list)
    for card_id, bp in picks.items():
        groups[(card_id.split("_")[0], pack_of.get(card_id, ""), kind(card_id), bp)].append(card_id)
    twins = sorted(c for g in groups.values() if len(g) > 1 for c in g)
    kept = {c: bp for c, bp in picks.items() if c not in set(twins)}
    return kept, twins


def load_blueprints(game):
    """Cardmarket product -> the blueprints listing it (the mapper's byCM);
    a product on several blueprints cannot be scored."""
    exps = json.loads((HERE / "cache" / "cardtrader_expansions.json").read_text("utf-8"))
    by_cm = {}
    for e in exps:
        if e.get("game_id") != CARDTRADER_GAME[game]:
            continue
        f = HERE / "cache" / f"cardtrader_blueprints_{e['id']}.json"
        if not f.exists():
            continue
        for b in json.loads(f.read_text("utf-8")):
            for cm in b.get("card_market_ids") or []:
                if cm and all(x["id"] != b["id"] for x in by_cm.get(cm, [])):
                    by_cm.setdefault(cm, []).append(b)
    return by_cm


def fetch(b):
    url = image_url(b)
    if not url:
        return None
    dest = IMAGES / f"{b['id']}.jpg"
    if dest.exists() and dest.stat().st_size > 1000:
        return dest
    IMAGES.mkdir(parents=True, exist_ok=True)
    try:
        req = urllib.request.Request(url, headers={"User-Agent": UA})
        with urllib.request.urlopen(req, timeout=30) as r:
            data = r.read()
    except Exception as e:  # noqa: BLE001 - the card stays unresolved
        print(f"  image {b['id']}: {e}")
        return None
    if len(data) < 1000:
        return None
    dest.write_bytes(data)
    time.sleep(0.1)
    return dest


def main():
    import numpy as np
    from PIL import Image

    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--game", default="onepiece", choices=sorted(GAME_PREFIXES))
    ap.add_argument("--report", default=str(HERE / "report.txt"))
    ap.add_argument("--references", default=None, help="default scripts/ml/images/<game>")
    ap.add_argument("--out", default=str(HERE / "visual.json"))
    ap.add_argument("--status", default=str(HERE / "pixel_status.json"))
    args = ap.parse_args()
    refs = Path(args.references) if args.references else HERE.parents[1] / "ml" / "images" / args.game
    cards = json.loads((HERE.parent / args.game / "cards.json").read_text("utf-8"))
    pack_of = {i: c.get("pack", "") for i, c in cards.items()}
    amb = read_ambiguous(Path(args.report).read_text("utf-8"), GAME_PREFIXES[args.game])
    by_cm = load_blueprints(args.game)

    cache = {}

    def arr(path):
        if path not in cache:
            # via RGBA: palette PNGs with transparency warn when converted straight to L
            cache[path] = np.asarray(Image.open(path).convert("RGBA").convert("L").resize(SIZE), dtype=np.float32)
        return cache[path]

    def corr(a, b):
        a = a - a.mean()
        b = b - b.mean()
        return float((a * b).sum() / (np.sqrt((a * a).sum() * (b * b).sum()) + 1e-9))

    picks, best_of, status = {}, {}, {}
    for n, (card_id, cands) in enumerate(amb.items(), 1):
        ref = None
        for ext in ("png", "jpg", "webp"):
            if (refs / f"{card_id}.{ext}").exists():
                ref = refs / f"{card_id}.{ext}"
                break
        if ref is None:
            status[card_id] = "no image"
            continue
        scored, seen, missing = [], set(), 0
        for p in cands:
            bps = by_cm.get(p, [])
            if len(bps) != 1:
                missing += 1
                continue
            b = bps[0]
            if b["id"] in seen:
                continue
            seen.add(b["id"])
            path = fetch(b)
            if not path:
                missing += 1
                continue
            scored.append((corr(arr(ref), arr(path)), b["id"], [cm for cm in b.get("card_market_ids") or [] if cm in cands]))
        scored.sort(reverse=True)
        st, best = settle(scored, missing, FLOOR, MARGIN)
        status[card_id] = st
        if best:
            picks[card_id] = best[1]
            best_of[card_id] = (best, best[0] - (scored[1][0] if len(scored) > 1 else 0.0))
        if n % 100 == 0:
            print(f"  {n}/{len(amb)}", flush=True)

    kept, twins = twin_rule(picks, pack_of)
    for t in twins:
        status[t] = "twin"
    fresh = {}
    for card_id in sorted(kept):
        (score, _, products), lead = best_of[card_id]
        fresh[card_id] = {"product": products[0], "score": round(score, 4), "lead": round(lead, 4)}
    # Merge with the previous run: the report no longer lists what it settled.
    in_report = set(amb)
    out_path, status_path = Path(args.out), Path(args.status)
    old = json.loads(out_path.read_text("utf-8")) if out_path.exists() else {}
    old_status = json.loads(status_path.read_text("utf-8")) if status_path.exists() else {}
    out = {"floor": FLOOR, "margin": MARGIN, "picks": merge_picks(old.get("picks", {}), fresh, in_report)}
    merged_status = merge_picks(old_status, status, in_report)
    out_path.write_text(json.dumps(out, indent=1) + "\n", "utf-8")
    status_path.write_text(json.dumps(merged_status, indent=1) + "\n", "utf-8")
    counts = collections.Counter(status.values())
    print(f"{args.game}: {len(amb)} ambiguous -> settled {len(fresh)}, "
          + ", ".join(f"{k} {v}" for k, v in sorted(counts.items()) if k != "settled"))
    print(f"wrote {args.out} ({len(out['picks'])} picks after merging) and {args.status}")


if __name__ == "__main__":
    main()
