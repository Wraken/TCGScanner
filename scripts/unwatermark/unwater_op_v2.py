"""
Remove the SAMPLE watermark from One Piece card images -- v2, statistical.

The watermark is the SAME alpha-blend on every card:
    observed = alpha * W + (1 - alpha) * original
with alpha (opacity) and W (colour) identical across the whole set and only
`original` changing. With ~2000 aligned cards we ESTIMATE alpha per pixel
instead of guessing a uniform strength, then invert the blend exactly.

Estimator (no regression, no iteration): per pixel across the stack,
  - the 1st percentile of observed ~ alpha*W          (cards whose original is black there)
  - the 99th percentile     ~ alpha*W + (1-alpha)*255 (original white there)
  => alpha = 1 - (p99 - p1)/255, and W = p1/alpha (measured ~white, we use 255).
Outside the letters this yields alpha ~ 0 by itself (self-checking).

The letter BODY is a flat plateau (measured 0.68): we use the plateau value
there (percentiles are noisy pixel-wise). The letter EDGE is antialiased and
slightly asymmetric: there we keep the raw percentile alpha, which measures
the real transition, times a small gain. Both factors (body k, edge g) were
calibrated so the mean of the cleaned stack shows NO residual letters
(residual luminance ~ +-3 on 0-255, i.e. webp noise). See calibrate().

Usage
    python unwater_op_v2.py estimate --input images/onepiece --output wm_v2.npz
    python unwater_op_v2.py preview  --input images/onepiece/OP06-118_p1.webp --params wm_v2.npz
    python unwater_op_v2.py remove   --input images/onepiece --output images/onepiece-clean --params wm_v2.npz
"""

import argparse
import os
from pathlib import Path

import cv2
import numpy as np
from tqdm import tqdm

EXTS = ("*.png", "*.webp", "*.jpg", "*.jpeg")
# Loading band (fraction of card height) -- generous, for the estimation stack.
BAND = (0.36, 0.64)
# Hard ROI where the SAMPLE text actually lives (measured on the set, same as
# v1): alpha is forced to 0 outside it. Percentile noise elsewhere (card
# borders, always-coloured zones) would otherwise leak a faint correction.
ROI = (0.41, 0.59, 0.08, 0.92)   # y1, y2, x1, x2 as fractions of h / w
W_COLOR = 255.0  # measured 250-255 in letter cores


def list_images(d):
    p = Path(d)
    out = []
    for e in EXTS:
        out += list(p.glob(e))
    return sorted(out)


# --------------------------------------------------------------------------
# Estimation
# --------------------------------------------------------------------------
def load_band_stack(paths, size, limit):
    """(N, band_h, W, 3) float32 stack of the SAMPLE band only."""
    w, h = size
    y1, y2 = int(h * BAND[0]), int(h * BAND[1])
    if limit and limit < len(paths):
        step = len(paths) // limit
        paths = paths[::step][:limit]
    imgs = []
    for p in tqdm(paths, desc="Loading"):
        im = cv2.imread(str(p))
        if im is None:
            continue
        if (im.shape[1], im.shape[0]) != size:
            im = cv2.resize(im, size)
        imgs.append(im[y1:y2].astype(np.float32))
    return np.stack(imgs), (y1, y2)


def percentile_alpha(stack, p_lo=1.0, p_hi=99.0):
    lo = np.percentile(stack, p_lo, axis=0)
    hi = np.percentile(stack, p_hi, axis=0)
    alpha = 1.0 - (hi - lo) / 255.0          # per channel
    return np.clip(alpha.mean(axis=2), 0.0, 0.95)


def hybrid_alpha(alpha_raw, plateau, k, g, floor=0.08):
    """Plateau*k inside the letter body, raw*g on the antialiased edge.
    Pixels below `floor` (estimator noise, ~0.05 max where there is no
    watermark) and pixels farther than 3px from any letter are zeroed."""
    mask = (alpha_raw > plateau * 0.5).astype(np.uint8)
    body = cv2.erode(mask, np.ones((3, 3), np.uint8)) > 0
    near = cv2.dilate(mask, np.ones((7, 7), np.uint8)) > 0
    a = np.where(body, plateau * k, alpha_raw * g)
    a = np.clip(a, 0.0, 0.95)
    a[(a < floor) | ~near] = 0.0
    return a.astype(np.float32)


