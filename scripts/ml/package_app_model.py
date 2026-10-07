"""Package the embedding pipeline for the TCGScanner app.

Copies models/tcg/ (scripts side) into the app's models/<name>/ directory:
  - yolo.onnx, embedding_model.onnx  : copied as-is
  - config.json                      : copied, + "score_floor"/"margin_min" keys
  - index_labels.json                : copied as-is
  - index.bin                        : FAISS index exported as flat float32
                                       header: magic "TCGEMBIX" + uint32 version, n, dim (LE)
  - search.onnx                      : one-node MatMul model, query [1,dim] x W [dim,n]
                                       -> scores [1,n]; W = the same index vectors,
                                       transposed. Lets the app run the cosine search
                                       through ONNX Runtime instead of a JS loop.
  - images/<game>/                   : reference PNGs converted to webp (resized),
                                       one sub-directory per game; sources default
                                       to scripts/fetch/<game>/images/ (training
                                       dirs are not packaged)
  - images/<game>/_cover.webp        : optional game tile artwork (from a
                                       _cover.* file in the source dir, or
                                       dropped in by hand); never in the
                                       manifest, never pruned
  - images/manifest.json             : card id -> path relative to images/, read
                                       by the app (one map lookup, no dir walk)
  - metadata/<game>.json             : card id -> {name, set, set_name, number,
                                       rarity, type, colors}, copied from each
                                       fetcher's cards.json (core of the bundle)
  - metadata/cardmarket.json         : card id -> Cardmarket product id, built by
                                       scripts/fetch/cardmarket; the app prices
                                       the collection with it (core of the bundle)

Re-runnable: only missing/outdated images are converted, and files left over
from a previous layout are removed. Use after every add_to_index.py run to
refresh the app copy.
"""
import argparse
import json
import re
import shutil
import struct
from concurrent.futures import ProcessPoolExecutor
from pathlib import Path

MAGIC = b"TCGEMBIX"
VERSION = 1

CARD_IMAGE_SUFFIXES = {".png", ".jpg", ".jpeg", ".webp"}
VARIANT_SUFFIX = re.compile(r"_[pr]\d+$")


def export_index(index_path: Path, out_path: Path):
    """Writes index.bin and returns the (n, dim) float32 vectors it contains."""
    import faiss
    import numpy as np

    index = faiss.read_index(str(index_path))
    vecs = np.ascontiguousarray(index.reconstruct_n(0, index.ntotal).astype(np.float32))
    n, dim = vecs.shape
    with open(out_path, "wb") as f:
        f.write(MAGIC)
        f.write(struct.pack("<III", VERSION, n, dim))
        f.write(vecs.tobytes(order="C"))
    return vecs


def export_search_onnx(vecs, out_path: Path, fp16: bool = False) -> None:
    """search.onnx: scores[1,n] = MatMul(query[1,dim], W[dim,n]) with W = vecs.T.

    The vectors are L2-normalised, so scores are cosines -- the same dot products
    the JS loop computes (only the float summation order differs). With `fp16`
    the initializer is stored as float16 (half the file) and Cast back to
    float32 in-graph before the MatMul; scores then differ by ~1e-4.
    """
    import numpy as np
    import onnx
    from onnx import TensorProto, helper, numpy_helper

    n, dim = vecs.shape
    w = np.ascontiguousarray(vecs.T)  # [dim, n]
    query = helper.make_tensor_value_info("query", TensorProto.FLOAT, [1, dim])
    scores = helper.make_tensor_value_info("scores", TensorProto.FLOAT, [1, n])
    if fp16:
        inits = [numpy_helper.from_array(w.astype(np.float16), "W_fp16")]
        nodes = [helper.make_node("Cast", ["W_fp16"], ["W"], to=TensorProto.FLOAT),
                 helper.make_node("MatMul", ["query", "W"], ["scores"])]
    else:
        inits = [numpy_helper.from_array(w, "W")]
        nodes = [helper.make_node("MatMul", ["query", "W"], ["scores"])]
    graph = helper.make_graph(nodes, "tcg_search", [query], [scores], initializer=inits)
    model = helper.make_model(graph, producer_name="package_app_model",
                              opset_imports=[helper.make_opsetid("", 17)])
    model.ir_version = 8  # ORT 1.24 accepts <= 10; 8 keeps older mobile runtimes happy
    onnx.checker.check_model(model, full_check=False)
    onnx.save(model, str(out_path))
    size = out_path.stat().st_size
    print(f"search.onnx written: MatMul [1,{dim}] x [{dim},{n}] "
          f"({'fp16' if fp16 else 'fp32'} weights, {size / 1e6:.1f} MB)")


