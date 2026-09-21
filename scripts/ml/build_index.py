"""
Construction de l'index FAISS

Après l'entraînement, ce script :
    1. Charge le modèle d'embedding entraîné
    2. Passe chaque image de référence (une par carte) dans le modèle → vecteur 512D
    3. Construit un index FAISS pour la recherche de plus proche voisin
    4. Sauvegarde l'index + le mapping position → card ID

L'index FAISS est ce qu'on utilise pendant la reconnaissance :
    query_embedding = modèle(photo_utilisateur)
    top5 = index.search(query_embedding, k=5)
    → retourne les 5 cartes les plus similaires avec leur score de confiance

Usage :
    pip install faiss-cpu numpy tensorflow tqdm
    python build_index.py --name riftbound
    python build_index.py --name onepiece
    python build_index.py --name riftbound --references images/riftbound

Structure attendue :
    images/riftbound/         ← une image PNG par carte (le "catalogue officiel")
        ogn-001-298.png
        ogn-002-298.png
        ...
    OU
    datasets/riftbound/val/   ← fallback si images/ n'existe pas
        ogn-001-298/
            ogn-001-298_000.jpg   (l'original non augmenté)

Sortie dans models/<name>/ :
    index.faiss         → index FAISS (base vectorielle)
    index_labels.json   → liste ordonnée des card IDs (position FAISS → card ID)
"""

import argparse
import json
import os
from pathlib import Path

os.environ["TF_CPP_MIN_LOG_LEVEL"] = "2"
os.environ["TF_XLA_FLAGS"] = "--tf_xla_auto_jit=0"
os.environ["TF_ENABLE_ONEDNN_OPTS"] = "0"

import albumentations as A
import cv2
import numpy as np
from tensorflow import keras
keras.mixed_precision.set_global_policy("float32")
from tqdm import tqdm

SUPPORTED_EXTS = {".jpg", ".jpeg", ".png", ".webp"}


# ─── Chargement des images de référence ───────────────────────────────────────

def load_flat_references(ref_dir):
    """
    Structure plate : un fichier par carte.
    Ex : images/riftbound/ogn-001-298.png

    C'est le cas idéal : une seule image officielle haute qualité par carte.
    Retourne les chemins, pas les pixels : les images sont décodées une par une
    au moment de l'embedding (31 000 références à 384x384 en float32 feraient
    55 Go en mémoire).
    """
    images_paths = sorted([p for p in Path(ref_dir).iterdir() if p.suffix.lower() in SUPPORTED_EXTS])

    if not images_paths:
        return None, None

    print(f"Structure plate : {len(images_paths)} images de référence")
    return images_paths, [p.stem for p in images_paths]


def load_subdir_references(ref_dir):
    """
    Structure avec sous-dossiers (issue de augment.py).
    Ex : datasets/riftbound/val/ogn-001-298/ogn-001-298_000.jpg

    Prend la première image de chaque dossier = l'original non augmenté (_000).
    """
    subdirs = sorted([p for p in Path(ref_dir).iterdir() if p.is_dir()])

    if not subdirs:
        return None, None

    print(f"Structure sous-dossiers : {len(subdirs)} cartes")
    paths, card_ids = [], []

    for card_dir in subdirs:
        img_files = sorted([p for p in card_dir.iterdir() if p.suffix.lower() in SUPPORTED_EXTS])
        if not img_files:
            continue

        # Cherche en priorité l'image originale (_000) sinon prend la première
        paths.append(next((p for p in img_files if "_000" in p.stem), img_files[0]))
        card_ids.append(card_dir.name)

    return paths, card_ids


def load_image(path, img_size):
    """Décode une image de référence en (img_size, img_size, 3) float32."""
    img = keras.utils.load_img(str(path), target_size=(img_size, img_size))
    return keras.utils.img_to_array(img).astype(np.float32)


