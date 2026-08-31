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
                                       one sub-directory per game
  - images/manifest.json             : card id -> path relative to images/, read
                                       by the app (one map lookup, no dir walk)

Re-runnable: only missing/outdated images are converted, and files left over
from a previous layout are removed. Use after every add_to_index.py run to
refresh the app copy.
"""
import argparse
import json
import shutil
import struct
from concurrent.futures import ProcessPoolExecutor
from pathlib import Path

MAGIC = b"TCGEMBIX"
VERSION = 1


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


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", default="models/tcg", help="source model dir (scripts side)")
    ap.add_argument("--images", nargs="+", metavar="GAME=DIR",
                    default=["onepiece=images/onepiece", "riftbound=images/riftbound",
                             "lorcana=../fetch/lorcana/images", "pokemon=../fetch/pokemon/images"],
                    help="source reference image dirs, one per game; images land in "
                         "images/<game>/ (labels are matched by file stem)")
    ap.add_argument("--keep-stale", action="store_true",
                    help="do not delete files under images/ that the manifest no longer references")
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
        for spec in args.images:
            game, _, d = spec.partition("=")
            if not game or not d:
                ap.error(f"--images expects GAME=DIR, got {spec!r}")
            for p in sorted(Path(d).iterdir()):
                if p.suffix.lower() not in {".png", ".jpg", ".jpeg", ".webp"}:
                    continue
                if p.stem in sources:
                    print(f"  WARNING: {p.stem} in {d} shadows {sources[p.stem][1].parent}")
                sources[p.stem] = (game, p)
        for game in sorted({g for g, _ in sources.values()}):
            (img_dest / game).mkdir(exist_ok=True)
        jobs = [(p, img_dest / game / (stem + ".webp"), args.width)
                for stem, (game, p) in sorted(sources.items())]
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
        expected = {manifest_path} | {img_dest / rel for rel in manifest.values()}
        stale = [p for p in img_dest.rglob("*") if p.is_file() and p not in expected]
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

    print("done.")


if __name__ == "__main__":
    main()
