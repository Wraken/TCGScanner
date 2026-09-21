# Retraining the embedding model

New cards never need a retrain: embed their reference with `add_to_index.py`.
Retrain when the reference set itself changed a lot (the August 2026 model saw
2,870 cards: One Piece March pull + Riftbound; the index now holds 30,999 cards
of four games) or when matching quality on real scans is the problem.

Everything below runs in WSL (`Ubuntu-22.04`, user `wraken`, `conda activate
riftbound`: TensorFlow 2.20, Keras 3.13, albumentations, faiss, tf2onnx,
onnxruntime) on the RTX 3080 (10 GB), from `/mnt/e/TCGScanner/scripts/ml`.
Use `tmux` so the run survives a closed terminal.

Pick a **new model name** (`tcg-full` below). `--name tcg` would overwrite
`models/tcg/`, the model behind the live index.

## 0. Decisions for the September 2026 run

- Recipe identical to August: EfficientNetV2-S at 384 px, 512-d ArcFace
  (margin 0.5, scale 64), 70 augmented views per card (50 train, 20 val + the
  original in val), 20 epochs per phase with the existing early stopping.
- **One class per card id**, printings with identical artwork included. The
  variant-choice popup handles twins at the index level, so the training set
  does not fold them.
- References = exactly the files behind the current index (30,999):

  | dir | cards |
  |---|---|
  | `images/onepiece` (SAMPLE removed, English scans) | 4,843 |
  | `images/riftbound` | 1,189 |
  | `../fetch/lorcana/images` | 3,191 |
  | `../fetch/pokemon/images` | 21,776 |

## 1. Augment

The dataset goes to `datasets/tcg-full` on E:, like the August one. **Never
write it into the WSL home**: the Linux disk of WSL is a single `ext4.vhdx`
file on C: that grows on demand, and `df` inside WSL reports the virtual
size, not the free space of C:. On 2026-09-08 a first attempt into
`~/datasets` filled C: at 58 %, ext4 remounted read-only and the distro would
not restart until `wsl --shutdown`. E: through the 9p mount measured
1,300 files/s read (16 parallel readers) and ~690 files/s write, enough for
the ~600 images/s the GPU consumes.

```bash
tmux new -s train
cd /mnt/e/TCGScanner/scripts/ml && conda activate riftbound
python augment.py \
  --input images/onepiece images/riftbound ../fetch/lorcana/images ../fetch/pokemon/images \
  --output datasets/tcg-full --backbone efficientnet-v2-s \
  --per-card 70 --val-per-card 20
```

Measured 2026-09-08: 30,999 classes, 2,200,929 JPEGs (50 train + 21 val per
card), **1 h 53 min** at 4.6 cards/s with 24 workers, the write rate of the
9p mount being the limit (on the WSL disk it ran at 18 cards/s). Size
~130 GB (4.25 MB per card).

## 2. Train

```bash
mkdir -p models/tcg-full
python train.py --name tcg-full --backbone efficientnet-v2-s --epochs 20 \
  2>&1 | tee models/tcg-full/train.log
```

`--dataset-dir` exists to point at a dataset stored elsewhere; the default
`datasets/tcg-full` is the right one here. Writes `models/tcg-full/`:
`phase1.weights.h5` / `phase2.weights.h5` (best val_loss of each phase, saved
at every improving epoch), `train_log.csv` (one line per epoch, both phases),
then at the end `embedding_model.keras`, `embedding_model.onnx` (float16
tainted, see step 3), `labels.json`, `config.json`.

Timing, measured on a 40-card smoke run with the dataset on the WSL ext4 disk
(batch 32, mixed precision); with the dataset on E: the 9p reads keep up on
paper (see above) but were not measured over a full epoch, so take it with a
±30 % margin and check the first epoch's speed against it:

| | per step (32 images) | per epoch (1.55 M train + 651 k val) |
|---|---|---|
| phase 1 (head only) | 54 ms | ~1 h |
| phase 2 (last 150 layers) | 61 ms | ~1 h |
| validation pass | 40 ms | included above (~14 min) |