def residual_score(stack, alpha, plateau):
    """Luminance of the MEAN cleaned card at letter body / inner edge / outer
    edge, relative to the far neighbourhood. 0 = watermark invisible."""
    mask = (alpha > plateau * 0.5).astype(np.uint8)
    inner = cv2.erode(mask, np.ones((5, 5), np.uint8)) > 0
    ring_in = (mask > 0) & ~inner
    ring_out = (cv2.dilate(mask, np.ones((5, 5), np.uint8)) > 0) & ~(mask > 0)
    far = (cv2.dilate(mask, np.ones((25, 25), np.uint8)) > 0) & ~(cv2.dilate(mask, np.ones((9, 9), np.uint8)) > 0)
    a3 = alpha[None, ..., None]
    out = np.clip((stack - a3 * W_COLOR) / np.maximum(1 - a3, 0.05), 0, 255)
    mean = out.mean(axis=0).mean(axis=2)
    ref = mean[far].mean()
    return mean[inner].mean() - ref, mean[ring_in].mean() - ref, mean[ring_out].mean() - ref


def calibrate(stack, alpha_raw, plateau, ks=(1.0, 1.03, 1.06), gs=(1.0, 1.15, 1.3)):
    """Pick (k, g) minimising |body|+|edge_in|+|edge_out| on the mean cleaned card."""
    best = None
    for k in ks:
        for g in gs:
            a = hybrid_alpha(alpha_raw, plateau, k, g)
            s = residual_score(stack, a, plateau)
            tot = sum(abs(x) for x in s)
            if best is None or tot < best[0]:
                best = (tot, k, g, s)
    return best[1], best[2], best[3]


def cmd_estimate(args):
    paths = list_images(args.input)
    if len(paths) < 50:
        print(f"Need >= 50 images, got {len(paths)}")
        return
    first = cv2.imread(str(paths[0]))
    size = (first.shape[1], first.shape[0])
    stack, (y1, y2) = load_band_stack(paths, size, args.limit)
    print(f"band stack: {stack.shape}")

    alpha_raw = percentile_alpha(stack)
    plateau = float(np.median(alpha_raw[alpha_raw > 0.5]))
    print(f"letter plateau alpha = {plateau:.3f}  (p50 over band = {np.percentile(alpha_raw, 50):.3f})")

    if args.no_calib:
        k, g = 1.03, 1.15   # values calibrated on the 2026-08 One Piece set
    else:
        k, g, s = calibrate(stack, alpha_raw, plateau)
        print(f"calibrated k={k} g={g}  residual body/edge_in/edge_out = {s[0]:+.2f}/{s[1]:+.2f}/{s[2]:+.2f}")
    alpha_band = hybrid_alpha(alpha_raw, plateau, k, g)

    h, w = size[1], size[0]
    alpha = np.zeros((h, w), np.float32)
    alpha[y1:y2] = alpha_band
    # hard ROI: nothing outside the measured SAMPLE box is ever touched
    roi = np.zeros((h, w), bool)
    roi[int(h * ROI[0]):int(h * ROI[1]), int(w * ROI[2]):int(w * ROI[3])] = True
    alpha[~roi] = 0.0
    np.savez_compressed(args.output, alpha=alpha, plateau=plateau, k=k, g=g)
    base = args.output.rsplit(".", 1)[0]
    cv2.imwrite(base + "_alpha.png", (alpha / max(alpha.max(), 1e-6) * 255).astype(np.uint8))
    print(f"saved {args.output} (+ _alpha.png)  |  pixels alpha>0.05: {(alpha > 0.05).sum()}")


