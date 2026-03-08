"""
Card Recognition - Training Script
MobileNetV2 transfer learning → TFLite export

Usage:
    pip install tensorflow numpy
    python train.py --name riftbound
    python train.py --name riftbound --epochs 30 --dense 256

Directory layout:
    datasets/riftbound/         <- input (from augment.py)
        ogn-001-298/
            ogn-001-298_000.jpg
            ogn-001-298_001.jpg
            ...
        ogn-002-298/
            ...
    models/riftbound/           <- output
        model.keras               (full model for retraining)
        model.tflite              (quantized for Go inference)
        labels.json               (class index -> card ID)
"""

import argparse
import json
import os

import numpy as np
import tensorflow as tf
from tensorflow import keras


IMG_SIZE = 224  # MobileNetV2 input size
BATCH_SIZE = 32
FINE_TUNE_LAYERS = 30  # unfreeze last N layers of MobileNetV2


def load_datasets(dataset_dir, validation_split=0.2):
    """Load dataset with train/val split."""
    train_ds = keras.utils.image_dataset_from_directory(
        dataset_dir,
        validation_split=validation_split,
        subset="training",
        seed=42,
        image_size=(IMG_SIZE, IMG_SIZE),
        batch_size=BATCH_SIZE,
        label_mode="int",
    )

    val_ds = keras.utils.image_dataset_from_directory(
        dataset_dir,
        validation_split=validation_split,
        subset="validation",
        seed=42,
        image_size=(IMG_SIZE, IMG_SIZE),
        batch_size=BATCH_SIZE,
        label_mode="int",
    )

    class_names = train_ds.class_names
    num_classes = len(class_names)
    print(f"Classes: {num_classes}")
    print(f"Train batches: {len(train_ds)}")
    print(f"Val batches: {len(val_ds)}")

    # Prefetch for performance
    train_ds = train_ds.prefetch(tf.data.AUTOTUNE)
    val_ds = val_ds.prefetch(tf.data.AUTOTUNE)

    return train_ds, val_ds, class_names, num_classes


def build_model(num_classes, dense_units=512):
    """MobileNetV2 + custom head."""
    # MobileNetV2 expects [-1, 1] input
    preprocess = keras.applications.mobilenet_v2.preprocess_input

    base_model = keras.applications.MobileNetV2(
        input_shape=(IMG_SIZE, IMG_SIZE, 3),
        include_top=False,
        weights="imagenet",
    )
    base_model.trainable = False  # freeze for phase 1

    model = keras.Sequential([
        keras.layers.Input(shape=(IMG_SIZE, IMG_SIZE, 3)),
        keras.layers.Lambda(preprocess),
        base_model,
        keras.layers.GlobalAveragePooling2D(),
        keras.layers.Dropout(0.3),
        keras.layers.Dense(dense_units, activation="relu"),
        keras.layers.Dropout(0.2),
        keras.layers.Dense(num_classes, activation="softmax"),
    ])

    return model, base_model


def train_phase1(model, train_ds, val_ds, epochs=10):
    """Phase 1: Train only the head (base frozen)."""
    print("\n=== Phase 1: Training head (base frozen) ===")

    model.compile(
        optimizer=keras.optimizers.Adam(learning_rate=1e-3),
        loss="sparse_categorical_crossentropy",
        metrics=["accuracy"],
    )

    history = model.fit(
        train_ds,
        validation_data=val_ds,
        epochs=epochs,
        callbacks=[
            keras.callbacks.EarlyStopping(
                monitor="val_accuracy",
                patience=3,
                restore_best_weights=True,
            ),
        ],
    )
    return history


def train_phase2(model, base_model, train_ds, val_ds, epochs=10):
    """Phase 2: Fine-tune last layers of base model."""
    print(f"\n=== Phase 2: Fine-tuning last {FINE_TUNE_LAYERS} layers ===")

    base_model.trainable = True
    for layer in base_model.layers[:-FINE_TUNE_LAYERS]:
        layer.trainable = False

    model.compile(
        optimizer=keras.optimizers.Adam(learning_rate=1e-4),  # lower LR
        loss="sparse_categorical_crossentropy",
        metrics=["accuracy"],
    )

    history = model.fit(
        train_ds,
        validation_data=val_ds,
        epochs=epochs,
        callbacks=[
            keras.callbacks.EarlyStopping(
                monitor="val_accuracy",
                patience=3,
                restore_best_weights=True,
            ),
            keras.callbacks.ReduceLROnPlateau(
                monitor="val_loss",
                factor=0.5,
                patience=2,
            ),
        ],
    )
    return history


def export_tflite(model, output_path):
    """Convert to TFLite."""
    print(f"\nExporting TFLite model to {output_path}...")

    converter = tf.lite.TFLiteConverter.from_keras_model(model)
    converter.optimizations = [tf.lite.Optimize.DEFAULT]  # quantize for size/speed
    tflite_model = converter.convert()

    with open(output_path, "wb") as f:
        f.write(tflite_model)

    size_mb = os.path.getsize(output_path) / (1024 * 1024)
    print(f"TFLite model saved: {size_mb:.1f} MB")


def save_labels(class_names, output_path):
    """Save class name mapping for inference."""
    mapping = {i: name for i, name in enumerate(class_names)}
    with open(output_path, "w") as f:
        json.dump(mapping, f, indent=2)
    print(f"Labels saved to {output_path} ({len(mapping)} classes)")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--name", required=True, help="Model name (e.g. riftbound)")
    parser.add_argument("--epochs", type=int, default=20, help="Epochs per phase")
    parser.add_argument("--dense", type=int, default=512, help="Dense layer units (default: 512)")
    args = parser.parse_args()

    dataset_dir = os.path.join("datasets", args.name)
    output_dir = os.path.join("models", args.name)

    if not os.path.isdir(dataset_dir):
        print(f"Dataset directory not found: {dataset_dir}")
        print(f"  Run augment.py --name {args.name} first")
        return

    os.makedirs(output_dir, exist_ok=True)

    print(f"Model: {args.name}")
    print(f"Dataset: {dataset_dir}/")
    print(f"Output: {output_dir}/")

    print("GPU available:", tf.config.list_physical_devices("GPU"))

    train_ds, val_ds, class_names, num_classes = load_datasets(dataset_dir)

    model, base_model = build_model(num_classes, dense_units=args.dense)
    model.summary()

    train_phase1(model, train_ds, val_ds, epochs=args.epochs)

    train_phase2(model, base_model, train_ds, val_ds, epochs=args.epochs)

    print("\n=== Final Evaluation ===")
    loss, acc = model.evaluate(val_ds)
    print(f"Val accuracy: {acc:.4f}")

    keras_path = os.path.join(output_dir, "model.keras")
    model.save(keras_path)
    print(f"Keras model saved to {keras_path}")

    export_tflite(model, os.path.join(output_dir, "model.tflite"))
    save_labels(class_names, os.path.join(output_dir, "labels.json"))

    print("\nDone! Files in", output_dir + "/")
    print("  model.keras   - full Keras model (for retraining)")
    print("  model.tflite  - quantized TFLite (for Go inference)")
    print("  labels.json   - class index → card ID mapping")


if __name__ == "__main__":
    main()