Worst case 20 + 20 epochs ≈ **40 h**; early stopping (patience 4 then 5 on
val_loss) usually ends each phase earlier. The first step of a run spends
about a minute in XLA compilation.

Follow progress with `tail -f models/tcg-full/train.log`, `cat
models/tcg-full/train_log.csv` and `nvidia-smi`. Keep Windows from sleeping.

If the run dies (reboot, WSL shut down), resume from the best checkpoint
instead of starting over:

```bash
# phase 1 finished or was interrupted: skip it, run phase 2 from its best weights
python train.py --name tcg-full --backbone efficientnet-v2-s --epochs 20 \
  --resume models/tcg-full/phase1.weights.h5
# phase 2 was interrupted: restart phase 2 from its best weights
python train.py ... --resume models/tcg-full/phase2.weights.h5
# just export what phase 2 reached, no more training
python train.py ... --resume models/tcg-full/phase2.weights.h5 --epochs 0
```

The checkpoints hold weights only, so a resumed phase 2 restarts its Adam
state and epoch count; at 5e-5 that is harmless.

## 3. Export a float32 ONNX

The ONNX written by `train.py` carries float16 ops from mixed precision and
the Go side reads it as float32 garbage. Always re-export:

```bash
python reexport_onnx_fp32.py --model models/tcg-full/embedding_model.keras \
  --output models/tcg-full/embedding_model.onnx
```

It asserts parity (cosine > 0.9999) between Keras and the ONNX output.

## 4. Build the index

```bash
python build_index.py --name tcg-full \
  --references images/onepiece images/riftbound ../fetch/lorcana/images ../fetch/pokemon/images
```

References are decoded one at a time (31 k images at 384 px in float32 would
be 55 GB in RAM), each embedded as the mean of 8 lightly augmented views, the
same recipe as `add_to_index.py`. About 30 minutes. Expect auto-retrieval
100 %; anything less names the colliding pairs.

## 5. Compare with the current model before touching anything

- `models/tcg-full/train_log.csv`: val accuracy of the last phase-2 epoch
  (the August model's is not recorded; the smoke run reached 0.99 on 40
  classes after one epoch, so expect a high number and look at the trend).
- Twin statistics, same tool as the baseline of 2026-08-26:

  ```bash
  python tools/index_twins.py --index models/tcg-full/index.faiss \
    --labels models/tcg-full/index_labels.json
  ```

  Current model: Lorcana 18.8 %, Pokémon 9.8 %, One Piece 6.4 %, Riftbound
  5.9 % of cards have a neighbour at cosine ≥ 0.9. Identical-art printings
  must stay above 0.9 (the popup relies on it); what should drop is the list
  of tightest pairs with *different* artwork that the tool prints per game.
- Package the candidate next to the live bundle and try it in the app; the
  model selector lists every directory under `models/`:

  ```bash
  cp models/tcg/yolo.onnx models/tcg-full/
  python package_app_model.py --src models/tcg-full --dest ../../models/tcg-full
  ```

  Then, from the repo root, the Go matcher tests against the candidate:

  ```bash
  TCG_TEST_MODEL_DIR=models/tcg-full go test ./...
  ```

  and a live pass in `wails dev` on the usual cards: a One Piece base card
  (OP01–OP08), an OP15–17 card, a Riftbound star, a Lorcana reprint that
  opens the popup, a Pokémon energy.

## Run log: September 2026

- **Phase 1** plateaued from its first epoch (val_loss 0.2485 → 0.2484 over 8
  epochs, val accuracy 0.943): with a frozen backbone the ArcFace centres fit
  in one pass and later epochs only sharpen the margin. A Windows crash at
  04:18 killed the run at epoch 9; `--resume phase1.weights.h5` skipped
  straight to phase 2 with no loss.
