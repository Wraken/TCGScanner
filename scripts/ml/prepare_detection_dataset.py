"""
Prépare le dataset Label Studio exporté (YOLO format) pour l'entraînement YOLOv8.
Crée la structure train/val attendue par Ultralytics et génère data.yaml.

Usage:
    python prepare_detection_dataset.py
"""

import os
import random
import shutil
from pathlib import Path

SRC = Path("datasets/project-1-at-2026-03-15-22-25-ff94196b")
DST = Path("datasets/card_detection")
VAL_RATIO = 0.2
SEED = 42

random.seed(SEED)

# Collecte toutes les images qui ont un label correspondant
images = sorted([
    p for p in (SRC / "images").iterdir()
    if p.suffix.lower() in {".jpg", ".jpeg", ".png", ".webp"}
    and (SRC / "labels" / (p.stem + ".txt")).exists()
])

random.shuffle(images)
n_val = max(1, int(len(images) * VAL_RATIO))
val_imgs   = images[:n_val]
train_imgs = images[n_val:]

print(f"Total : {len(images)} images  →  train={len(train_imgs)}, val={len(val_imgs)}")

for split, split_imgs in [("train", train_imgs), ("val", val_imgs)]:
    (DST / "images" / split).mkdir(parents=True, exist_ok=True)
    (DST / "labels" / split).mkdir(parents=True, exist_ok=True)
    for img in split_imgs:
        shutil.copy(img, DST / "images" / split / img.name)
        lbl = SRC / "labels" / (img.stem + ".txt")
        shutil.copy(lbl, DST / "labels" / split / lbl.name)

# data.yaml
yaml_path = DST / "data.yaml"
yaml_path.write_text(f"""path: {DST.resolve()}
train: images/train
val:   images/val

nc: 1
names: ['cards']
""")

print(f"✓ Dataset prêt : {DST}")
print(f"✓ data.yaml    : {yaml_path}")
print()
print("Lancer l'entraînement :")
print(f"  yolo train model=yolov8n.pt data={yaml_path.resolve()} epochs=50 imgsz=640 patience=10")