def convert_image(args: tuple[Path, Path, int]) -> bool:
    src, dst, width = args
    from PIL import Image

    if dst.exists() and dst.stat().st_mtime >= src.stat().st_mtime:
        return False
    img = Image.open(src).convert("RGB")
    if img.width > width:
        h = round(img.height * width / img.width)
        img = img.resize((width, h), Image.LANCZOS)
    img.save(dst, "WEBP", quality=80)
    return True


def image_source_problems(specs: list[str]) -> tuple[dict[str, Path], list[str]]:
    """Parse --images GAME=DIR specs into {game: dir}, catching a missing or
    empty/partial source dir before any work runs: step 4's stale-pruning would
    otherwise silently delete every webp already in images/<game>/ (gitignored
    -- only recoverable by re-running the packager)."""
    image_dirs: dict[str, Path] = {}
    problems: list[str] = []
    for spec in specs:
        game, _, d = spec.partition("=")
        if not game or not d:
            problems.append(f"expected GAME=DIR, got {spec!r}")
            continue
        if game in image_dirs:
            problems.append(f"{game}: given twice ({image_dirs[game]} and {d})")
            continue
        p = Path(d)
        if not p.is_dir():
            problems.append(f"{game}: {p} does not exist -- the run refuses "
                             f"(pruning would drop images/{game}/)")
            continue
        has_card = any(f.is_file() and f.suffix.lower() in CARD_IMAGE_SUFFIXES and f.stem != "_cover"
                        for f in p.iterdir())
        if not has_card:
            problems.append(f"{game}: {p} has no card images -- the run refuses "
                             f"(pruning would drop images/{game}/)")
            continue
        image_dirs[game] = p
    return image_dirs, problems


def manifest_game(manifest: dict[str, str], label: str) -> str:
    """Game prefix of a label's manifest path ("onepiece/OP01-025.webp" ->
    "onepiece"), or "" if the label isn't in the manifest."""
    rel = manifest.get(label, "")
    return rel.split("/", 1)[0] if "/" in rel else ""


def metadata_coverage(labels: list[str], manifest: dict[str, str],
                       metadata: dict[str, dict]) -> dict[str, list[str]]:
    """Index labels of each game (per the manifest) missing from its metadata."""
    by_game: dict[str, list[str]] = {}
    for label in labels:
        game = manifest_game(manifest, label)
        if game in metadata and label not in metadata[game]:
            by_game.setdefault(game, []).append(label)
    return {g: sorted(m) for g, m in by_game.items()}


def unindexed_images(manifest: dict[str, str], labels: list[str]) -> dict[str, list[str]]:
    """Manifest entries (per game) with no reference in the recognition index:
    the app shows them, the scanner can never match them."""
    known = set(labels)
    by_game: dict[str, list[str]] = {}
    for stem, rel in manifest.items():
        if stem not in known:
            by_game.setdefault(rel.split("/", 1)[0], []).append(stem)
    return {g: sorted(m) for g, m in by_game.items()}


def warn_unindexed(manifest: dict[str, str], labels: list[str]) -> None:
    for game, stems in sorted(unindexed_images(manifest, labels).items()):
        print(f"  WARNING: {game}: {len(stems)} manifest images have no index reference "
              f"(e.g. {stems[:5]}) -- shown in the app, never recognised; run add_to_index.py")


def fill_variant_fallbacks(labels: list[str], manifest: dict[str, str],
                            metadata: dict[str, dict]) -> dict[str, int]:
    """Give variant ids (OP01-025_p2) the metadata of their base card when the
    source lacks the variant itself. Mutates `metadata`; returns added per game."""
    added: dict[str, int] = {}
    for label in labels:
        game = manifest_game(manifest, label)
        if game not in metadata or label in metadata[game]:
            continue
        base = VARIANT_SUFFIX.sub("", label)
        if base != label and base in metadata[game]:
            # shallow copy is enough: entries are never mutated after this
            metadata[game][label] = dict(metadata[game][base])
            added[game] = added.get(game, 0) + 1
    return added


