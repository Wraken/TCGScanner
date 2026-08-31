"""
Card Data Augmentation
Composites cards onto random backgrounds with transformations.

Usage:
    pip install albumentations opencv-python-headless numpy tqdm
    python augment.py --input images/riftbound
    python augment.py --input images/riftbound --backbone efficientnet-b3
    python augment.py --input images/riftbound --backbone convnext-tiny
    python augment.py --input images/riftbound --per-card 100 --backgrounds backgrounds/
    python augment.py --input images/riftbound --output datasets/riftbound-b3 --backbone efficientnet-b3
    python augment.py --input images/onepiece images/riftbound --output datasets/tcg   # multi-jeux

Directory layout:
    images/riftbound/           <- input card PNGs
    datasets/riftbound/         <- output augmented dataset
        train/
            ogn-001-298/
                ogn-001-298_001.jpg
                ...
        val/
            ogn-001-298/
                ogn-001-298_000.jpg  (original, non augmentée)
                ...
"""

import argparse
import os
import random
import multiprocessing as mp

import cv2
import numpy as np
import albumentations as A
from pathlib import Path
from tqdm import tqdm


BACKBONE_IMG_SIZES = {
    "mobilenetv2":        224,
    "efficientnet-b0":    224,
    "efficientnet-b3":    300,
    "convnext-tiny":      224,
    "efficientnet-v2-s":  384,
}


def generate_solid_bg(size):
    """Fond uni couleur aléatoire."""
    color = np.random.randint(0, 256, 3, dtype=np.uint8)
    return np.full((size, size, 3), color, dtype=np.uint8)


def generate_gradient_bg(size):
    """Fond dégradé aléatoire (horizontal, vertical ou diagonal)."""
    c1 = np.random.randint(0, 256, 3).astype(np.float32)
    c2 = np.random.randint(0, 256, 3).astype(np.float32)
    direction = random.choice(["horizontal", "vertical", "diagonal"])
    t = np.linspace(0, 1, size, dtype=np.float32)

    if direction == "horizontal":
        row = c1 * (1 - t)[:, np.newaxis] + c2 * t[:, np.newaxis]  # (size, 3)
        bg = np.broadcast_to(row[np.newaxis, :, :], (size, size, 3))
    elif direction == "vertical":
        col = c1 * (1 - t)[:, np.newaxis] + c2 * t[:, np.newaxis]  # (size, 3)
        bg = np.broadcast_to(col[:, np.newaxis, :], (size, size, 3))
    else:
        t2 = np.add.outer(t, t) / 2
        bg = c1 * (1 - t2[:, :, np.newaxis]) + c2 * t2[:, :, np.newaxis]

    return bg.astype(np.uint8)


def generate_noise_bg(size):
    """Fond bruit gaussien flouté (simule une surface texturée)."""
    bg = np.random.randint(0, 256, (size, size, 3), dtype=np.uint8)
    blur_k = random.choice([15, 25, 35, 45])
    return cv2.GaussianBlur(bg, (blur_k, blur_k), 0)


def generate_patches_bg(size):
    """Fond avec rectangles de couleurs aléatoires (surface encombrée)."""
    bg = np.zeros((size, size, 3), dtype=np.uint8)
    for _ in range(random.randint(3, 8)):
        color = np.random.randint(0, 256, 3).tolist()
        x1, y1 = random.randint(0, size - 1), random.randint(0, size - 1)
        x2, y2 = random.randint(x1, size), random.randint(y1, size)
        cv2.rectangle(bg, (x1, y1), (x2, y2), color, -1)
    return cv2.GaussianBlur(bg, (21, 21), 0)


def get_random_background(size, real_backgrounds=None):
    """Choisit un fond aléatoire : généré ou photo réelle (50/50)."""
    generators = [generate_solid_bg, generate_gradient_bg, generate_noise_bg, generate_patches_bg]

    if real_backgrounds and random.random() < 0.5:
        bg_path = random.choice(real_backgrounds)
        bg = cv2.imread(str(bg_path))
        if bg is not None:
            bg = cv2.cvtColor(bg, cv2.COLOR_BGR2RGB)
            h, w = bg.shape[:2]
            min_dim = min(h, w)
            x = random.randint(0, w - min_dim)
            y = random.randint(0, h - min_dim)
            bg = bg[y:y + min_dim, x:x + min_dim]
            return cv2.resize(bg, (size, size))

    return random.choice(generators)(size)


def composite_card_on_bg(card_img, bg, scale_range=(0.40, 0.90)):
    """
    Place la carte sur le fond avec une taille et position aléatoires.
    """
    bg_size = bg.shape[0]
    card_h, card_w = card_img.shape[:2]

    scale = random.uniform(*scale_range)
    card_ratio = card_w / card_h

    new_h = int(bg_size * scale)
    new_w = int(new_h * card_ratio)

    if new_w > bg_size * scale:
        new_w = int(bg_size * scale)
        new_h = int(new_w / card_ratio)

    resized_card = cv2.resize(card_img, (new_w, new_h), interpolation=cv2.INTER_AREA)

    max_x = bg_size - new_w
    max_y = bg_size - new_h
    x = random.randint(0, max(0, max_x))
    y = random.randint(0, max(0, max_y))

    x = min(x, bg_size - new_w)
    y = min(y, bg_size - new_h)

    result = bg.copy()
    result[y:y + new_h, x:x + new_w] = resized_card
    return result


