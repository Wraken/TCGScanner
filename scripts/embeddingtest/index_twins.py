"""Twin report: which indexed cards can never lock on their own?

The app locks a match only when the top hit leads the best *different*
canonical card by margin_min (0.1). A card whose nearest neighbour in the index
scores >= 0.9 therefore cannot reach that margin no matter how clean the scan:
it is a "twin" (reprint / alternate art / same artwork in another set). This
script measures that per game so a new game's cost is a number, not a guess.

For every indexed vector it finds the nearest OTHER vector (exact search over
the whole index, batched), then reports per game:
  - how many cards have a twin (nearest >= --twin), i.e. unlockable
  - the nearest-neighbour distribution
  - the tightest pairs, so you can eyeball what the twins actually are

Games are assigned from source dirs, GAME=DIR like package_app_model.py.

    python index_twins.py
    python index_twins.py --twin 0.85 --top 15
"""
import argparse
import json
import re
from collections import defaultdict
from pathlib import Path

import faiss
import numpy as np

# Mirror of canonicalCard in card_canonical.go — keep the two in sync. Prints
# of one card face (One Piece _pN/_rN, Riftbound "-star-", known cross-set
# reprints) are excluded from the margin at scan time, so a pair inside one
# canonical group is NOT a problem twin.
_OP_VARIANT = re.compile(r"_[pr]\d+$")
_REPRINT_ALIASES = {
    "ven-r02": "ogn-042-298",
    "ven-r06": "ogn-214-298",
    "unl-t05": "sfd-t03",
}


def canonical(card_id: str) -> str:
    base = _OP_VARIANT.sub("", card_id).replace("-star-", "-", 1)
    return _REPRINT_ALIASES.get(base, base)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--index", default="models/tcg/index.faiss")
    ap.add_argument("--labels", default="models/tcg/index_labels.json")
    ap.add_argument("--images", nargs="+", metavar="GAME=DIR",
                    default=["onepiece=images/onepiece", "riftbound=images/riftbound",
                             "lorcana=../fetch/lorcana/images", "pokemon=../fetch/pokemon/images"],
                    help="source dirs, used only to assign each card id to a game")
    ap.add_argument("--twin", type=float, default=0.9,
                    help="nearest-other cosine at/above which a card cannot reach the lock margin")
    ap.add_argument("--top", type=int, default=10, help="tightest pairs to print per game")
    ap.add_argument("--batch", type=int, default=2048)
    args = ap.parse_args()

    index = faiss.read_index(args.index)
    labels = json.load(open(args.labels))
    n = index.ntotal
    assert n == len(labels), f"index ({n}) / labels ({len(labels)}) mismatch"
    vecs = index.reconstruct_n(0, n).astype(np.float32)
    faiss.normalize_L2(vecs)
    print(f"index: {n} cards x {vecs.shape[1]}D")

    game_of = {}
    for spec in args.images:
        game, _, d = spec.partition("=")
        dp = Path(d)
        if not dp.is_dir():
            continue
        for p in dp.iterdir():
            if p.suffix.lower() in {".png", ".jpg", ".jpeg", ".webp"}:
                game_of.setdefault(p.stem, game)
    games = [game_of.get(l, "unknown") for l in labels]

    # Nearest OTHER card, skipping same-canonical hits (k=4 is plenty: a
    # canonical group is at most a handful of prints).
    flat = faiss.IndexFlatIP(vecs.shape[1])
    flat.add(vecs)
    canon = [canonical(l) for l in labels]
    nearest = np.zeros(n, dtype=np.float32)
    nearest_idx = np.zeros(n, dtype=np.int64)
    for start in range(0, n, args.batch):
        q = vecs[start:start + args.batch]
        scores, idx = flat.search(q, 4)
        for r in range(len(q)):
            i = start + r
            for s, j in zip(scores[r], idx[r]):
                if j != i and canon[j] != canon[i]:
                    nearest[i], nearest_idx[i] = s, j
                    break
            else:  # every neighbour was a print of the same card
                nearest[i], nearest_idx[i] = -1, -1

    by_game = defaultdict(list)
    for i, g in enumerate(games):
        by_game[g].append(i)

    print(f"\nnearest OTHER canonical card (cosine); twin threshold = {args.twin}")
    print(f"{'game':10s} {'cards':>6s} {'twins':>6s} {'%':>5s}   {'median':>6s} {'p90':>6s} {'p99':>6s} {'max':>6s}")
    for g in sorted(by_game, key=lambda g: -len(by_game[g])):
        ids = np.array(by_game[g])
        v = nearest[ids]
        v = v[v >= 0]
        twins = int((v >= args.twin).sum())
        print(f"{g:10s} {len(ids):6d} {twins:6d} {100 * twins / len(ids):5.1f}   "
              f"{np.median(v):6.3f} {np.percentile(v, 90):6.3f} {np.percentile(v, 99):6.3f} {v.max():6.3f}")

    all_v = nearest[nearest >= 0]
    print(f"{'TOTAL':10s} {n:6d} {int((all_v >= args.twin).sum()):6d} "
          f"{100 * (all_v >= args.twin).sum() / n:5.1f}")

    for g in sorted(by_game, key=lambda g: -len(by_game[g])):
        ids = np.array(by_game[g])
        order = ids[np.argsort(-nearest[ids])][: args.top]
        print(f"\ntightest pairs — {g}:")
        seen = set()
        for i in order:
            j = nearest_idx[i]
            if j < 0 or (j, i) in seen:
                continue
            seen.add((i, j))
            cross = "" if games[j] == g else f"  (cross-game: {games[j]})"
            print(f"  {labels[i]:18s} ~ {labels[j]:18s} {nearest[i]:.3f}{cross}")


if __name__ == "__main__":
    main()
