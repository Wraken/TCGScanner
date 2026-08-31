"""
Visualisation de l'index FAISS : projection UMAP 2D des embeddings 512D.

Produit deux fichiers :
  - index_map.html  : scatter interactif (plotly), un point par carte, couleur par set,
                      hover = card_id. Légende cliquable pour isoler des sets.
  - index_atlas.jpg : mosaïque — chaque carte est dessinée (miniature) à sa position
                      dans l'espace d'embedding projeté.

Usage:
    python visualize_index.py
    python visualize_index.py --no-atlas          # scatter seulement (plus rapide)
    python visualize_index.py --thumb 48          # taille des miniatures de l'atlas
"""

import argparse
import json
import os

import pathlib

import numpy as np
import faiss

parser = argparse.ArgumentParser()
parser.add_argument("--index",  default="models/tcg/index.faiss")
parser.add_argument("--labels", default="models/tcg/index_labels.json")
# Defaults point at the packaged app images (512px webp, one dir per game):
# far faster to thumbnail than the full-size source PNGs (Pokémon alone is
# 6.8 GB of PNG), and they exist for every indexed game.
APP_IMAGES = "../../models/tcg/images"
parser.add_argument("--images", nargs="+", metavar="GAME=DIR",
                    default=[f"{g}={APP_IMAGES}/{g}" for g in ("onepiece", "riftbound", "lorcana", "pokemon")],
                    help="reference image dirs, one per game (atlas thumbnails + game "
                         "assignment), matched by file stem")
parser.add_argument("--color-by", choices=("game", "set"), default="game",
                    help="scatter colour/legend: per game (readable at 200+ sets) or per set")
parser.add_argument("--thumb",  type=int, default=56, help="hauteur des miniatures (atlas)")
parser.add_argument("--canvas", type=int, default=0,
                    help="taille du canvas atlas (px); 0 = auto d'après le nombre de cartes")
parser.add_argument("--no-atlas", action="store_true")
args = parser.parse_args()

# ─── Vecteurs ──────────────────────────────────────────────────────────────────

index = faiss.read_index(args.index)
labels = json.load(open(args.labels))
n = index.ntotal
vecs = index.reconstruct_n(0, n)
print(f"Index : {n} vecteurs x {vecs.shape[1]}D")

def set_of(card_id: str) -> str:
    # First dash-separated token is the set for every game: OP01-025_p1 -> OP01,
    # ogn-042-298 -> ogn, 1-154 -> 1, sv10-101 -> sv10, me02.5-107 -> me02.5.
    return card_id.split("-")[0]

# Map each card id to its source image and game. Sources are stored one dir per
# game, so membership is the ground truth — no id heuristics (which broke on
# Pokémon: lowercase "sv01" looked like Riftbound, "A1" like One Piece).
IMG_EXTS = (".png", ".jpg", ".jpeg", ".webp")
img_path, game_of = {}, {}
for spec in args.images:
    game, _, d = spec.partition("=")
    dp = pathlib.Path(d)
    if not game or not dp.is_dir():
        continue
    for f in dp.iterdir():
        if f.suffix.lower() in IMG_EXTS and f.stem not in img_path:
            img_path[f.stem] = f
            game_of[f.stem] = game

sets = [set_of(l) for l in labels]
games = [game_of.get(l, "unknown") for l in labels]

# ─── UMAP ──────────────────────────────────────────────────────────────────────

print("Projection UMAP 512D -> 2D (metric=cosine)...")
import umap
reducer = umap.UMAP(n_neighbors=15, min_dist=0.1, metric="cosine", random_state=42)
xy = reducer.fit_transform(vecs)
print("  ok")

# ─── Scatter interactif ────────────────────────────────────────────────────────

import plotly.graph_objects as go
import plotly.express as px

palette = px.colors.qualitative.Dark24 + px.colors.qualitative.Light24
groups = games if args.color_by == "game" else sets
unique_groups = sorted(set(groups), key=lambda g: -groups.count(g))
fig = go.Figure()
for i, g in enumerate(unique_groups):
    mask = np.array([x == g for x in groups])
    idx = np.where(mask)[0]
    fig.add_trace(go.Scattergl(
        x=xy[mask, 0], y=xy[mask, 1],
        mode="markers",
        name=f"{g} ({mask.sum()})",
        text=[labels[j] for j in idx],
        customdata=[sets[j] for j in idx],
        hovertemplate="%{text}<br>set %{customdata}<extra>" + g + "</extra>",
        marker=dict(size=3 if n > 10000 else 4, color=palette[i % len(palette)], opacity=0.75),
    ))
fig.update_layout(
    title=(f"Espace d'embedding TCG — UMAP 2D ({n} cartes : "
           + " / ".join(f"{games.count(g)} {g}" for g in sorted(set(games), key=games.count, reverse=True))
           + ")"),
    template="plotly_dark",
    width=1500, height=950,
    legend=dict(font=dict(size=9), itemsizing="constant"),
    xaxis=dict(visible=False), yaxis=dict(visible=False, scaleanchor="x"),
)
fig.write_html("index_map.html", include_plotlyjs="cdn")
print("  -> index_map.html")

# ─── Atlas mosaïque ────────────────────────────────────────────────────────────

if not args.no_atlas:
    from PIL import Image
    # ~50px of canvas per sqrt(card) keeps thumbnail overlap similar as the
    # index grows (6000px was tuned for ~6.5k cards; 28k cards -> ~8400px).
    C = args.canvas or max(6000, int(np.sqrt(n) * 50))
    TH = args.thumb
    print(f"Atlas {C}x{C}, miniatures h={TH}...")

    mn, mx = xy.min(axis=0), xy.max(axis=0)
    pos = (xy - mn) / (mx - mn)  # 0..1
    margin = TH
    pos = margin + pos * (C - 2 * margin)

    canvas = Image.new("RGB", (C, C), (18, 18, 24))
    missing = 0
    order = np.argsort(pos[:, 1])  # dessine de haut en bas pour un chevauchement propre
    for j in order:
        p = img_path.get(labels[j])
        if p is None:
            missing += 1
            continue
        im = Image.open(p).convert("RGB")
        w = max(1, round(im.width * TH / im.height))
        im = im.resize((w, TH), Image.LANCZOS)
        x, y = int(pos[j, 0] - w / 2), int(pos[j, 1] - TH / 2)
        canvas.paste(im, (x, y))
    canvas.save("index_atlas.jpg", quality=88)
    print(f"  -> index_atlas.jpg ({missing} images manquantes)")

print("Termine.")
