"""
Visualisation de l'index FAISS

Projette les embeddings 512D → 2D via UMAP ou t-SNE
pour voir comment les cartes sont organisées dans l'espace vectoriel.

Usage :
    pip install umap-learn plotly numpy faiss-cpu
    python visualize_index.py --name riftbound
    python visualize_index.py --name riftbound --method tsne
    python visualize_index.py --name riftbound --color-by set   (ex: ogn, op01...)
    python visualize_index.py --name riftbound --top 200        (top N cartes seulement)

Ce que tu verras :
    - Chaque point = une carte
    - Les points proches = cartes visuellement similaires selon le modèle
    - Les clusters = groupes de cartes que le modèle perçoit comme proches
    - Les points isolés = cartes uniques / atypiques

Sortie : models/<name>/embeddings_viz.html  (ouvrir dans le navigateur)
"""

import argparse
import json
import os
import sys

import numpy as np


def load_index(model_dir):
    """Charge l'index FAISS et les labels."""
    try:
        import faiss
    except ImportError:
        print("faiss-cpu requis : pip install faiss-cpu")
        sys.exit(1)

    index_path = os.path.join(model_dir, "index.faiss")
    labels_path = os.path.join(model_dir, "index_labels.json")

    if not os.path.exists(index_path):
        print(f"index.faiss introuvable dans {model_dir}")
        print("  Lance d'abord : python build_index.py --name <nom>")
        sys.exit(1)

    print(f"Chargement de l'index...")
    index = faiss.read_index(index_path)
    with open(labels_path) as f:
        card_ids = json.load(f)

    # Extraire tous les vecteurs de l'index
    n = index.ntotal
    dim = index.d
    embeddings = np.zeros((n, dim), dtype=np.float32)
    index.reconstruct_n(0, n, embeddings)

    print(f"  {n} cartes × {dim}D chargées")
    return embeddings, card_ids


def reduce_dimensions(embeddings, method="umap", n_neighbors=15, min_dist=0.1):
    """
    Réduit les embeddings 512D → 2D.

    UMAP  : préserve mieux la structure globale et locale, plus rapide
    t-SNE : préserve surtout les clusters locaux, plus lent sur grands datasets
    """
    print(f"\nRéduction dimensionnelle avec {method.upper()}...")

    if method == "umap":
        try:
            import umap
        except ImportError:
            print("umap-learn requis : pip install umap-learn")
            sys.exit(1)

        reducer = umap.UMAP(
            n_neighbors=n_neighbors,
            min_dist=min_dist,
            n_components=2,
            metric="cosine",      # cohérent avec notre espace d'embedding
            random_state=42,
            verbose=True,
        )

    elif method == "tsne":
        from sklearn.manifold import TSNE
        reducer = TSNE(
            n_components=2,
            perplexity=min(30, len(embeddings) - 1),
            metric="cosine",
            random_state=42,
            n_iter=1000,
            verbose=1,
        )
    else:
        raise ValueError(f"Méthode inconnue : {method}. Utilise 'umap' ou 'tsne'.")

    coords_2d = reducer.fit_transform(embeddings)
    print(f"  Réduction terminée : shape {coords_2d.shape}")
    return coords_2d


def extract_set_prefix(card_id):
    """
    Extrait le préfixe de set depuis un card_id.
    Ex : 'ogn-001-298' → 'ogn'
         'op01-001'     → 'op01'
         'st01-003-en'  → 'st01'
    """
    parts = card_id.split("-")
    return parts[0].lower() if parts else "unknown"


def compute_nearest_neighbors(embeddings, card_ids, k=5):
    """
    Pour chaque carte, trouve ses K plus proches voisins.
    Utilisé pour afficher les voisins dans le tooltip.
    """
    try:
        import faiss
        index = faiss.IndexFlatIP(embeddings.shape[1])
        emb_copy = embeddings.copy()
        faiss.normalize_L2(emb_copy)
        index.add(emb_copy)
        scores, indices = index.search(emb_copy, k + 1)  # +1 car la carte elle-même est rang 0
        return scores, indices
    except Exception:
        return None, None


