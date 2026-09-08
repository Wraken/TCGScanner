"""
Remove the SAMPLE watermark from One Piece card images -- v3, English PNGs.

v2 was calibrated on the March 2026 French webp pull: one translucent SAMPLE
at one place on every card. The English site's PNGs (punk-records `img_url`,
re-exported 2026-08-28) come in two styles:
  - translucent, same rendering and placement as the calibration (a per-image
    alignment search was tried and never beat the fixed placement: the
    residual comes from the palette encoding, not from the position);
  - OPAQUE white on the oldest sets (OP01-OP05, ST01-ST10, part of OP02...):
    the art under the letters is lost, so the letters are inpainted from
    their surroundings (measured +0.05..0.10 embedding cosine over leaving
    them white).

Per image v3 therefore:
  1. looks for OPAQUE letters: the binary pure-white mask of the middle band
     is matched against the calibrated letter shape at several scales; a
     strong match means solid white SAMPLE letters sit there (translucent
     letters saturate only fragments and score low);
  2. opaque  -> mask = the matched letter shape, dilated, TELEA inpaint
                (the shape, not the white blob: letters touching white art
                such as Bepo's fur are cut out cleanly);
     else    -> v2's exact inversion with the calibrated alpha map.

Usage
    python unwater_op_v3.py remove --input images/ --output clean/ --params wm_v2.npz
        [--ids ids.json] [--report report.csv] [--limit N]
    python unwater_op_v3.py preview --input card.png --params wm_v2.npz --output preview.png
"""

import argparse
import csv
import json
import os

import cv2
import numpy as np
from tqdm import tqdm

from unwater_op_v2 import list_images, remove as invert_v2

# Rows of the card the SAMPLE text lives in (fraction of h).
BAND = (0.30, 0.70)
# Template scales tried (the opaque exports differ by up to ~20 % in size).
SCALES = np.round(np.arange(0.80, 1.21, 0.025), 3)
# Normalised correlation of the white mask with the letter shape above which
# the letters are opaque (measured: opaque 0.55-0.85, translucent < 0.35).
OPAQUE_SCORE = 0.45
PURE_WHITE = 250


def load_params(params):
    d = np.load(params)
    alpha, plateau = d["alpha"].astype(np.float32), float(d["plateau"])
    ys, xs = np.where(alpha > 0.5)
    letters = (alpha[ys.min():ys.max() + 1, xs.min():xs.max() + 1] > 0.5).astype(np.float32)
    return alpha, plateau, letters


def find_opaque_letters(image, letters):
    """(score, mask): best normalised correlation of the band's pure-white mask
    with the letter shape over SCALES, and the full-size mask of that shape at
    the matched position, grown by 2 px for the antialiased edges."""
    h, w = image.shape[:2]
    y0, y1 = int(h * BAND[0]), int(h * BAND[1])
    white = (image[y0:y1].min(axis=2) >= PURE_WHITE).astype(np.float32)
    best = (-1.0, None)
    for s in SCALES:
        th, tw = int(round(letters.shape[0] * s)), int(round(letters.shape[1] * s))
        if th >= white.shape[0] or tw >= white.shape[1]:
            continue
        t = cv2.resize(letters, (tw, th), interpolation=cv2.INTER_AREA)
        res = cv2.matchTemplate(white, t, cv2.TM_CCOEFF_NORMED)
        _, score, _, loc = cv2.minMaxLoc(res)
        if score > best[0]:
            best = (float(score), (s, loc[1] + y0, loc[0], th, tw, t))
    score, hit = best
    mask = np.zeros((h, w), np.uint8)
    if hit is not None:
        _, y, x, th, tw, t = hit
        mask[y:y + th, x:x + tw] = (t > 0.5).astype(np.uint8) * 255
        mask = cv2.dilate(mask, np.ones((5, 5), np.uint8))
    return score, mask


def clean(image, alpha, plateau, letters):
    """Returns (cleaned, info) with info = dict(mode, score)."""
    h, w = image.shape[:2]
    a = alpha if alpha.shape == (h, w) else cv2.resize(alpha, (w, h))
    score, mask = find_opaque_letters(image, letters)
    if score >= OPAQUE_SCORE:
        return cv2.inpaint(image, mask, 5, cv2.INPAINT_TELEA), {"mode": "inpaint", "score": round(score, 3)}
    return invert_v2(image, a, plateau), {"mode": "invert", "score": round(score, 3)}


def cmd_remove(args):
    alpha, plateau, letters = load_params(args.params)
    paths = list_images(args.input)
    if args.ids:
        keep = set(json.load(open(args.ids)))
        paths = [p for p in paths if p.stem in keep]
    if args.limit:
        paths = paths[: args.limit]
    os.makedirs(args.output, exist_ok=True)
    rows = []
    for p in tqdm(paths, desc="Cleaning"):
        img = cv2.imread(str(p), cv2.IMREAD_COLOR)
        if img is None:
            continue
        out, info = clean(img, alpha, plateau, letters)
        cv2.imwrite(os.path.join(args.output, p.stem + ".png"), out)
        rows.append({"id": p.stem, **info})
    if args.report:
        with open(args.report, "w", newline="") as f:
            w = csv.DictWriter(f, fieldnames=["id", "mode", "score"])
            w.writeheader()
            w.writerows(rows)
    modes = {}
    for r in rows:
        modes[r["mode"]] = modes.get(r["mode"], 0) + 1
    print(f"done -> {args.output}  ({len(rows)} images) modes={modes}")


def cmd_preview(args):
    alpha, plateau, letters = load_params(args.params)
    img = cv2.imread(args.input, cv2.IMREAD_COLOR)
    out, info = clean(img, alpha, plateau, letters)
    h = img.shape[0]
    y1, y2 = int(h * 0.36), int(h * 0.64)
    if info["mode"] == "inpaint":
        mask = find_opaque_letters(img, letters)[1]
    else:
        a = cv2.resize(alpha, (img.shape[1], img.shape[0])) if alpha.shape != img.shape[:2] else alpha
        mask = (a / max(float(a.max()), 1e-6) * 255).astype(np.uint8)
    cv2.imwrite(args.output, np.hstack([img, cv2.cvtColor(mask, cv2.COLOR_GRAY2BGR), out]))
    cv2.imwrite(args.output.replace(".png", "_band.png"), np.hstack([img[y1:y2], out[y1:y2]]))
    print(f"preview: {info} -> {args.output} (+ _band.png)")


def main():
    ap = argparse.ArgumentParser(description="SAMPLE watermark removal v3 (English PNGs: invert or inpaint)")
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("remove", help="Clean a directory")
    r.add_argument("--input", required=True)
    r.add_argument("--output", required=True)
    r.add_argument("--params", required=True, help="alpha map .npz from unwater_op_v2.py estimate")
    r.add_argument("--ids", help="JSON list of ids (file stems) to process; default: every image")
    r.add_argument("--report", help="CSV of per-image mode and opaque score")
    r.add_argument("--limit", type=int, default=0)
    r.set_defaults(func=cmd_remove)
    p = sub.add_parser("preview", help="Preview on one card: original | mask | result")
    p.add_argument("--input", required=True)
    p.add_argument("--params", required=True)
    p.add_argument("--output", default="watermark_preview_v3.png")
    p.set_defaults(func=cmd_preview)
    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