def default_reference_dirs(name):
    """
    Dossiers de références par défaut pour un modèle.

    images/<name>/ s'il existe (modèle mono-jeu), sinon chaque sous-dossier
    images/*/ : les scans de référence sont rangés un dossier par jeu
    (images/onepiece, images/riftbound, ...). Ne PAS passer images/ lui-même à
    load_reference_images : elle prendrait chaque sous-dossier pour une carte.
    """
    single = Path("images") / name
    if single.is_dir():
        return [single]
    root = Path("images")
    if not root.is_dir():
        return []
    return sorted(p for p in root.iterdir() if p.is_dir())


def load_reference_images(references_dir):
    """Détecte automatiquement la structure du dossier et liste les références (chemins, ids)."""
    # Essai structure plate
    paths, card_ids = load_flat_references(references_dir)
    if paths is not None:
        return paths, card_ids

    # Fallback structure sous-dossiers
    paths, card_ids = load_subdir_references(references_dir)
    if paths is not None:
        return paths, card_ids

    raise ValueError(f"Aucune image trouvée dans {references_dir}")


# ─── Calcul des embeddings ────────────────────────────────────────────────────

_ref_transform = A.Compose([
    A.RandomBrightnessContrast(brightness_limit=0.15, contrast_limit=0.15, p=0.8),
    A.GaussianBlur(blur_limit=3, p=0.3),
    A.HueSaturationValue(hue_shift_limit=5, sat_shift_limit=10, val_shift_limit=5, p=0.5),
    A.GaussNoise(std_range=(0.01, 0.03), p=0.3),
    A.Affine(rotate=(-5, 5), fit_output=True,
             border_mode=cv2.BORDER_CONSTANT, fill=128, p=0.5),
])

def compute_reference_embedding(model, img_array, preprocess_fn, n_aug=8):
    """
    Calcule l'embedding robuste d'une carte de référence
    en moyennant K versions légèrement augmentées.
    img_array : (H, W, 3) float32
    """
    h, w = img_array.shape[:2]
    resize = A.Resize(h, w)

    versions = [img_array]  # version originale incluse
    for _ in range(n_aug - 1):
        aug = _ref_transform(image=img_array.astype(np.uint8))["image"]
        aug = resize(image=aug)["image"]
        versions.append(aug.astype(np.float32))

    batch = preprocess_fn(np.stack(versions))
    embeddings = model.predict(batch, verbose=0)  # (n_aug, 512)

    # Moyenne puis renormalisation L2
    mean_emb = embeddings.mean(axis=0, keepdims=True)
    norm = np.linalg.norm(mean_emb, axis=1, keepdims=True)
    return (mean_emb / (norm + 1e-8)).astype(np.float32)

def compute_all_embeddings(model, paths, img_size, preprocess_fn):
    """
    Passe toutes les images de référence dans le modèle, décodées une par une.
    Retourne une matrice (N, embedding_dim) de float32.
    """
    all_embeddings = []

    for path in tqdm(paths, desc="Calcul des embeddings"):
        all_embeddings.append(
            compute_reference_embedding(model, load_image(path, img_size), preprocess_fn)
        )

    result = np.vstack(all_embeddings).astype(np.float32)
    print(f"Embeddings : {result.shape[0]} vecteurs × {result.shape[1]}D")
    return result


# ─── Construction de l'index FAISS ────────────────────────────────────────────

def build_faiss_index(embeddings):
    """
    Construit un index FAISS IndexFlatIP (Inner Product = produit scalaire).

    Pourquoi IndexFlatIP et pas IndexFlatL2 ?
    Parce que nos embeddings sont L2-normalisés (vecteurs unitaires sur une sphère).
    Pour des vecteurs unitaires : produit scalaire = similarité cosinus.
    Donc IndexFlatIP avec des vecteurs normalisés donne exactement la similarité cosinus.

    Score de 1.0 = identique, 0.0 = aucun rapport, -1.0 = opposé.
    En pratique on considère qu'un score > 0.7 est une correspondance fiable.

    IndexFlatIP fait une recherche exacte (pas d'approximation).
    C'est le bon choix jusqu'à ~100K vecteurs. Au-delà on passerait à IndexIVFFlat
    (recherche approximative, beaucoup plus rapide sur les très grands catalogues).
    """
    try:
        import faiss
    except ImportError:
        print("\nfaiss-cpu non installé. Installation :")
        print("  pip install faiss-cpu    (CPU)")
        print("  pip install faiss-gpu    (GPU CUDA)")
        return None

    dim = embeddings.shape[1]
    print(f"\nConstruction de l'index FAISS ({embeddings.shape[0]} vecteurs × {dim}D)...")

    index = faiss.IndexFlatIP(dim)

    # Re-normalisation L2 par sécurité (le modèle devrait déjà sortir des vecteurs normalisés)
    embeddings_copy = embeddings.copy()
    faiss.normalize_L2(embeddings_copy)
    index.add(embeddings_copy)

    print(f"Index construit : {index.ntotal} vecteurs")
    return index