STRING_FIELDS = ("name", "set", "set_name", "number", "rarity", "type")


def is_pocket_id(label: str) -> bool:
    """Pokémon TCG Pocket ids (A1-001, A2a-080, B1-019, P-A-001): mobile-only
    cards, never priced, left out of the price coverage."""
    head = label.split("-", 1)[0]
    return label.startswith("P-A-") or (head[:1] in ("A", "B") and head[1:2].isdigit())


def price_coverage(labels: list[str], manifest: dict[str, str],
                   mapping: dict) -> dict[str, tuple[int, int]]:
    """Per game: (index labels with a Cardmarket product, index labels)."""
    cards = mapping.get("cards", {})
    other = mapping.get("cards_non_english", {})
    out: dict[str, list[int]] = {}
    for label in labels:
        game = manifest_game(manifest, label)
        if game == "" or is_pocket_id(label):
            continue
        n = out.setdefault(game, [0, 0])
        n[1] += 1
        if label in cards or label in other:
            n[0] += 1
    return {g: (p, t) for g, (p, t) in out.items()}


def metadata_problems(cards) -> list[str]:
    """cards.json shapes the app's Go unmarshalling would reject outright,
    dropping the WHOLE game file (e.g. "colors" sent as a string, not a list,
    or a string field sent as a number)."""
    if not isinstance(cards, dict):
        return [f"expected an object of card id -> card, got {type(cards).__name__}"]
    problems: list[str] = []
    for card_id, entry in cards.items():
        if not isinstance(entry, dict):
            problems.append(f"{card_id}: expected an object, got {type(entry).__name__}")
            continue
        for k in STRING_FIELDS:
            v = entry.get(k)
            if v is not None and not isinstance(v, str):
                problems.append(f"{card_id}: {k} must be a string, got {type(v).__name__}")
        colors = entry.get("colors")
        if colors is not None:
            if not isinstance(colors, list):
                problems.append(f"{card_id}: colors must be a list, got {type(colors).__name__}")
            elif not all(isinstance(c, str) for c in colors):
                problems.append(f"{card_id}: colors must be strings")
    return problems


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", default="models/tcg", help="source model dir (scripts side)")
    ap.add_argument("--images", nargs="+", metavar="GAME=DIR",
                    default=["onepiece=../fetch/onepiece/images", "riftbound=../fetch/riftbound/images",
                             "lorcana=../fetch/lorcana/images", "pokemon=../fetch/pokemon/images"],
                    help="source reference image dirs, one per game; images land in "
                         "images/<game>/ (labels are matched by file stem)")
    ap.add_argument("--keep-stale", action="store_true",
                    help="do not delete files under images/ that the manifest no longer references")
    ap.add_argument("--metadata", nargs="+", metavar="GAME=FILE",
                    default=["onepiece=../fetch/onepiece/cards.json",
                             "riftbound=../fetch/riftbound/cards.json",
                             "lorcana=../fetch/lorcana/cards.json",
                             "pokemon=../fetch/pokemon/cards.json"],
                    help="card metadata per game (fetcher cards.json); missing files are skipped")
    ap.add_argument("--skip-metadata", action="store_true")
    ap.add_argument("--cardmarket", default="../fetch/cardmarket/cardmarket.json",
                    help="card id -> Cardmarket product mapping to ship (see scripts/fetch/cardmarket)")
    ap.add_argument("--allow-price-regression", action="store_true",
                    help="ship a cardmarket.json that prices fewer index labels of some game than the "
                         "bundle's current one (otherwise the build stops)")
    ap.add_argument("--dest", default="../../models/tcg", help="app model dir")
    ap.add_argument("--score-floor", type=float, default=0.4,
                    help="min top-1 cosine to lock (rejects noise)")
    ap.add_argument("--margin-min", type=float, default=0.1,
                    help="min lead over the best different card to lock")
    ap.add_argument("--width", type=int, default=512, help="preview image max width")
    ap.add_argument("--skip-images", action="store_true")
    ap.add_argument("--fp16-search", action="store_true",
                    help="store search.onnx weights as float16 (half the size, ~1e-3 score error)")
    args = ap.parse_args()

    src, dest = Path(args.src), Path(args.dest)

    # 0. validate --images specs before any work (not even `dest` creation): a
    #    missing, duplicate, or empty/partial source dir must not reach step
    #    4's stale-pruning, which would delete every webp already in
    #    images/<game>/ for that game.
    image_dirs, image_problems = image_source_problems(args.images)
    if not args.skip_images and image_problems:
        ap.error("; ".join(image_problems))

    dest.mkdir(parents=True, exist_ok=True)

    # 1. ONNX models + labels
    for name in ("yolo.onnx", "embedding_model.onnx", "index_labels.json"):
        shutil.copy2(src / name, dest / name)
        print(f"copied {name}")

    # 2. config.json with the app-side lock thresholds. margin_min is the
    #    match margin used at scan time; it is unrelated to the "margin" key,
    #    which is the ArcFace training margin.
    config = json.loads((src / "config.json").read_text())
    config.pop("threshold", None)  # replaced by score_floor + margin_min
    config["score_floor"] = args.score_floor
    config["margin_min"] = args.margin_min
    (dest / "config.json").write_text(json.dumps(config, indent=2))
    print(f"config.json written (score_floor={args.score_floor}, margin_min={args.margin_min})")

    # 3. index.bin + search.onnx (same vectors: JS fallback and ORT MatMul agree)
    vecs = export_index(src / "index.faiss", dest / "index.bin")
    n, dim = vecs.shape
    labels = json.loads((src / "index_labels.json").read_text())
    assert len(labels) == n, f"labels ({len(labels)}) != vectors ({n})"
    size = (dest / "index.bin").stat().st_size
    assert size == len(MAGIC) + 12 + n * dim * 4
    print(f"index.bin written: {n} vectors x {dim} dims ({size / 1e6:.1f} MB)")
    export_search_onnx(vecs, dest / "search.onnx", fp16=args.fp16_search)

    # 4. images — one sub-directory per game, keyed by file stem (= card id),
    #    plus manifest.json so the app resolves any layout with one map lookup.
    if not args.skip_images:
        img_dest = dest / "images"
        img_dest.mkdir(exist_ok=True)
        sources = {}  # stem -> (game, source path)
        covers = {}  # game -> source path of its optional _cover.* art
        for game, d in image_dirs.items():
            for p in sorted(d.iterdir()):
                if p.suffix.lower() not in CARD_IMAGE_SUFFIXES:
                    continue
                # _cover.* is the game's tile artwork, not a card: converted
                # alongside the cards but kept out of the manifest.
                if p.stem == "_cover":
                    covers[game] = p
                    continue
                if p.stem in sources:
                    print(f"  WARNING: {p.stem} in {d} shadows {sources[p.stem][1].parent}")
                sources[p.stem] = (game, p)
        for game in sorted({g for g, _ in sources.values()}):
            (img_dest / game).mkdir(exist_ok=True)
        for game in covers:
            (img_dest / game).mkdir(exist_ok=True)
        jobs = [(p, img_dest / game / (stem + ".webp"), args.width)
                for stem, (game, p) in sorted(sources.items())]
        jobs += [(p, img_dest / game / "_cover.webp", args.width)
                 for game, p in sorted(covers.items())]
        done = 0
        with ProcessPoolExecutor(max_workers=8) as ex:
            for i, converted in enumerate(ex.map(convert_image, jobs, chunksize=16)):
                done += converted
                if (i + 1) % 1000 == 0:
                    print(f"  images: {i + 1}/{len(jobs)}")
        print(f"images: {done} converted, {len(jobs) - done} up-to-date ({len(jobs)} total)")

        manifest = {stem: f"{game}/{stem}.webp" for stem, (game, _) in sources.items()}
        manifest_path = img_dest / "manifest.json"
        manifest_path.write_text(json.dumps(manifest, indent=1, sort_keys=True))
        per_game = {g: sum(1 for gg, _ in sources.values() if gg == g)
                    for g in sorted({g for g, _ in sources.values()})}
        print(f"manifest.json written: {len(manifest)} entries {per_game}")

        # Files from a previous layout (e.g. the old flat images/<id>.webp) would
        # leave the app dir carrying two copies of every card — drop them.
        # Cover art (_cover.*) may also be dropped straight into the app dir by
        # hand, so anything with that stem survives the pruning.
        expected = {manifest_path} | {img_dest / rel for rel in manifest.values()}
        stale = [p for p in img_dest.rglob("*")
                 if p.is_file() and p not in expected and p.stem != "_cover"]
        if stale:
            if args.keep_stale:
                print(f"  {len(stale)} stale files kept (--keep-stale), e.g. {stale[0].name}")
            else:
                for p in stale:
                    p.unlink()
                print(f"  {len(stale)} stale files removed (previous layout)")

        # Every index label should have an image, or its preview will be blank.
        missing = [l for l in labels if l not in sources]
        if missing:
            print(f"  WARNING: {len(missing)} index labels have no source image "
                  f"(e.g. {missing[:5]}) — their preview will be blank")
        # ...and the reverse: an image nobody can scan is a silent coverage hole.
        warn_unindexed(manifest, labels)

    # 5. metadata — one file per game, kept next to (not under) images/ so it
    #    ships in the bundle core even when a game's image pack is absent.
    if not args.skip_metadata:
        meta_dest = dest / "metadata"
        meta_dest.mkdir(exist_ok=True)
        metadata: dict[str, dict] = {}
        for spec in args.metadata:
            game, _, f = spec.partition("=")
            if not game or not f:
                ap.error(f"--metadata expects GAME=FILE, got {spec!r}")
            src_file = Path(f)
            if not src_file.exists():
                print(f"  metadata: {game}: {src_file} not found, skipped")
                continue
            cards = json.loads(src_file.read_text(encoding="utf-8"))
            problems = metadata_problems(cards)
            if problems:
                ap.error(f"--metadata {game}: " + "; ".join(problems))
            metadata[game] = cards

        # Step 4 already built `manifest` in-memory when it ran; only hit the
        # disk copy under --skip-images, since it may be from a stale run.
        if args.skip_images:
            manifest_path = dest / "images" / "manifest.json"
            manifest = json.loads(manifest_path.read_text(encoding="utf-8")) if manifest_path.exists() else {}
            print("  metadata: --skip-images set, reusing images/manifest.json from a "
                  "previous run -- game attribution may be stale")
            warn_unindexed(manifest, labels)

        added = fill_variant_fallbacks(labels, manifest, metadata)
        for game, cards in metadata.items():
            n = added.get(game, 0)
            (meta_dest / f"{game}.json").write_text(
                json.dumps(cards, indent=1, sort_keys=True, ensure_ascii=False), encoding="utf-8")
            print(f"  metadata: {game}: {len(cards)} cards "
                  f"({n} variant ids filled from their base card)")

        not_in_manifest = [l for l in labels if manifest_game(manifest, l) == ""]
        if not_in_manifest:
            print(f"  metadata: {len(not_in_manifest)} index labels not in the manifest "
                  f"(not checked)")

        for game, missing in metadata_coverage(labels, manifest, metadata).items():
            print(f"  WARNING: {game}: {len(missing)} index labels without metadata "
                  f"(e.g. {missing[:5]})")

        # 6. Cardmarket mapping — one file for every game, next to the
        #    metadata. Coverage is reported per game against the index
        #    labels, and a game that would lose coverage against the bundle's
        #    current file stops the build: a broken upstream must not ship
        #    quietly as "no prices".
        cm_src = Path(args.cardmarket)
        cm_dest = meta_dest / "cardmarket.json"
        if not cm_src.exists():
            print(f"  cardmarket: {cm_src} not found, skipped (the app will show no prices)")
        else:
            new = price_coverage(labels, manifest, json.loads(cm_src.read_text(encoding="utf-8")))
            old = (price_coverage(labels, manifest, json.loads(cm_dest.read_text(encoding="utf-8")))
                   if cm_dest.exists() else {})
            regressed = []
            for game, (priced, total) in sorted(new.items()):
                was = old.get(game, (0, total))[0]
                note = f" (was {was})" if old and was != priced else ""
                print(f"  cardmarket: {game}: {priced}/{total} index labels priced{note}")
                if priced < was:
                    regressed.append(f"{game} {was} -> {priced}")
            if regressed and not args.allow_price_regression:
                ap.error("cardmarket.json prices fewer labels than the shipped one: "
                         + "; ".join(regressed) + " (pass --allow-price-regression to ship anyway)")
            shutil.copyfile(cm_src, cm_dest)

    print("done.")


if __name__ == "__main__":
    main()