# --------------------------------------------------------------------------
# Removal
# --------------------------------------------------------------------------
def remove(image, alpha, plateau=None, edge_inpaint=True):
    """orig = (obs - a*W) / (1 - a), per pixel & channel; W = white.

    edge_inpaint: the 1-2 px antialiased transition has an alpha the
    percentile estimator cannot pin down (webp smears each card's edge by a
    different sub-pixel amount), leaving a faint light halo. Those edge
    pixels (alpha in ]0.05, 0.72[, i.e. below the letter plateau) are
    inpainted from their already-clean neighbours -- a 1 px TELEA fill on
    contours only, invisible, unlike v1's 5 px fill on dilated masks."""
    h, w = image.shape[:2]
    if alpha.shape != (h, w):
        alpha = cv2.resize(alpha, (w, h))
    a = alpha[..., None]
    out = (image.astype(np.float32) - a * W_COLOR) / np.maximum(1.0 - a, 0.05)
    out = np.clip(out, 0, 255).astype(np.uint8)
    if edge_inpaint:
        top = plateau * 1.02 if plateau else 0.72
        edge = ((alpha > 0.05) & (alpha < top)).astype(np.uint8) * 255
        out = cv2.inpaint(out, edge, 1, cv2.INPAINT_TELEA)
    return out


def cmd_preview(args):
    d = np.load(args.params)
    alpha, plateau = d["alpha"], float(d["plateau"])
    img = cv2.imread(args.input)
    out = remove(img, alpha, plateau, edge_inpaint=not args.no_inpaint)
    h = img.shape[0]
    y1, y2 = int(h * BAND[0]), int(h * BAND[1])
    a_vis = cv2.cvtColor((alpha / max(alpha.max(), 1e-6) * 255).astype(np.uint8), cv2.COLOR_GRAY2BGR)
    full = np.hstack([img, a_vis, out])
    band = np.hstack([img[y1:y2], out[y1:y2]])
    outp = args.output or "watermark_preview_v2.png"
    cv2.imwrite(outp, full)
    cv2.imwrite(outp.replace(".png", "_band.png"), band)
    print(f"preview: original | alpha | result -> {outp}  (+ _band.png zoom)")


def cmd_remove(args):
    d = np.load(args.params)
    alpha, plateau = d["alpha"], float(d["plateau"])
    paths = list_images(args.input)
    os.makedirs(args.output, exist_ok=True)
    for p in tqdm(paths, desc="Removing"):
        img = cv2.imread(str(p))
        if img is None:
            continue
        cv2.imwrite(os.path.join(args.output, p.stem + ".png"),
                    remove(img, alpha, plateau, edge_inpaint=not args.no_inpaint))
    print(f"done -> {args.output}  ({len(paths)} images)")


def main():
    ap = argparse.ArgumentParser(description="SAMPLE watermark removal v2 (statistical per-pixel alpha)")
    sub = ap.add_subparsers(dest="cmd", required=True)

    e = sub.add_parser("estimate", help="Estimate per-pixel alpha from the whole set")
    e.add_argument("--input", required=True)
    e.add_argument("--output", default="wm_v2.npz")
    e.add_argument("--limit", type=int, default=600, help="max images used (evenly sampled)")
    e.add_argument("--no-calib", action="store_true", help="skip k/g grid search, use defaults")

    p = sub.add_parser("preview", help="Preview on one card")
    p.add_argument("--input", required=True)
    p.add_argument("--params", default="wm_v2.npz")
    p.add_argument("--output", default=None)
    p.add_argument("--no-inpaint", action="store_true", help="skip the 1px edge inpaint")

    r = sub.add_parser("remove", help="Clean a whole directory")
    r.add_argument("--input", required=True)
    r.add_argument("--output", required=True)
    r.add_argument("--params", default="wm_v2.npz")
    r.add_argument("--no-inpaint", action="store_true", help="skip the 1px edge inpaint")

    args = ap.parse_args()
    {"estimate": cmd_estimate, "preview": cmd_preview, "remove": cmd_remove}[args.cmd](args)


if __name__ == "__main__":
    main()