# ─── Vérification de cohérence ────────────────────────────────────────────────

def sanity_check(index, embeddings, card_ids):
    """
    Vérifie que chaque image retrouve bien elle-même comme plus proche voisin.

    On cherche k=2 car l'image de référence est dans l'index (score=1.0 à rang 0).
    On vérifie juste que rang 0 = elle-même.

    Si ce test donne < 100% : problème de qualité des images ou du modèle.
    """
    import faiss

    print("\nVérification de cohérence (chaque carte doit retrouver elle-même)...")
    embeddings_copy = embeddings.copy()
    faiss.normalize_L2(embeddings_copy)

    scores, indices = index.search(embeddings_copy, k=2)

    correct = 0
    failures = []
    for i, (card_id, idx_row, score_row) in enumerate(zip(card_ids, indices, scores)):
        if idx_row[0] == i:
            correct += 1
        else:
            failures.append((card_id, card_ids[idx_row[0]], score_row[0]))

    pct = 100 * correct / len(card_ids)
    print(f"  Auto-retrieval : {correct}/{len(card_ids)} ({pct:.1f}%)")

    if failures:
        print(f"  ⚠ {len(failures)} cartes mal récupérées :")
        for orig, pred, score in failures[:5]:
            print(f"    '{orig}' → '{pred}' (score={score:.3f})")
        if len(failures) > 5:
            print(f"    ... et {len(failures) - 5} autres")
    else:
        print("  ✓ Parfait — toutes les cartes se retrouvent elles-mêmes")

    return pct


def demo_search(index, embeddings, card_ids, n=3):
    """
    Affiche quelques exemples de recherche (top-3 voisins pour N cartes aléatoires).
    Utile pour vérifier que les cartes similaires sont bien proches dans l'index.
    """
    import faiss
    import random

    print(f"\nExemple de recherche (top-3 pour {n} cartes aléatoires) :")
    sample_indices = random.sample(range(len(card_ids)), min(n, len(card_ids)))

    for idx in sample_indices:
        query = embeddings[idx:idx + 1].copy()
        faiss.normalize_L2(query)
        scores, indices = index.search(query, k=4)  # k=4 car rang 0 = elle-même

        print(f"\n  Requête : {card_ids[idx]}")
        for rank, (neighbor_idx, score) in enumerate(zip(indices[0][1:], scores[0][1:])):
            print(f"    #{rank + 1} : {card_ids[neighbor_idx]}  (score={score:.3f})")