def get_card_transform():
    """
    Transformations appliquées à la CARTE avant composition.
    """
    return A.Compose([
        A.Perspective(scale=(0.05, 0.12), p=0.5),   # était (0.01, 0.05)
    ])


def get_scene_transform(output_size):
    """
    Transformations appliquées à la SCÈNE complète (carte + fond).
    """
    return A.Compose([
        A.Rotate(
            limit=30,
            border_mode=cv2.BORDER_WRAP,
            p=0.6,
        ),

        # Flou caméra
        A.OneOf([
            A.MotionBlur(blur_limit=5),
            A.GaussianBlur(blur_limit=3),
        ], p=0.3),

        # Éclairage
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

        # Bruit & compression JPEG
        A.GaussNoise(std_range=(0.01, 0.05), p=0.3),
        A.ImageCompression(quality_range=(60, 100), p=0.5),

        # Occlusion partielle (doigt, pochette)
        A.CoarseDropout(
            num_holes_range=(1, 2),
            hole_height_range=(20, 60),
            hole_width_range=(20, 60),
            fill=128,          # était fill=0 (noir) → gris neutre
            p=0.2,
        ),

        # Resize final
        A.Resize(output_size, output_size),
    ])


def get_card_only_transform(output_size):
    """
    Transformations appliquées directement à la carte (sans fond).
    Simule ce que YOLO crop + webcam produit en conditions réelles.
    La carte reste toujours entièrement visible (pas de coupure sur les bords).
    """
    return A.Compose([
        # Resize initial
        A.Resize(output_size, output_size),

        # Rotation légère (simule carte pas parfaitement droite dans le crop YOLO)
        # fit_output=True agrandit le canvas pour garder la carte entière
        A.Affine(
            rotate=(-10, 10),
            fit_output=True,
            border_mode=cv2.BORDER_CONSTANT,
            fill=random.choice([(0, 0, 0), (128, 128, 128), (200, 200, 200)]),
            p=0.5,
        ),

        # Resize final — remet à output_size (carte légèrement dézoomée si rotation)
        A.Resize(output_size, output_size),

        # Flou caméra
        A.OneOf([
            A.MotionBlur(blur_limit=5),
            A.GaussianBlur(blur_limit=3),
        ], p=0.3),

        # Éclairage
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

        # Bruit & compression JPEG
        A.GaussNoise(std_range=(0.01, 0.05), p=0.3),
        A.ImageCompression(quality_range=(60, 100), p=0.5),

        # Occlusion partielle (doigt, pochette)
        A.CoarseDropout(
            num_holes_range=(1, 2),
            hole_height_range=(20, 60),
            hole_width_range=(20, 60),
            fill=128,
            p=0.2,
        ),
    ])


def augment_card_worker(args):
    """Worker multiprocessing. Reçoit un tuple d'arguments."""
    image_path, output_dir, card_id, n_variations, n_val, real_backgrounds, output_size, no_bg = args

    seed = os.getpid() + hash(card_id)
    random.seed(seed)
    np.random.seed(seed % (2 ** 32))

    if no_bg:
        transform = get_card_only_transform(output_size)
    else:
        transform = None  # use legacy pipeline

    return augment_card(
        image_path, output_dir, card_id, n_variations, n_val,
        real_backgrounds, output_size, no_bg, transform,
    )


def augment_card(image_path, output_dir, card_id, n_variations, n_val,
                 real_backgrounds, output_size, no_bg, transform):
    img = cv2.imread(str(image_path))
    if img is None:
        print(f"  ✗ Impossible de lire {image_path}")
        return 0

    img = cv2.cvtColor(img, cv2.COLOR_BGR2RGB)

    train_dir = os.path.join(output_dir, "train", card_id)
    val_dir = os.path.join(output_dir, "val", card_id)
    os.makedirs(train_dir, exist_ok=True)
    os.makedirs(val_dir, exist_ok=True)

    # Image originale (redimensionnée, sans augmentation) → val
    original = cv2.resize(img, (output_size, output_size))
    cv2.imwrite(
        os.path.join(val_dir, f"{card_id}_000.jpg"),
        cv2.cvtColor(original, cv2.COLOR_RGB2BGR),
    )

    count = 0
    for i in range(1, n_variations + 1):
        if no_bg:
            # Mode carte seule : augmentations directement sur la carte
            result = transform(image=img)["image"]
        else:
            # Mode legacy : carte composée sur un fond aléatoire
            card_transform = get_card_transform()
            scene_transform = get_scene_transform(output_size)
            card_aug = card_transform(image=img)["image"]
            bg = get_random_background(output_size * 3, real_backgrounds)
            scene = composite_card_on_bg(card_aug, bg)
            result = scene_transform(image=scene)["image"]

        result_bgr = cv2.cvtColor(result, cv2.COLOR_RGB2BGR)
        is_val = i > n_variations - n_val
        out_dir = val_dir if is_val else train_dir
        cv2.imwrite(
            os.path.join(out_dir, f"{card_id}_{i:03d}.jpg"),
            result_bgr,
            [cv2.IMWRITE_JPEG_QUALITY, 95],
        )
        count += 1

    return count


