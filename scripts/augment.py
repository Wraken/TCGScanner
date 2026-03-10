"""
Card Data Augmentation
Composites cards onto random backgrounds with transformations.

Usage:
    pip install albumentations opencv-python-headless numpy tqdm
    python augment.py --name riftbound
    python augment.py --name riftbound --per-card 100 --backgrounds backgrounds/

Directory layout:
    images/riftbound/           ← input card PNGs
    datasets/riftbound/         ← output augmented dataset
        ogn-001-298/
            ogn-001-298_000.jpg   (original, resized)
            ogn-001-298_001.jpg   (augmented on background)
            ...
"""

import argparse
import os
import random
import multiprocessing as mp
from functools import partial

import cv2
import numpy as np
import albumentations as A
from pathlib import Path
from tqdm import tqdm


# Output size matches model input
OUTPUT_SIZE = 224


def generate_solid_bg(size):
    """Random solid color background."""
    color = np.random.randint(0, 256, 3, dtype=np.uint8)
    bg = np.full((size, size, 3), color, dtype=np.uint8)
    return bg


def generate_gradient_bg(size):
    """Random gradient background (horizontal, vertical, or diagonal)."""
    c1 = np.random.randint(0, 256, 3).astype(np.float32)
    c2 = np.random.randint(0, 256, 3).astype(np.float32)

    bg = np.zeros((size, size, 3), dtype=np.float32)
    direction = random.choice(["horizontal", "vertical", "diagonal"])

    for i in range(size):
        t = i / max(size - 1, 1)
        if direction == "horizontal":
            bg[:, i] = c1 * (1 - t) + c2 * t
        elif direction == "vertical":
            bg[i, :] = c1 * (1 - t) + c2 * t
        else:
            for j in range(size):
                t2 = (i + j) / max(2 * (size - 1), 1)
                bg[i, j] = c1 * (1 - t2) + c2 * t2

    return bg.astype(np.uint8)


def generate_noise_bg(size):
    """Random noise background (gaussian or perlin-like)."""
    bg = np.random.randint(0, 256, (size, size, 3), dtype=np.uint8)
    # Blur to make it look more like a surface texture
    blur_k = random.choice([15, 25, 35, 45])
    bg = cv2.GaussianBlur(bg, (blur_k, blur_k), 0)
    return bg


def generate_patches_bg(size):
    """Random colored patches background (simulates cluttered surface)."""
    bg = np.zeros((size, size, 3), dtype=np.uint8)
    n_patches = random.randint(3, 8)
    for _ in range(n_patches):
        color = np.random.randint(0, 256, 3).tolist()
        x1 = random.randint(0, size - 1)
        y1 = random.randint(0, size - 1)
        x2 = random.randint(x1, size)
        y2 = random.randint(y1, size)
        cv2.rectangle(bg, (x1, y1), (x2, y2), color, -1)
    bg = cv2.GaussianBlur(bg, (21, 21), 0)
    return bg


def get_random_background(size, real_backgrounds=None):
    """Pick a random background: generated or from photos."""
    generators = [generate_solid_bg, generate_gradient_bg, generate_noise_bg, generate_patches_bg]

    # 50/50 real vs generated if real backgrounds available
    if real_backgrounds and random.random() < 0.5:
        bg_path = random.choice(real_backgrounds)
        bg = cv2.imread(str(bg_path))
        if bg is not None:
            bg = cv2.cvtColor(bg, cv2.COLOR_BGR2RGB)
            # Random crop to square
            h, w = bg.shape[:2]
            min_dim = min(h, w)
            x = random.randint(0, w - min_dim)
            y = random.randint(0, h - min_dim)
            bg = bg[y:y+min_dim, x:x+min_dim]
            bg = cv2.resize(bg, (size, size))
            return bg

    return random.choice(generators)(size)


def composite_card_on_bg(card_img, bg, scale_range=(0.5, 0.80)):
    """
    Place card on background with random scale and position.
    Card is always fully visible (50-80% of the image).
    """
    bg_size = bg.shape[0]
    card_h, card_w = card_img.shape[:2]

    # Random scale
    scale = random.uniform(*scale_range)
    card_ratio = card_w / card_h  # ~0.716

    # Fit card in bg with scale
    new_h = int(bg_size * scale)
    new_w = int(new_h * card_ratio)

    if new_w > bg_size * scale:
        new_w = int(bg_size * scale)
        new_h = int(new_w / card_ratio)

    resized_card = cv2.resize(card_img, (new_w, new_h), interpolation=cv2.INTER_AREA)

    # Random position, always fully within bounds
    max_x = bg_size - new_w
    max_y = bg_size - new_h
    x = random.randint(0, max(0, max_x))
    y = random.randint(0, max(0, max_y))

    # Composite
    result = bg.copy()
    result[y:y+new_h, x:x+new_w] = resized_card

    return result


def get_card_transform():
    """Augmentations applied to the card BEFORE compositing."""
    return A.Compose([
        A.Perspective(scale=(0.01, 0.05), p=0.5),
        A.Rotate(limit=10, border_mode=cv2.BORDER_CONSTANT, fill=0, p=0.5),
    ])


def get_scene_transform():
    """Augmentations applied to the FULL scene (card + background)."""
    return A.Compose([
        # Blur (camera)
        A.OneOf([
            A.MotionBlur(blur_limit=5),
            A.GaussianBlur(blur_limit=3),
        ], p=0.3),

        # Lighting
        A.RandomBrightnessContrast(
            brightness_limit=0.3,
            contrast_limit=0.3,
            p=0.8,
        ),
        A.HueSaturationValue(
            hue_shift_limit=10,
            sat_shift_limit=20,
            val_shift_limit=10,
            p=0.5,
        ),
        A.RandomShadow(p=0.3),

        # Noise & compression
        A.GaussNoise(std_range=(0.01, 0.05), p=0.3),
        A.ImageCompression(quality_range=(60, 100), p=0.5),

        # Occlusion (finger, sleeve)
        A.CoarseDropout(
            num_holes_range=(1, 2),
            hole_height_range=(20, 60),
            hole_width_range=(20, 60),
            fill=0,
            p=0.2,
        ),

        # Final resize
        A.Resize(OUTPUT_SIZE, OUTPUT_SIZE),
    ])