def build_plotly_figure(coords_2d, card_ids, color_by="set", scores=None, indices=None):
    """
    Construit la figure Plotly interactive.

    color_by :
        'set'   → colorier par extension (ogn, op01, etc.)
        'index' → dégradé par position dans l'index
    """
    try:
        import plotly.graph_objects as go
        import plotly.express as px
    except ImportError:
        print("plotly requis : pip install plotly")
        sys.exit(1)

    x = coords_2d[:, 0]
    y = coords_2d[:, 1]
    n = len(card_ids)

    # Couleurs par set
    sets = [extract_set_prefix(cid) for cid in card_ids]
    unique_sets = sorted(set(sets))
    set_to_idx = {s: i for i, s in enumerate(unique_sets)}
    color_values = [set_to_idx[s] for s in sets]

    # Palette : une couleur par set
    palette = px.colors.qualitative.Plotly + px.colors.qualitative.D3
    point_colors = [palette[i % len(palette)] for i in color_values]

    # Tooltip personnalisé
    hover_texts = []
    for i, cid in enumerate(card_ids):
        lines = [f"<b>{cid}</b>", f"Set : {sets[i]}"]
        if indices is not None and scores is not None:
            lines.append("<br>Voisins proches :")
            for rank in range(1, min(4, indices.shape[1])):
                neighbor_idx = indices[i][rank]
                neighbor_score = scores[i][rank]
                neighbor_id = card_ids[neighbor_idx]
                lines.append(f"  #{rank} {neighbor_id} ({neighbor_score:.3f})")
        hover_texts.append("<br>".join(lines))

    # Trace principale
    scatter = go.Scatter(
        x=x,
        y=y,
        mode="markers",
        marker=dict(
            size=6,
            color=point_colors,
            opacity=0.75,
            line=dict(width=0.3, color="white"),
        ),
        text=hover_texts,
        hovertemplate="%{text}<extra></extra>",
        customdata=card_ids,
    )

    fig = go.Figure(data=[scatter])

    # Légende manuelle par set (Plotly ne gère pas bien les légendes sur Scatter couleur libre)
    for s in unique_sets:
        idx = set_to_idx[s]
        color = palette[idx % len(palette)]
        fig.add_trace(go.Scatter(
            x=[None], y=[None],
            mode="markers",
            marker=dict(size=8, color=color),
            name=s.upper(),
            showlegend=True,
        ))

    fig.update_layout(
        title=dict(
            text=f"Espace d'embedding — {n} cartes",
            font=dict(size=18),
        ),
        xaxis=dict(showgrid=False, zeroline=False, showticklabels=False, title=""),
        yaxis=dict(showgrid=False, zeroline=False, showticklabels=False, title=""),
        plot_bgcolor="white",
        paper_bgcolor="white",
        hovermode="closest",
        legend=dict(
            title="Extensions",
            itemsizing="constant",
            font=dict(size=12),
        ),
        margin=dict(l=20, r=20, t=60, b=20),
        width=None,
        height=700,
    )

    return fig


def add_cluster_annotations(fig, coords_2d, card_ids, sets):
    """
    Ajoute des annotations de clusters en calculant le centroïde de chaque set.
    Optionnel — rend la visu plus lisible sur de grands datasets.
    """
    try:
        import plotly.graph_objects as go
        unique_sets = sorted(set(sets))

        for s in unique_sets:
            mask = [i for i, st in enumerate(sets) if st == s]
            if not mask:
                continue
            cx = np.mean(coords_2d[mask, 0])
            cy = np.mean(coords_2d[mask, 1])

            fig.add_annotation(
                x=cx, y=cy,
                text=s.upper(),
                showarrow=False,
                font=dict(size=11, color="#444"),
                bgcolor="rgba(255,255,255,0.7)",
                bordercolor="#ccc",
                borderwidth=1,
                borderpad=4,
            )
    except Exception:
        pass

    return fig


