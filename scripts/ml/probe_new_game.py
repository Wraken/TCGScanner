"""Read-only probe: would the frozen embedding model handle a new game or set?

Point a --images dir of card scans (a new game like Lorcana or Pokemon, or a
new set) at this and it embeds a sample with the frozen model WITHOUT touching
the index, answering two questions before you commit to add_to_index:

  1. Separation   - do different cards get distinct embeddings, or does the
                    model collapse them into one blob? (nearest OTHER sample
                    card's cosine; low = good, ~0.9 = a near-identical pair)
  2. Contamination- does a new card falsely match an existing indexed card?
                    (nearest card in the current index; low = good)

The match margin at scan time is roughly self_score minus the best rival, where
the rival is whichever is higher of (nearest other sample card, nearest existing
card). So the decisive number per card is that worst rival: below ~0.85 leaves
room for a healthy >0.1 margin. Note: on a large catalog, use --even so the
sample spans all eras, and remember a sparse sample under-counts reprint pairs.

    python probe_new_game.py --images ../fetch/lorcana/images
    python probe_new_game.py --images ../fetch/pokemon/images --limit 400 --even
"""
import argparse
import json
import os
from pathlib import Path

os.environ.setdefault("TF_CPP_MIN_LOG_LEVEL", "2")

import numpy as np
from tensorflow import keras

keras.mixed_precision.set_global_policy("float32")
import faiss

from build_index import SUPPORTED_EXTS, compute_reference_embedding


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--name", default="tcg")
    ap.add_argument("--images", default="../fetch/lorcana/images")
    ap.add_argument("--limit", type=int, default=0, help="cap sample size (0 = all)")
    ap.add_argument("--even", action="store_true",
                    help="sample --limit cards evenly across the sorted set (covers all eras)")
    args = ap.parse_args()

    model_dir = os.path.join("models", args.name)
    with open(os.path.join(model_dir, "config.json")) as f:
        config = json.load(f)
    img_size = config["img_size"]

    from train import BACKBONES

    preprocess_fn = BACKBONES[config["backbone"]]["preprocess"]

    index = faiss.read_index(os.path.join(model_dir, "index.faiss"))
    labels = json.load(open(os.path.join(model_dir, "index_labels.json")))
    print(f"Existing index: {index.ntotal} cards ({len(labels)} labels)")

    paths = sorted(p for p in Path(args.images).iterdir() if p.suffix.lower() in SUPPORTED_EXTS)
    if args.limit and args.even and len(paths) > args.limit:
        step = len(paths) / args.limit
        paths = [paths[int(i * step)] for i in range(args.limit)]
    elif args.limit:
        paths = paths[: args.limit]
    if not paths:
        print(f"No images under {args.images}")
        return
    print(f"Sample: {len(paths)} cards from {args.images}\n")

    model = keras.models.load_model(os.path.join(model_dir, "embedding_model.keras"))

    embs = []
    for p in paths:
        img = keras.utils.load_img(str(p), target_size=(img_size, img_size))
        arr = keras.utils.img_to_array(img).astype(np.float32)
        embs.append(compute_reference_embedding(model, arr, preprocess_fn))
    embs = np.vstack(embs).astype(np.float32)
    faiss.normalize_L2(embs)
    ids = [p.stem for p in paths]

    # 1. Contamination: nearest existing (Riftbound/OP) card.
    cross_scores, cross_idx = index.search(embs, k=1)
    cross = cross_scores[:, 0]

    # 2. Separation: nearest OTHER sample card (self is column 0 at ~1.0).
    sample_idx = faiss.IndexFlatIP(embs.shape[1])
    sample_idx.add(embs)
    intra_scores, intra_idx = sample_idx.search(embs, k=2)
    intra = intra_scores[:, 1]

    worst = np.maximum(cross, intra)

    def stats(name, arr):
        print(f"{name:28s} min={arr.min():.3f}  median={np.median(arr):.3f}  "
              f"p90={np.percentile(arr, 90):.3f}  max={arr.max():.3f}")

    print("Cosine of each sample card to its nearest...")
    stats("  ...existing indexed card", cross)
    stats("  ...other sample card", intra)
    stats("  ...rival (max of both)", worst)

    healthy = int((worst < 0.85).sum())
    print(f"\nCards whose worst rival < 0.85 (margin >0.10 reachable): "
          f"{healthy}/{len(ids)} ({100 * healthy / len(ids):.0f}%)")

    print("\nWorst contamination (sample matching an existing card):")
    for i in np.argsort(-cross)[:6]:
        print(f"  {ids[i]:10s} -> {labels[cross_idx[i][0]]:16s} score={cross[i]:.3f}")

    print("\nTightest pairs (least separation):")
    for i in np.argsort(-intra)[:6]:
        print(f"  {ids[i]:10s} ~ {ids[intra_idx[i][1]]:10s} score={intra[i]:.3f}")


if __name__ == "__main__":
    main()