def load_real_backgrounds(bg_dir):
    """Charge les chemins d'images de fond réelles si le dossier existe."""
    if not bg_dir or not os.path.isdir(bg_dir):
        return []
    exts = {".jpg", ".jpeg", ".png", ".bmp", ".webp"}
    paths = [p for p in Path(bg_dir).iterdir() if p.suffix.lower() in exts]
    if paths:
        print(f"  {len(paths)} images de fond réelles chargées depuis {bg_dir}/")
    return paths


def main():
    parser = argparse.ArgumentParser(description="Augmentation d'images de cartes pour l'entraînement")
    parser.add_argument("--input", required=True, nargs="+",
                        help="Dossier(s) d'images source, un par jeu (ex: images/onepiece images/riftbound)")
    parser.add_argument("--output", default=None,
                        help="Dossier dataset de sortie (défaut: datasets/<nom du dossier d'entrée> ; "
                             "obligatoire avec plusieurs --input)")
    parser.add_argument(
        "--backbone",
        default="convnext-tiny",
        choices=list(BACKBONE_IMG_SIZES.keys()),
        help="Backbone cible (définit la taille des images, défaut: convnext-tiny)",
    )
    parser.add_argument("--per-card", type=int, default=50, help="Variations par carte (défaut: 50)")
    parser.add_argument("--val-per-card", type=int, default=10, help="Variations de validation par carte (défaut: 10)")
    parser.add_argument("--backgrounds", default="backgrounds", help="Dossier de photos de fond réelles")
    parser.add_argument("--no-bg", action="store_true", default=True,
                        help="Pas de fond : augmente la carte directement (défaut: activé)")
    parser.add_argument("--with-bg", action="store_true",
                        help="Activer le mode fond (composite carte sur fond aléatoire)")
    parser.add_argument("--workers", type=int, default=0, help="Workers parallèles (0 = auto)")
    args = parser.parse_args()

    output_size = BACKBONE_IMG_SIZES[args.backbone]

    input_dirs = [Path(d) for d in args.input]
    if args.output:
        output_dir = Path(args.output)
    elif len(input_dirs) == 1:
        output_dir = Path("datasets") / input_dirs[0].name
    else:
        parser.error("--output est obligatoire avec plusieurs --input "
                     "(ex: --output datasets/tcg)")

    for d in input_dirs:
        if not d.is_dir():
            print(f"Dossier d'entrée introuvable : {d}")
            return

    # Les sources sont rangées un dossier par jeu ; un dataset multi-jeux se
    # construit en les concaténant (les card_id sont uniques entre jeux).
    images = sorted(
        p for d in input_dirs
        for ext in ("*.png", "*.webp", "*.jpg")
        for p in d.glob(ext)
    )

    if not images:
        print(f"Aucune image trouvée dans {', '.join(map(str, input_dirs))}")
        return

    no_bg = not args.with_bg  # --no-bg is default, --with-bg overrides
    real_backgrounds = load_real_backgrounds(args.backgrounds) if not no_bg else []
    n_val = min(args.val_per_card, args.per_card)

    print(f"Backbone : {args.backbone} ({output_size}x{output_size})")
    print(f"Mode     : {'carte seule (no-bg)' if no_bg else 'composite sur fond'}")
    print(f"Entrée   : {', '.join(f'{d}/' for d in input_dirs)}")
    print(f"Sortie   : {output_dir}/")
    print(f"Cartes   : {len(images)}")
    print(f"Variations : {args.per_card} par carte ({args.per_card - n_val} train, {n_val} val + 1 original en val)")
    print(f"Total images : ~{len(images) * (args.per_card + 1)}")
    if not no_bg and not real_backgrounds:
        print("Astuce : ajoute des photos dans backgrounds/ pour des résultats plus réalistes")
    print()

    worker_args = [
        (str(img_path), str(output_dir), img_path.stem, args.per_card, n_val, real_backgrounds, output_size, no_bg)
        for img_path in images
    ]

    n_workers = args.workers if args.workers > 0 else mp.cpu_count()
    print(f"Workers : {n_workers}")

    total = 0
    with mp.Pool(n_workers) as pool:
        for count in tqdm(
            pool.imap_unordered(augment_card_worker, worker_args),
            total=len(images),
            desc="Traitement des cartes",
        ):
            total += count + 1

    print(f"\nTerminé ! {total} images générées dans {output_dir}/")


if __name__ == "__main__":
    main()
