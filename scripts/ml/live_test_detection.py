"""
Test en direct : YOLO detection + Keras Embedding + FAISS search.
Détecte les cartes via webcam, crop, embed, et affiche le résultat FAISS.

Usage:
    python live_test_detection.py
    python live_test_detection.py --camera 1
    python live_test_detection.py --yolo runs/detect/train2/weights/best.pt
"""

import argparse
import glob
import json
import os
import threading
import time
import numpy as np
import cv2
import faiss

os.environ["TF_CPP_MIN_LOG_LEVEL"] = "2"
os.environ["TF_XLA_FLAGS"] = "--tf_xla_auto_jit=0"
os.environ["TF_ENABLE_ONEDNN_OPTS"] = "0"
import keras
keras.mixed_precision.set_global_policy("float32")

from ultralytics import YOLO

# ─── Args ──────────────────────────────────────────────────────────────────────

parser = argparse.ArgumentParser(description="Live card detection + recognition")
parser.add_argument("--yolo",      default="runs/detect/train2/weights/best.pt",
                    help="YOLO detection model")
parser.add_argument("--embedding", default="models/tcg/embedding_model.keras",
                    help="Keras embedding model")
parser.add_argument("--index",     default="models/tcg/index.faiss",
                    help="FAISS index file")
parser.add_argument("--labels",    default="models/tcg/index_labels.json",
                    help="Index labels JSON file")
parser.add_argument("--camera",    type=int, default=0)
parser.add_argument("--conf",      type=float, default=0.4,
                    help="YOLO detection confidence threshold")
parser.add_argument("--threshold", type=float, default=0.6,
                    help="FAISS similarity threshold for a valid match")
args = parser.parse_args()

# ─── Load models ───────────────────────────────────────────────────────────────

print("Chargement des modèles...")
yolo = YOLO(args.yolo)

embed_model = keras.models.load_model(args.embedding, compile=False)
img_size = embed_model.input_shape[1]  # 224

index = faiss.read_index(args.index)
with open(args.labels, "r") as f:
    labels = json.load(f)

print(f"  YOLO        : {args.yolo}")
print(f"  Embedding   : {args.embedding} ({img_size}x{img_size} → {embed_model.output_shape})")
print(f"  Index FAISS : {len(labels)} cartes")
print(f"  Threshold   : {args.threshold}")
print()
print("Appuie sur 'q' pour quitter, 's' pour screenshot, 'd' pour debug")

# ─── Preprocessing ─────────────────────────────────────────────────────────────

from train import BACKBONES
import json as _json
with open("models/tcg/config.json") as _f:
    _cfg = _json.load(_f)
preprocess_fn = BACKBONES[_cfg["backbone"]]["preprocess"]

def preprocess_card(crop):
    resized = cv2.resize(crop, (img_size, img_size))
    rgb = cv2.cvtColor(resized, cv2.COLOR_BGR2RGB)
    tensor = np.expand_dims(rgb.astype(np.float32), axis=0)
    return preprocess_fn(tensor)

def search_card(crop):
    tensor = preprocess_card(crop)
    embedding = np.ascontiguousarray(embed_model(tensor, training=False).numpy(), dtype=np.float32)
    faiss.normalize_L2(embedding)
    scores, indices = index.search(embedding, k=1)
    score = float(scores[0][0])
    card_id = labels[int(indices[0][0])]
    print(f"  FAISS → {card_id}  score={score:.3f}")
    return card_id, score

# ─── Embedding thread ──────────────────────────────────────────────────────────
# YOLO runs every frame (fast). Embedding+FAISS runs in a background thread
# only when YOLO finds a card and the thread is idle.

_lock = threading.Lock()
_pending_crop = None        # crop to embed, set by main loop
_cached_label = None        # (card_id, score) from last embedding
_embed_busy = False

def _embed_loop():
    global _pending_crop, _cached_label, _embed_busy
    while True:
        with _lock:
            crop = _pending_crop
            _pending_crop = None
        if crop is None:
            time.sleep(0.005)
            continue
        _embed_busy = True
        card_id, score = search_card(crop)
        with _lock:
            _cached_label = (card_id, score)
            _embed_busy = False

threading.Thread(target=_embed_loop, daemon=True).start()

# ─── Main loop ─────────────────────────────────────────────────────────────────

cap = cv2.VideoCapture(args.camera)
cached_label = None  # last known (card_id, score)

while True:
    ret, frame = cap.read()
    if not ret:
        break

    # YOLO every frame — it's fast
    detections = yolo(frame, conf=args.conf, verbose=False)[0]

    with _lock:
        if _cached_label is not None:
            cached_label = _cached_label
            _cached_label = None

    for box in detections.boxes:
        x1, y1, x2, y2 = map(int, box.xyxy[0])
        crop = frame[y1:y2, x1:x2]
        if crop.size == 0:
            continue

        # Send crop to embedding thread if idle
        with _lock:
            if not _embed_busy and _pending_crop is None:
                _pending_crop = crop.copy()

        # Draw box with last known label
        if cached_label is not None:
            card_id, score = cached_label
            color = (0, 255, 0) if score >= args.threshold else (0, 165, 255)
            label = f"{card_id} ({score:.2f})" if score >= args.threshold else f"? ({score:.2f})"
        else:
            color = (200, 200, 200)
            label = "..."

        cv2.rectangle(frame, (x1, y1), (x2, y2), color, 2)
        (tw, th), _ = cv2.getTextSize(label, cv2.FONT_HERSHEY_SIMPLEX, 0.6, 2)
        cv2.rectangle(frame, (x1, y1 - th - 10), (x1 + tw + 4, y1), color, -1)
        cv2.putText(frame, label, (x1 + 2, y1 - 6),
                    cv2.FONT_HERSHEY_SIMPLEX, 0.6, (0, 0, 0), 2)

    n = len(detections.boxes)
    status = "embedding..." if _embed_busy else "ready"
    cv2.putText(frame, f"{n} card(s) [{status}]", (10, 30),
                cv2.FONT_HERSHEY_SIMPLEX, 0.8, (0, 200, 255), 2)

    cv2.imshow("TCGScanner — live test", frame)
    key = cv2.waitKey(1) & 0xFF
    if key == ord('q'):
        break
    if key == ord('s'):
        cv2.imwrite("screenshot.jpg", frame)
        print("Screenshot saved: screenshot.jpg")
    if key == ord('d'):
        ref_images = sorted(glob.glob("images/*/*.*"))  # one sub-dir per game
        if ref_images:
            ref_path = ref_images[0]
            ref_img = cv2.imread(ref_path)
            ref_name = os.path.basename(ref_path).rsplit(".", 1)[0]
            card_id, score = search_card(ref_img)
            print(f"\n  DEBUG: ref={ref_name} → FAISS={card_id}  score={score:.3f}")
            print(f"  {'✓ MATCH' if ref_name == card_id else '✗ MISMATCH'}\n")

cap.release()
cv2.destroyAllWindows()