# ─── Main ─────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Construction de l'index FAISS pour la reconnaissance de cartes")
    parser.add_argument("--name", required=True, help="Nom du modèle (ex: riftbound)")
    parser.add_argument(
        "--references",
        nargs="+",
        default=None,
        help="Dossiers d'images de référence, un par jeu (défaut: images/<name>/ "
             "s'il existe, sinon chaque sous-dossier images/*/)",
    )
    parser.add_argument(
        "--model",
        default=None,
        help="Chemin du modèle d'embedding (défaut: models/<name>/embedding_model.keras)",
    )
    args = parser.parse_args()

    model_dir = os.path.join("models", args.name)

    # Chargement de la config
    config_path = os.path.join(model_dir, "config.json")
    if not os.path.exists(config_path):
        print(f"config.json introuvable dans {model_dir}")
        print(f"  Lance d'abord : python train.py --name {args.name}")
        return

    with open(config_path) as f:
        config = json.load(f)

    img_size = config["img_size"]
    backbone_name = config["backbone"]
    embedding_dim = config.get("embedding_dim", 512)

    print(f"Modèle    : {args.name}")
    print(f"Backbone  : {backbone_name} ({img_size}x{img_size})")
    print(f"Embedding : {embedding_dim}D")

    # Chargement de la fonction de preprocessing
    from train import BACKBONES
    preprocess_fn = BACKBONES[backbone_name]["preprocess"]

    # Chargement du modèle d'embedding
    model_path = args.model or os.path.join(model_dir, "embedding_model.keras")
    if not os.path.exists(model_path):
        print(f"Modèle introuvable : {model_path}")
        return

    print(f"\nChargement du modèle : {model_path}")
    model = keras.models.load_model(model_path)
    print(f"  ✓ Output shape : {model.output_shape}")

    # Dossiers des images de référence (un par jeu, chargés à plat puis concaténés)
    references_dirs = [Path(d) for d in args.references] if args.references else default_reference_dirs(args.name)

    if not references_dirs:
        # Fallback sur le val set de augment.py
        references_dirs = [Path("datasets") / args.name / "val"]
        print(f"  images/{args.name}/ et images/*/ introuvables → utilisation du val set : {references_dirs[0]}")

    for d in references_dirs:
        if not d.is_dir():
            print(f"Dossier de références introuvable : {d}")
            return

    print(f"\nRéférences : {', '.join(str(d) for d in references_dirs)}")

    # Liste des images (décodées plus tard, une par une)
    paths, card_ids = [], []
    for d in references_dirs:
        p, ids = load_reference_images(str(d))
        paths.extend(p)
        card_ids.extend(ids)
    print(f"  {len(card_ids)} cartes listées")
    if len(set(card_ids)) != len(card_ids):
        dup = sorted({c for c in card_ids if card_ids.count(c) > 1})[:5]
        raise SystemExit(f"Identifiants en double entre les dossiers de références : {dup} ...")

    # Calcul des embeddings
    print()
    embeddings = compute_all_embeddings(model, paths, img_size, preprocess_fn)

    # Construction de l'index
    index = build_faiss_index(embeddings)
    if index is None:
        return

    # Sauvegarde de l'index
    import faiss

    index_path = os.path.join(model_dir, "index.faiss")
    faiss.write_index(index, index_path)
    size_mb = os.path.getsize(index_path) / (1024 * 1024)
    print(f"\n  ✓ Index sauvegardé : {index_path} ({size_mb:.1f} MB)")

    # Sauvegarde du mapping position → card ID
    labels_path = os.path.join(model_dir, "index_labels.json")
    with open(labels_path, "w") as f:
        json.dump(card_ids, f, indent=2)
    print(f"  ✓ Labels sauvegardés : {labels_path} ({len(card_ids)} cartes)")

    # Vérifications
    sanity_check(index, embeddings, card_ids)
    demo_search(index, embeddings, card_ids, n=3)

    print(f"\n✓ Index prêt ! Résumé :")
    print(f"  Cartes indexées : {len(card_ids)}")
    print(f"  Dimension       : {embedding_dim}D")
    print(f"  Fichiers        : {index_path}")
    print(f"                    {labels_path}")
    print(f"\nComment utiliser l'index en Python :")
    print(f"""
    import faiss, json
    import numpy as np

    # Charger une fois au démarrage
    index = faiss.read_index("models/{args.name}/index.faiss")
    card_ids = json.load(open("models/{args.name}/index_labels.json"))

    # Pour chaque photo
    query_embedding = model.predict(preprocessed_image)  # shape (1, {embedding_dim})
    faiss.normalize_L2(query_embedding)
    scores, indices = index.search(query_embedding, k=5)

    best_card_id = card_ids[indices[0][0]]
    confidence = float(scores[0][0])   # similarité cosinus, 0.0 → 1.0
    # Si confidence < 0.7 : carte inconnue ou photo de mauvaise qualité
    """)

    print(f"En Go (onnxruntime_go + faiss-go) → même logique, même index.")


if __name__ == "__main__":
    main()
