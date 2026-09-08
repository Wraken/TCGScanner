"""
Ajout incrémental de cartes à un index FAISS existant — SANS retrain, SANS rebuild.

C'est la promesse de la pipeline embedding : pour un nouveau set, on embed les
nouveaux scans avec le modèle figé et on les ajoute à l'index. Les 2870 vecteurs
existants ne bougent pas d'un octet.

    1. Charge l'index + labels existants
    2. Scanne le dossier de références : toute carte absente des labels = nouvelle
    3. Embed les nouvelles cartes (même recette que build_index : moyenne de 8 vues)
    4. index.add() + labels.extend() + sauvegarde (backup .bak avant écriture)
    5. Sanity checks : les nouvelles se retrouvent, les anciennes aussi (zéro pollution)

Usage :
    python add_to_index.py --name tcg                       # tous les images/*/
    python add_to_index.py --name tcg --references ../fetch/pokemon/images
"""

import argparse
import json
import os
import shutil
from pathlib import Path

os.environ["TF_CPP_MIN_LOG_LEVEL"] = "2"
os.environ["TF_XLA_FLAGS"] = "--tf_xla_auto_jit=0"
os.environ["TF_ENABLE_ONEDNN_OPTS"] = "0"

import numpy as np
from tensorflow import keras
keras.mixed_precision.set_global_policy("float32")
from tqdm import tqdm
import faiss

from build_index import SUPPORTED_EXTS, compute_reference_embedding, default_reference_dirs


def main():
    parser = argparse.ArgumentParser(description="Ajout incrémental de cartes à l'index FAISS")
    parser.add_argument("--name", required=True, help="Nom du modèle (ex: tcg)")
    parser.add_argument("--references", nargs="+", default=None,
                        help="Dossiers d'images de référence (défaut: images/<name>/ s'il "
                             "existe, sinon chaque sous-dossier images/*/ — un par jeu)")
    args = parser.parse_args()

    model_dir = os.path.join("models", args.name)
    index_path = os.path.join(model_dir, "index.faiss")
    labels_path = os.path.join(model_dir, "index_labels.json")

    with open(os.path.join(model_dir, "config.json")) as f:
        config = json.load(f)
    img_size = config["img_size"]

    from train import BACKBONES
    preprocess_fn = BACKBONES[config["backbone"]]["preprocess"]

    # ─── Index existant ────────────────────────────────────────────────────────
    index = faiss.read_index(index_path)
    labels = json.load(open(labels_path))
    known = set(labels)
    assert index.ntotal == len(labels), "index et labels désynchronisés !"
    print(f"Index existant : {index.ntotal} cartes")

    # ─── Nouvelles cartes ──────────────────────────────────────────────────────
    ref_dirs = [Path(d) for d in args.references] if args.references else default_reference_dirs(args.name)
    if not ref_dirs:
        print("Aucun dossier de références (images/<name>/ ou images/*/ introuvables).")
        return
    print(f"Références : {', '.join(str(d) for d in ref_dirs)}")
    all_paths = sorted(p for d in ref_dirs for p in d.iterdir() if p.suffix.lower() in SUPPORTED_EXTS)
    new_paths = [p for p in all_paths if p.stem not in known]

    if not new_paths:
        print("Aucune nouvelle carte — l'index est déjà à jour.")
        return
    print(f"Nouvelles cartes : {len(new_paths)} (ex: {new_paths[0].stem}, {new_paths[-1].stem})")

    # ─── Modèle ────────────────────────────────────────────────────────────────
    model = keras.models.load_model(os.path.join(model_dir, "embedding_model.keras"))

    # ─── Embeddings des nouvelles cartes ──────────────────────────────────────
    new_embeddings, new_ids = [], []
    for p in tqdm(new_paths, desc="Embedding des nouvelles cartes"):
        img = keras.utils.load_img(str(p), target_size=(img_size, img_size))
        arr = keras.utils.img_to_array(img).astype(np.float32)
        new_embeddings.append(compute_reference_embedding(model, arr, preprocess_fn))
        new_ids.append(p.stem)
    new_embeddings = np.vstack(new_embeddings).astype(np.float32)
    faiss.normalize_L2(new_embeddings)

    # ─── Généralisation : similarité aux ANCIENNES cartes avant ajout ─────────
    # (les nouvelles cartes n'ont jamais été vues au training : un score max
    #  modéré = bon signe, un score > ~0.8 = risque de confusion avec une ancienne)
    scores_old, idx_old = index.search(new_embeddings, k=1)
    closest = scores_old[:, 0]
    print(f"\nSimilarité max de chaque nouvelle carte aux {len(labels)} anciennes :")
    print(f"  min={closest.min():.3f}  médiane={np.median(closest):.3f}  max={closest.max():.3f}")
    worst = np.argsort(-closest)[:5]
    for i in worst:
        print(f"    {new_ids[i]} → {labels[idx_old[i][0]]}  (score={closest[i]:.3f})")

    # ─── Ajout + sauvegarde (backup avant écriture) ───────────────────────────
    shutil.copy2(index_path, index_path + ".bak")
    shutil.copy2(labels_path, labels_path + ".bak")

    index.add(new_embeddings)
    labels.extend(new_ids)
    faiss.write_index(index, index_path)
    with open(labels_path, "w") as f:
        json.dump(labels, f, indent=2)
    print(f"\n✓ Index : {index.ntotal} cartes ({len(new_ids)} ajoutées) — backup en .bak")

    # ─── Sanity checks ────────────────────────────────────────────────────────
    # 1. Les nouvelles cartes se retrouvent elles-mêmes
    scores, indices = index.search(new_embeddings, k=2)
    n_old = len(labels) - len(new_ids)
    ok = sum(1 for i, row in enumerate(indices) if row[0] == n_old + i)
    print(f"\nAuto-retrieval des nouvelles : {ok}/{len(new_ids)} ({100*ok/len(new_ids):.1f}%)")
    if ok < len(new_ids):
        for i, row in enumerate(indices):
            if row[0] != n_old + i:
                print(f"    ✗ {new_ids[i]} → {labels[row[0]]} (score={scores[i][0]:.3f})")

    # 2. Les anciennes cartes se retrouvent toujours (zéro pollution)
    old_vecs = index.reconstruct_n(0, n_old)
    scores2, indices2 = index.search(old_vecs, k=1)
    ok2 = sum(1 for i, row in enumerate(indices2) if row[0] == i)
    print(f"Auto-retrieval des anciennes : {ok2}/{n_old} ({100*ok2/n_old:.1f}%)")
    if ok2 < n_old:
        stolen = [(labels[i], labels[indices2[i][0]], scores2[i][0])
                  for i in range(n_old) if indices2[i][0] != i]
        for orig, pred, s in stolen[:5]:
            print(f"    ✗ {orig} → {pred} (score={s:.3f})")

    print("\n✓ Terminé.")


if __name__ == "__main__":
    main()