def compute_stats(embeddings, card_ids, scores, indices):
    """Affiche quelques statistiques sur la qualité de l'espace d'embedding."""
    print("\n=== Statistiques de l'espace d'embedding ===")
    print(f"  Cartes              : {len(card_ids)}")
    print(f"  Dimension           : {embeddings.shape[1]}D")

    if scores is not None:
        # Score moyen du plus proche voisin réel (rang 1, pas soi-même)
        nn_scores = scores[:, 1]
        print(f"  Score voisin #1     : {nn_scores.mean():.4f} ± {nn_scores.std():.4f}")
        print(f"    min={nn_scores.min():.4f}  max={nn_scores.max():.4f}")

        # Top 5 cartes avec le voisin le plus proche (embeddings très similaires)
        top_similar_idx = np.argsort(nn_scores)[::-1][:5]
        print("\n  Top 5 paires les plus similaires (potentiels doublons / variantes) :")
        for i in top_similar_idx:
            neighbor = card_ids[indices[i][1]]
            print(f"    {card_ids[i]:30s} ↔ {neighbor:30s}  ({nn_scores[i]:.4f})")

        # Top 5 cartes les plus isolées
        isolated_idx = np.argsort(nn_scores)[:5]
        print("\n  Top 5 cartes les plus isolées (embeddings atypiques) :")
        for i in isolated_idx:
            print(f"    {card_ids[i]:30s}  (voisin score={nn_scores[i]:.4f})")


def main():
    parser = argparse.ArgumentParser(description="Visualisation de l'index FAISS")
    parser.add_argument("--name", required=True, help="Nom du modèle (ex: riftbound)")
    parser.add_argument("--method", default="umap", choices=["umap", "tsne"],
                        help="Méthode de réduction dimensionnelle (défaut: umap)")
    parser.add_argument("--color-by", default="set", choices=["set", "index"],
                        help="Critère de colorisation (défaut: set)")
    parser.add_argument("--top", type=int, default=None,
                        help="Limiter à N cartes (pour tester rapidement)")
    parser.add_argument("--no-neighbors", action="store_true",
                        help="Ne pas calculer les voisins proches (plus rapide)")
    parser.add_argument("--annotate-sets", action="store_true",
                        help="Ajouter des annotations de centroïde par set")
    parser.add_argument("--n-neighbors", type=int, default=15,
                        help="Paramètre UMAP n_neighbors (défaut: 15)")
    parser.add_argument("--min-dist", type=float, default=0.1,
                        help="Paramètre UMAP min_dist (défaut: 0.1)")
    args = parser.parse_args()

    model_dir = os.path.join("models", args.name)
    if not os.path.isdir(model_dir):
        print(f"Dossier modèle introuvable : {model_dir}")
        sys.exit(1)

    # Chargement
    embeddings, card_ids = load_index(model_dir)

    # Sous-ensemble optionnel
    if args.top and args.top < len(card_ids):
        print(f"\nLimité aux {args.top} premières cartes")
        embeddings = embeddings[:args.top]
        card_ids = card_ids[:args.top]

    # Voisins proches
    scores, indices = None, None
    if not args.no_neighbors:
        print("\nCalcul des voisins proches...")
        scores, indices = compute_nearest_neighbors(embeddings, card_ids, k=5)
        compute_stats(embeddings, card_ids, scores, indices)

    # Réduction dimensionnelle
    coords_2d = reduce_dimensions(
        embeddings,
        method=args.method,
        n_neighbors=args.n_neighbors,
        min_dist=args.min_dist,
    )

    # Figure
    print("\nGénération de la visualisation...")
    sets = [extract_set_prefix(cid) for cid in card_ids]
    fig = build_plotly_figure(coords_2d, card_ids, color_by=args.color_by,
                              scores=scores, indices=indices)

    if args.annotate_sets:
        fig = add_cluster_annotations(fig, coords_2d, card_ids, sets)

    # Export HTML interactif
    output_path = os.path.join(model_dir, "embeddings_viz.html")
    fig.write_html(
        output_path,
        include_plotlyjs="cdn",
        full_html=True,
        config={
            "displayModeBar": True,
            "modeBarButtonsToRemove": ["select2d", "lasso2d"],
            "toImageButtonOptions": {"format": "png", "width": 1400, "height": 900},
        },
    )

    size_kb = os.path.getsize(output_path) / 1024
    print(f"\n✓ Visualisation sauvegardée : {output_path} ({size_kb:.0f} KB)")
    print(f"  Ouvre ce fichier dans ton navigateur.")
    print(f"\nInteractivité :")
    print(f"  - Survol  → card ID + voisins proches + score")
    print(f"  - Scroll  → zoom")
    print(f"  - Drag    → pan")
    print(f"  - Légende → afficher/masquer un set")
    print(f"  - Double-clic légende → isoler un set")


if __name__ == "__main__":
    main()
