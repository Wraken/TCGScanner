"""Dump ultralytics YOLO predictions for the detection validation set.

The Go implementation reimplements letterboxing, output decoding and the
coordinate transform back to image space. This fixture pins what Python
produces so the Go test (TestYOLOParity) can assert parity instead of guessing.

Two details make the comparison apples-to-apples:

* It runs the exported ONNX, not best.pt. ultralytics letterboxes a .pt to the
  smallest stride-aligned rectangle, but the ONNX has a fixed 640x640 input so
  it gets a square letterbox - the one the Go code applies. Same weights,
  different padding: on a 4590x8160 photo best.pt scores 0.498, the ONNX 0.406.

* It feeds both sides identical pixels. Go's image/jpeg and libjpeg (cv2)
  decode the same JPEG differently (chroma upsampling, IDCT rounding): mean
  pixel error is a few levels, peaks above 70, and on a 224x224 image upscaled
  to 640 that alone moves YOLO's confidence by 0.1. So every validation image is
  decoded once here with cv2 and written as lossless PNG; the fixture refers to
  those PNGs and the Go test reads them. What remains between the two sides is
  resampling and float summation order - about 0.005 of confidence.

    python dump_yolo_reference.py
"""
import argparse
import json
import os

import cv2
from ultralytics import YOLO


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--yolo", default="models/tcg/yolo.onnx")
    ap.add_argument("--images", default="datasets/card_detection/images/val")
    ap.add_argument("--png-dir", default="datasets/card_detection/images/val_png",
                    help="where cv2-decoded lossless copies are written")
    ap.add_argument("--conf", type=float, default=0.4)
    ap.add_argument("--out", default="../../testdata/yolo_python_boxes.json")
    args = ap.parse_args()

    model = YOLO(args.yolo, task="detect")
    os.makedirs(args.png_dir, exist_ok=True)

    result = {
        "model": os.path.basename(args.yolo),
        "conf": args.conf,
        # Relative to the detection dataset root, mirrored in the Go test.
        "images_dir": "images/" + os.path.basename(args.png_dir),
        "images": {},
    }

    for name in sorted(os.listdir(args.images)):
        if not name.lower().endswith((".jpg", ".jpeg", ".png")):
            continue
        img = cv2.imread(os.path.join(args.images, name))
        if img is None:
            continue
        h, w = img.shape[:2]

        png_name = os.path.splitext(name)[0] + ".png"
        cv2.imwrite(os.path.join(args.png_dir, png_name), img)

        det = model(img, conf=args.conf, verbose=False)[0]
        best = None
        for b in det.boxes:
            c = float(b.conf[0])
            if best is None or c > best["conf"]:
                x1, y1, x2, y2 = (float(v) for v in b.xyxy[0])
                best = {"conf": c, "box": [x1, y1, x2, y2]}

        result["images"][png_name] = {
            "width": w,
            "height": h,
            "detected": best is not None,
            "conf": best["conf"] if best else 0.0,
            "box": best["box"] if best else None,
        }
        print(f"{png_name[:46]:48s} {'det' if best else 'MISS'} conf={result['images'][png_name]['conf']:.3f}")

    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w") as f:
        json.dump(result, f, indent=2)
    print(f"\nwrote {args.out} ({len(result['images'])} images, pixels in {args.png_dir})")


if __name__ == "__main__":
    main()