def augment_card_worker(args):
    """Worker function for multiprocessing. Takes a tuple of arguments."""
    image_path, output_dir, card_id, n_variations, n_val, real_backgrounds = args

    # Seed RNG per worker to avoid identical augmentations when using fork
    seed = os.getpid() + hash(card_id)
    random.seed(seed)
    np.random.seed(seed % (2**32))

    card_transform = get_card_transform()
    scene_transform = get_scene_transform()

    return augment_card(
        image_path, output_dir, card_id, n_variations, n_val,
        card_transform, scene_transform, real_backgrounds,
    )


def augment_card(image_path, output_dir, card_id, n_variations, n_val,
                 card_transform, scene_transform, real_backgrounds):
    img = cv2.imread(str(image_path))
    if img is None:
        print(f"  ✗ Could not read {image_path}")
        return 0

    img = cv2.cvtColor(img, cv2.COLOR_BGR2RGB)

    train_dir = os.path.join(output_dir, "train", card_id)
    val_dir = os.path.join(output_dir, "val", card_id)
    os.makedirs(train_dir, exist_ok=True)
    os.makedirs(val_dir, exist_ok=True)

    # Original card (resized, no augmentation) goes to val
    original = cv2.resize(img, (OUTPUT_SIZE, OUTPUT_SIZE))
    original_bgr = cv2.cvtColor(original, cv2.COLOR_RGB2BGR)
    cv2.imwrite(os.path.join(val_dir, f"{card_id}_000.jpg"), original_bgr)

    # Generate all augmented variations
    # Last n_val go to val, the rest go to train
    count = 0
    for i in range(1, n_variations + 1):

        # 1. Transform the card (perspective, rotation)
        card_aug = card_transform(image=img)["image"]

        # 2. Generate/pick a background
        bg = get_random_background(OUTPUT_SIZE * 3, real_backgrounds)

        # 3. Composite card on background
        scene = composite_card_on_bg(card_aug, bg)

        # 4. Apply scene-level augmentations
        result = scene_transform(image=scene)["image"]

        # Save to val for last n_val variations, train for the rest
        result_bgr = cv2.cvtColor(result, cv2.COLOR_RGB2BGR)
        is_val = i > n_variations - n_val
        out_dir = val_dir if is_val else train_dir
        out_path = os.path.join(out_dir, f"{card_id}_{i:03d}.jpg")
        cv2.imwrite(out_path, result_bgr, [cv2.IMWRITE_JPEG_QUALITY, 95])
        count += 1

    return count


def load_real_backgrounds(bg_dir):
    """Load background image paths if directory exists."""
    if not bg_dir or not os.path.isdir(bg_dir):
        return []

    exts = {".jpg", ".jpeg", ".png", ".bmp", ".webp"}
    paths = [p for p in Path(bg_dir).iterdir() if p.suffix.lower() in exts]
    if paths:
        print(f"Loaded {len(paths)} real background images from {bg_dir}/")
    return paths


def main():
    parser = argparse.ArgumentParser(description="Augment card images for model training")
    parser.add_argument("--name", required=True, help="Model name (e.g. riftbound)")
    parser.add_argument("--per-card", type=int, default=50, help="Variations per card")
    parser.add_argument("--val-per-card", type=int, default=10, help="Validation variations per card (default: 10)")
    parser.add_argument("--backgrounds", default="backgrounds", help="Real background photos dir")
    parser.add_argument("--workers", type=int, default=0, help="Parallel workers (0 = auto)")
    args = parser.parse_args()

    input_dir = Path("images") / args.name
    output_dir = Path("datasets") / args.name

    if not input_dir.is_dir():
        print(f"Input directory not found: {input_dir}")
        return

    # Find all card images
    images = sorted(
        list(input_dir.glob("*.png")) +
        list(input_dir.glob("*.webp")) +
        list(input_dir.glob("*.jpg"))
    )

    if not images:
        print(f"No images found in {input_dir}")
        return

    real_backgrounds = load_real_backgrounds(args.backgrounds)

    print(f"Model: {args.name}")
    print(f"Input: {input_dir}/")
    print(f"Output: {output_dir}/")
    n_val = min(args.val_per_card, args.per_card)
    print(f"Found {len(images)} cards")
    print(f"Generating {args.per_card} variations each ({args.per_card - n_val} train, {n_val} val + 1 original in val)")
    print(f"Total images: ~{len(images) * (args.per_card + 1)}")
    if not real_backgrounds:
        print(f"No backgrounds/ folder found — using generated backgrounds only")
        print(f"  Tip: add photos to backgrounds/ for more realistic results")
    print()

    # Build worker args
    worker_args = [
        (str(img_path), str(output_dir), img_path.stem, args.per_card, n_val, real_backgrounds)
        for img_path in images
    ]

    n_workers = args.workers if args.workers > 0 else mp.cpu_count()
    print(f"Using {n_workers} workers")

    total = 0
    with mp.Pool(n_workers) as pool:
        results = pool.imap_unordered(augment_card_worker, worker_args)
        for count in tqdm(results, total=len(images), desc="Processing cards"):
            total += count + 1

    print(f"\nDone! {total} images generated in {output_dir}/")


if __name__ == "__main__":
    main()
