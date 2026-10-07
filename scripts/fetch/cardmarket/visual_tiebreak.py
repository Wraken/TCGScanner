"""Visual tie-break for the Cardmarket mapper (step 3 of the spec).

Our variant ids (OP01-025_p1, OP01-025_p2 ...) name printings by number;
CardTrader names them by letter suffix and version label, and nothing maps
one scheme to the other. These printings differ precisely by their artwork,
which is what our embedding model separates, so this script embeds, for
every ambiguous card of the report, our reference image and every
candidate blueprint's preview image with the model behind the index, and
keeps the candidate closest to ours when it leads the runner-up by the
lock margin of the app. Everything else stays unresolved.

Runs where the model and the references are (the training PC, WSL, conda
env `riftbound`, see scripts/ml/RETRAIN.md):

    cd scripts/fetch/cardmarket
    python visual_tiebreak.py --report report.txt --game onepiece \
        --model ../../ml/models/tcg --references ../../ml/images/onepiece \
        --out visual.json

then `go run . -games onepiece -offline` reads visual.json as a source.
Previews are cached under cache/previews/ (gitignored). visual.json is
committed: one entry per card, with the margin that justified it, so a
pick can be audited and the step replayed without the GPU.

Preparation mirrors build_index.py exactly (same backbone preprocess, same
size, the reference embedded with the same augmentation average), so the
distances are the ones the app itself would see.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
ML = HERE.parent.parent / "ml"
sys.path.insert(0, str(ML))

CACHE = HERE / "cache"
PREVIEWS = CACHE / "previews"
UA = "TCGScanner cardmarket mapper (personal ML project)"
AMBIGUOUS = re.compile(r"^ambiguous\s+(\S+)\s+candidates \[([^\]]*)\]")


def read_ambiguous(report: Path, game_prefixes: tuple[str, ...]) -> dict[str, list[int]]:
    """card id -> Cardmarket candidate ids, from the mapper's report."""
    out: dict[str, list[int]] = {}
    for line in report.read_text(encoding="utf-8").splitlines():
        m = AMBIGUOUS.match(line)
        if not m or not m.group(1).startswith(game_prefixes):
            continue
        out[m.group(1)] = [int(x) for x in m.group(2).split()]
    return out


def load_blueprints(game_id: int) -> dict[int, dict]:
    """Cardmarket product id -> the CardTrader blueprint that lists it
    (expansions and blueprints come from the mapper's cache)."""
    exps = json.loads((CACHE / "cardtrader_expansions.json").read_text(encoding="utf-8"))
    by_cm: dict[int, dict] = {}
    for e in exps:
        if e.get("game_id") != game_id:
            continue
        f = CACHE / f"cardtrader_blueprints_{e['id']}.json"
        if not f.exists():
            continue
        for b in json.loads(f.read_text(encoding="utf-8")):
            for cm in b.get("card_market_ids") or []:
                if cm and cm not in by_cm:
                    by_cm[cm] = b
    return by_cm