- **Phase 2** converged at epoch 2 (val_loss 0.1770, val accuracy 0.961,
  train loss flat at 15.44) and still ran to epoch 12: improvements of 1e-5
  reset `EarlyStopping`, whose `min_delta` is 0. Ten epochs (~9 h) bought
  nothing. Next run: `min_delta=1e-3` on both EarlyStopping callbacks.
- **Timings** with the dataset on E: through 9p: 38–50 ms/step, 45–55 min per
  epoch including validation; the 9p mount never limited the GPU. A CS2
  session sharing the GPU tripled the step time for one epoch.
- **Validation is optimistic**: the val split is augmented views of the same
  cards, so 0.961 measures self-recognition, not generalisation to new cards
  or to webcam frames. The index twin statistics and the live pass are the
  real test.
- **Index** (`build_index.py`, streaming, 63 min for 30,999 references over
  9p): auto-retrieval 30,999/30,999.
- **Twin statistics** (`tools/index_twins.py`, nearest *other* canonical card,
  cosine): the new model separates different cards much better on the two
  games the August model had never seen, and leaves the identical-art
  reprints where the popup needs them (≥ 0.9).

  | game | twins ≥ 0.9, August model | twins ≥ 0.9, new model | median nearest-other, August → new |
  |---|---|---|---|
  | Pokémon | 9.8 % | 6.6 % | 0.707 → 0.552 |
  | Lorcana | 18.8 % | 15.6 % | 0.617 → 0.510 |
  | One Piece | 0 % | 0 % | 0.505 → 0.480 |
  | Riftbound | 1.0 % | 1.0 % | 0.481 → 0.497 |
  | total | 8.9 % | 6.3 % | |

  The tightest pairs are the same genuine reprints in both (Lorcana 9-167 ~
  3-163 at 0.99, Riftbound ven-r ~ ogn at 0.97–0.98), so the drop is in
  look-alikes with different artwork, which become lockable.
- **Recognition under synthetic distortion** (200 random cards per game, 5
  augmented validation views each, never the `_000` original; each model
  searches its own index; lock = app rule, score ≥ 0.4 and margin ≥ 0.1
  against the best different id; ±1–1.5 pt of sampling noise):

  | game | top-1 August → new | lock rate August → new | median margin August → new |
  |---|---|---|---|
  | Pokémon | 96.5 → 98.4 % | 81.9 → **89.4 %** | 0.23 → 0.37 |
  | Lorcana | 94.7 → 94.0 % | 74.7 → **79.5 %** | 0.28 → 0.36 |
  | One Piece | 95.7 → 93.4 % (99.9 → 99.8 % up to a `_pN` printing) | 76.4 → 71.5 % | 0.35 → 0.34 |
  | Riftbound | 97.9 → 97.5 % | 90.3 → 89.1 % | 0.40 → 0.37 |
  | all, unweighted | 96.2 → 95.8 % | 80.8 → 82.4 % | |

  The August model had 2,870 classes of dedicated capacity for One Piece and
  Riftbound; the new one spreads 30,999 classes and trades a few points on
  those two games (One Piece's strict top-1 loss is between printings of the
  same artwork, which the popup handles) for large gains on the two games it
  is the first to have trained on. Weighted by catalogue size (Pokémon is
  70 % of the cards) the lock rate goes from 80.6 % to 85.6 %.
- **Live pass** in the app on 2026-09-10 (candidate packaged as
  `models/tcg-full`, picked in the model selector): convincing. **Promoted on
  2026-09-20** by rename on both sides; the August model is kept as
  `tcg.pre-retrain-2026-09` (scripts and app) until it is deleted.

## 6. Promote or discard

Promote by renaming, on both sides, keeping the old model until you are sure:

```bash
mv models/tcg ../../models/tcg.pre-retrain-2026-09 && mv models/tcg-full models/tcg      # scripts side
cd ../.. && mv models/tcg models/tcg.pre-retrain-2026-09 && mv models/tcg-full models/tcg  # app side
```

Rollback is the reverse rename. Discarding is `rm -rf` of the two `tcg-full`
directories and `datasets/tcg-full`.