def fetch_preview(bp: dict) -> Path | None:
    url = bp.get("image_url")
    if not url:
        return None
    PREVIEWS.mkdir(parents=True, exist_ok=True)
    dest = PREVIEWS / f"{bp['id']}.jpg"
    if dest.exists() and dest.stat().st_size > 1000:
        return dest
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            data = r.read()
    except Exception as e:  # noqa: BLE001 - reported, the card stays unresolved
        print(f"  preview {bp['id']}: {e}")
        return None
    if len(data) < 1000:
        return None
    dest.write_bytes(data)
    time.sleep(0.1)
    return dest


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--report", required=True, help="the mapper's -report file")
    ap.add_argument("--game", default="onepiece", choices=["onepiece", "riftbound"])
    ap.add_argument("--model", required=True, help="model dir with embedding_model.keras and config.json")
    ap.add_argument("--references", required=True, help="dir of our reference images (one file per card id)")
    ap.add_argument("--out", default="visual.json")
    ap.add_argument("--margin", type=float, default=None,
                    help="min lead of the best candidate over the runner-up (default: the model's margin_min)")
    ap.add_argument("--floor", type=float, default=None,
                    help="min cosine of the best candidate (default: the model's score_floor)")
    args = ap.parse_args()

    prefixes = {"onepiece": ("OP", "ST", "EB", "PRB", "P-"), "riftbound": ("ogn", "ogs", "sfd", "unl", "ven", "arc", "rad", "lgc", "rec")}[args.game]
    game_id = {"onepiece": 15, "riftbound": 22}[args.game]
    ambiguous = read_ambiguous(Path(args.report), prefixes)
    if not ambiguous:
        print("nothing ambiguous for this game in the report")
        return
    print(f"{len(ambiguous)} ambiguous cards")

    # Model, exactly as build_index.py loads it (imports kept here so the
    # report parsing above runs on a machine without the ML stack).
    import numpy as np  # noqa: E402
    import keras  # noqa: E402
    from build_index import BACKBONES, compute_reference_embedding, load_image  # noqa: E402

    model_dir = Path(args.model)
    config = json.loads((model_dir / "config.json").read_text(encoding="utf-8"))
    img_size = config["img_size"]
    preprocess = BACKBONES[config["backbone"]]["preprocess"]
    margin = args.margin if args.margin is not None else float(config.get("margin_min", 0.1))
    floor = args.floor if args.floor is not None else float(config.get("score_floor", 0.4))
    model = keras.models.load_model(model_dir / "embedding_model.keras")
    print(f"model {model_dir.name}: {config['backbone']} {img_size}px, floor {floor}, margin {margin}")

    refs = {p.stem: p for p in Path(args.references).iterdir() if p.suffix.lower() in {".png", ".jpg", ".jpeg", ".webp"}}
    by_cm = load_blueprints(game_id)

    # One embedding per preview, shared by every card that lists it.
    preview_emb: dict[int, object] = {}

    def embed_file(path: Path):
        return compute_reference_embedding(model, load_image(path, img_size), preprocess)[0]

    picks: dict[str, dict] = {}
    unresolved: dict[str, str] = {}
    for n, (card, cands) in enumerate(sorted(ambiguous.items()), 1):
        ref = refs.get(card)
        if ref is None:
            unresolved[card] = "no reference image"
            continue
        ours = embed_file(ref)
        scored: list[tuple[float, int]] = []
        for cm in cands:
            bp = by_cm.get(cm)
            if bp is None:
                continue
            if cm not in preview_emb:
                p = fetch_preview(bp)
                if p is None:
                    continue
                preview_emb[cm] = embed_file(p)
            scored.append((float(np.dot(ours, preview_emb[cm])), cm))
        if not scored:
            unresolved[card] = "no candidate preview"
            continue
        scored.sort(reverse=True)
        best = scored[0]
        # Candidates that are the same printing on two shelves (English and
        # non-English) share one preview and so one score; the lead is
        # measured against the best candidate of a different artwork. The
        # mapper chooses the shelf afterwards (rule B), not this script.
        second = next((s for s in scored[1:] if best[0] - s[0] > 1e-6), (-1.0, 0))
        lead = best[0] - second[0]
        if best[0] < floor:
            unresolved[card] = f"best {best[0]:.3f} under floor"
        elif second[0] >= 0 and lead < margin:
            unresolved[card] = f"lead {lead:.3f} under margin (best {best[1]} {best[0]:.3f}, next {second[1]} {second[0]:.3f})"
        else:
            picks[card] = {"product": best[1], "score": round(best[0], 4), "lead": round(lead, 4),
                           "blueprint": by_cm[best[1]]["id"], "candidates": len(scored)}
        if n % 100 == 0:
            print(f"  {n}/{len(ambiguous)}: {len(picks)} picked, {len(unresolved)} left")

    out = {"game": args.game, "model": model_dir.name, "floor": floor, "margin": margin,
           "picks": picks, "unresolved": unresolved}
    Path(args.out).write_text(json.dumps(out, indent=1, sort_keys=True), encoding="utf-8")
    print(f"{args.out}: {len(picks)} picked, {len(unresolved)} unresolved")
    reasons: dict[str, int] = {}
    for why in unresolved.values():
        reasons[why.split(" (")[0].split(" 0.")[0]] = reasons.get(why.split(" (")[0].split(" 0.")[0], 0) + 1
    for why, k in sorted(reasons.items(), key=lambda x: -x[1]):
        print(f"  {k:5d}  {why}")


if __name__ == "__main__":
    main()
