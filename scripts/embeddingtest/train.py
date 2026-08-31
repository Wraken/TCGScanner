"""
Card Recognition - Training Script (Embedding + ArcFace)

Au lieu d'un classifier (softmax → proba par carte), ce script entraîne un modèle
à produire des embeddings : des vecteurs 512D où les cartes identiques sont proches
et les cartes différentes sont éloignées. La reconnaissance se fait ensuite via
une recherche de plus proche voisin dans un index FAISS.

Avantages vs classifier :
    - Nouvelles cartes → juste ajouter leurs embeddings dans l'index (pas de réentraînement)
    - Fonctionne avec une seule image de référence par carte
    - Gère plusieurs TCGs dans un seul modèle
    - Peut rejeter une carte inconnue (score faible)

Backbones supportés :
    convnext-tiny    224x224  (recommandé - meilleur pour fine-grained recognition)
    mobilenetv2      224x224  (léger, moins précis)
    efficientnet-b0  224x224  (bon compromis taille/précision)
    efficientnet-b3  300x300  (précis mais plus lourd)

Usage :
    pip install tensorflow numpy tf2onnx onnx
    python train.py --name riftbound
    python train.py --name riftbound --backbone convnext-tiny
    python train.py --name riftbound --backbone convnext-tiny --epochs 30

Structure attendue :
    datasets/riftbound/
        train/
            ogn-001-298/
                ogn-001-298_001.jpg
                ...
        val/
            ogn-001-298/
                ogn-001-298_000.jpg
                ...

Sortie dans models/riftbound/ :
    embedding_model.keras    → modèle Keras complet (pour réentraînement)
    embedding_model.onnx     → ONNX pour inférence Go (onnxruntime_go)
    embedding_model.tflite   → TFLite pour mobile (Android/iOS)
    labels.json              → index classe → card ID (pour référence)
    config.json              → hyperparamètres sauvegardés

Étape suivante : python build_index.py --name riftbound
"""

import argparse
import json
import math
import os

import numpy as np
import tensorflow as tf
from tensorflow import keras


# ─── Constantes ───────────────────────────────────────────────────────────────

BATCH_SIZE = 32

BACKBONES = {
    "convnext-tiny": {
        "class": keras.applications.ConvNeXtTiny,
        "preprocess": keras.applications.convnext.preprocess_input,
        "img_size": 224,
        "fine_tune_layers": 50,   # ConvNeXt a plus de couches que EfficientNet
    },
    "mobilenetv2": {
        "class": keras.applications.MobileNetV2,
        "preprocess": keras.applications.mobilenet_v2.preprocess_input,
        "img_size": 224,
        "fine_tune_layers": 30,
    },
    "efficientnet-b0": {
        "class": keras.applications.EfficientNetB0,
        "preprocess": keras.applications.efficientnet.preprocess_input,
        "img_size": 224,
        "fine_tune_layers": 30,
    },
    "efficientnet-b3": {
        "class": keras.applications.EfficientNetB3,
        "preprocess": keras.applications.efficientnet.preprocess_input,
        "img_size": 300,
        "fine_tune_layers": 30,
    },
    # Recommended for ONNX export — uses Swish (no Erfc/GELU issues), exports cleanly
    # 384x384 matches the ImageNet pretraining resolution for EfficientNetV2-S
    # 150 out of 513 layers (~30%) gives a good balance of plasticity vs stability
    "efficientnet-v2-s": {
        "class": keras.applications.EfficientNetV2S,
        "preprocess": keras.applications.efficientnet_v2.preprocess_input,
        "img_size": 384,
        "fine_tune_layers": 150,
    },
}


# ─── Couche ArcFace ────────────────────────────────────────────────────────────

class ArcFaceLayer(keras.layers.Layer):
    """
    ArcFace : Additive Angular Margin Loss (CVPR 2019).

    Principe : pendant l'entraînement, ajoute une marge angulaire `margin`
    (en radians) entre l'embedding de la carte et les autres classes.
    Ça force le modèle à apprendre des représentations bien séparées,
    comme si tu exigeais un écart minimum entre chaque groupe de cartes
    dans l'espace vectoriel.

    Sans ArcFace : le modèle apprend juste à distinguer les classes.
    Avec ArcFace : le modèle apprend à *maximiser la distance* entre classes.

    Paramètres :
        margin : marge angulaire (défaut 0.5 rad ≈ 28°) - plus élevé = plus strict
        scale  : température de la softmax (défaut 64.0) - plus élevé = plus confiant
    """

    def __init__(self, num_classes, margin=0.5, scale=64.0, **kwargs):
        kwargs["dtype"] = "float32"  # ArcFace needs float32 for numerical stability (cos/sin/sqrt)
        super().__init__(**kwargs)
        self.num_classes = num_classes
        self.margin = margin
        self.scale = scale
        # Pré-calcul pour l'efficacité
        self.cos_m = math.cos(margin)
        self.sin_m = math.sin(margin)
        self.threshold = math.cos(math.pi - margin)
        self.mm = math.sin(math.pi - margin) * margin

    def build(self, input_shape):
        # W : une colonne par classe = centre de chaque cluster dans l'espace d'embedding
        self.W = self.add_weight(
            name="arcface_weights",
            shape=(input_shape[-1], self.num_classes),
            initializer="glorot_uniform",
            trainable=True,
        )

    def call(self, embeddings, labels=None):
        # Normalise les centres de classes (L2) → on travaille sur une sphère
        W_norm = tf.nn.l2_normalize(self.W, axis=0)

        # Similarité cosinus entre l'embedding et chaque centre de classe
        # Les embeddings sont déjà L2-normalisés → cosine = simple produit scalaire
        cosine = tf.matmul(embeddings, W_norm)  # shape: (batch, num_classes)

        if labels is None:
            # Mode inférence : pas de marge, retourne les similarités cosinus brutes
            return self.scale * cosine

        # Mode entraînement : ajoute la marge angulaire sur la bonne classe
        # cos(θ + m) = cos·cos_m - sin·sin_m
        sine = tf.sqrt(tf.maximum(1.0 - tf.square(cosine), 1e-9))
        cos_theta_m = cosine * self.cos_m - sine * self.sin_m

        # Sécurité numérique : si θ > π - m, on utilise une approximation linéaire
        cos_theta_m = tf.where(cosine > self.threshold, cos_theta_m, cosine - self.mm)

        # Applique la marge uniquement sur la classe correcte (one-hot)
        one_hot = tf.one_hot(tf.cast(labels, tf.int32), self.num_classes)
        logits = one_hot * cos_theta_m + (1.0 - one_hot) * cosine

        return self.scale * logits

    def get_config(self):
        config = super().get_config()
        config.update({
            "num_classes": self.num_classes,
            "margin": self.margin,
            "scale": self.scale,
        })
        return config


# ─── Modèle ───────────────────────────────────────────────────────────────────

class CardEmbeddingModel(keras.Model):
    """
    Modèle d'embedding avec tête ArcFace pour l'entraînement.

    Architecture :
        Image (224x224x3)
            ↓
        Backbone (ConvNeXt-Tiny, frozen en phase 1)
            ↓
        GlobalAveragePooling2D
            ↓
        Dropout(0.3)
            ↓
        Dense(embedding_dim, sans biais)
            ↓
        BatchNormalization
            ↓
        L2 Normalize  ← l'embedding final (vecteur unitaire)
            ↓
        [ArcFaceLayer]  ← seulement pendant l'entraînement
            ↓
        Logits → SparseCategoricalCrossentropy

    À l'export : on garde uniquement jusqu'à L2 Normalize.
    C'est ce modèle épuré (sans ArcFace) qu'on exporte en ONNX/TFLite.
    """

    def __init__(self, base_model, num_classes, embedding_dim=512, margin=0.5, scale=64.0):
        super().__init__()
        self.base_model = base_model
        self.pool = keras.layers.GlobalAveragePooling2D()
        self.dropout = keras.layers.Dropout(0.3)
        # use_bias=False : convention ArcFace (le biais ne contribue pas au cosinus)
        self.embedding_dense = keras.layers.Dense(embedding_dim, use_bias=False, name="embedding_dense")
        self.bn = keras.layers.BatchNormalization(name="embedding_bn")
        self.arcface = ArcFaceLayer(num_classes, margin=margin, scale=scale, name="arcface")
        self.num_classes = num_classes
        self.embedding_dim = embedding_dim

    def compute_embeddings(self, images, training=False):
        """Produit un vecteur L2-normalisé de taille embedding_dim."""
        x = self.base_model(images, training=training)
        x = self.pool(x)
        x = self.dropout(x, training=training)
        x = self.embedding_dense(x)
        x = self.bn(x, training=training)
        return keras.ops.normalize(x, axis=1)

    def train_step(self, data):
        """
        Étape d'entraînement personnalisée.
        On passe (images, labels) → embedding → ArcFace → CrossEntropy.
        """
        images, labels = data

        with tf.GradientTape() as tape:
            embeddings = self.compute_embeddings(images, training=True)
            logits = self.arcface(embeddings, labels)  # avec marge angulaire
            loss = self.compute_loss(y=labels, y_pred=logits)

        grads = tape.gradient(loss, self.trainable_variables)
        self.optimizer.apply_gradients(zip(grads, self.trainable_variables))
        logits_f32 = tf.cast(logits, tf.float32)
        for metric in self.metrics:
            if metric.name == "loss":
                metric.update_state(loss)
            else:
                metric.update_state(labels, logits_f32)
        return {m.name: m.result() for m in self.metrics}

    def test_step(self, data):
        """
        Étape de validation.
        Pas de marge angulaire pour la validation → similarités cosinus brutes.
        L'accuracy affichée est donc légèrement inférieure à l'accuracy réelle
        (qui se mesure via FAISS dans build_index.py).
        """
        images, labels = data
        embeddings = self.compute_embeddings(images, training=False)
        logits = self.arcface(embeddings, labels=None)  # sans marge
        loss = self.compute_loss(y=labels, y_pred=logits)
        logits_f32 = tf.cast(logits, tf.float32)
        for metric in self.metrics:
            if metric.name == "loss":
                metric.update_state(loss)
            else:
                metric.update_state(labels, logits_f32)
        return {m.name: m.result() for m in self.metrics}

    def call(self, inputs, training=False):
        """Appel par défaut : retourne l'embedding (mode inférence)."""
        return self.compute_embeddings(inputs, training=training)

    def get_embedding_model(self, img_size):
        """
        Construit et retourne un modèle Keras fonctionnel propre,
        sans la couche ArcFace. C'est ce modèle qu'on exporte en ONNX/TFLite.
        """
        inputs = keras.Input(shape=(img_size, img_size, 3), name="input_image")
        outputs = self.compute_embeddings(inputs, training=False)
        return keras.Model(inputs, outputs, name="card_embedding_model")


# ─── Dataset ──────────────────────────────────────────────────────────────────

def load_datasets(dataset_dir, img_size, preprocess_fn):
    """
    Charge les datasets train/val avec le preprocessing dans le pipeline de données.

    CHANGEMENT vs ancienne version :
    Le preprocessing n'est plus dans le modèle via une Lambda layer.
    Il est appliqué ici via .map() sur le dataset tf.data.

    Pourquoi c'est important :
    - Les Lambda layers se sérialisent mal → risque de crash à l'export TFLite/ONNX
    - Le preprocessing dans le pipeline est plus rapide (CPU pendant que le GPU calcule)
    - À l'export, le modèle ne reçoit que des images float32 déjà normalisées
    """
    train_dir = os.path.join(dataset_dir, "train")
    val_dir = os.path.join(dataset_dir, "val")

    def apply_preprocessing(images, labels):
        images = tf.cast(images, tf.float32)
        return preprocess_fn(images), labels

    train_ds = keras.utils.image_dataset_from_directory(
        train_dir,
        image_size=(img_size, img_size),
        batch_size=BATCH_SIZE,
        label_mode="int",
        shuffle=True,
    )

    val_ds = keras.utils.image_dataset_from_directory(
        val_dir,
        image_size=(img_size, img_size),
        batch_size=BATCH_SIZE,
        label_mode="int",
        shuffle=False,
    )

    class_names = train_ds.class_names
    num_classes = len(class_names)
    print(f"Classes  : {num_classes}")
    print(f"Batches train : {len(train_ds)}")
    print(f"Batches val   : {len(val_ds)}")

    train_ds = (
        train_ds
        .map(apply_preprocessing, num_parallel_calls=tf.data.AUTOTUNE)
        .prefetch(tf.data.AUTOTUNE)
    )
    val_ds = (
        val_ds
        .map(apply_preprocessing, num_parallel_calls=tf.data.AUTOTUNE)
        .prefetch(tf.data.AUTOTUNE)
    )

    return train_ds, val_ds, class_names, num_classes


# ─── Entraînement ─────────────────────────────────────────────────────────────

def train_phase1(model, train_ds, val_ds, epochs=10, warmup_epochs=3):
    """
    Phase 1 : entraîne uniquement la tête (backbone gelé).

    CHANGEMENT : ajout d'un warmup du learning rate sur les 3 premières epochs.
    Raison : démarrer directement à lr=1e-3 peut créer de grandes oscillations
    dès le début. Le warmup monte progressivement de 0 à lr_max en warmup_epochs.
    C'est surtout utile avec ArcFace dont les gradients sont plus forts qu'une
    CrossEntropy classique.
    """
    print("\n=== Phase 1 : entraînement de la tête (backbone gelé) ===")

    model.base_model.trainable = False
    lr_max = 1e-3

    def lr_schedule(epoch):
        if epoch < warmup_epochs:
            return lr_max * (epoch + 1) / warmup_epochs
        return lr_max

    model.compile(
        optimizer=keras.optimizers.Adam(learning_rate=lr_max, clipnorm=1.0),
        loss=keras.losses.SparseCategoricalCrossentropy(from_logits=True),
        metrics=["accuracy"],
    )

    return model.fit(
        train_ds,
        validation_data=val_ds,
        epochs=epochs,
        callbacks=[
            keras.callbacks.LearningRateScheduler(lr_schedule, verbose=1),
            keras.callbacks.EarlyStopping(
                monitor="val_loss",   # val_accuracy absent avec train_step custom
                mode="min",
                patience=4,
                restore_best_weights=True,
            ),
        ],
    )


def train_phase2(model, train_ds, val_ds, epochs=10, fine_tune_layers=50):
    """
    Phase 2 : dégèle les dernières couches du backbone pour le fine-tuning.

    CHANGEMENTS :
    - LR réduit à 1e-5 (était 1e-4) : le fine-tuning ConvNeXt demande
      un LR très faible pour ne pas détruire les features pré-entraînées.
    - patience EarlyStopping : 3 → 5
      Raison : le fine-tuning peut stagner plusieurs epochs avant de repartir.
      Avec patience=3 on risquait d'arrêter trop tôt.
    """
    print(f"\n=== Phase 2 : fine-tuning des {fine_tune_layers} dernières couches ===")

    model.base_model.trainable = True
    for layer in model.base_model.layers[:-fine_tune_layers]:
        layer.trainable = False

    # Fige TOUTES les BatchNorm du backbone (poids gamma/beta ET stats).
    # Une BN trainable en mode training=True ecrase ses moyennes/variances
    # ImageNet avec celles de nos batches de 32 cartes -> les stats derivent,
    # les couches conv d'origine ne les reconnaissent plus, et la phase 2
    # plafonne ou degrade l'embedding (recette officielle Keras du
    # fine-tuning : BN toujours en inference). La BN de la tete
    # (embedding_bn) reste entrainable, elle n'a jamais connu ImageNet.
    frozen_bn = 0
    for layer in model.base_model.layers:
        if isinstance(layer, keras.layers.BatchNormalization):
            layer.trainable = False
            frozen_bn += 1

    trainable_count = sum(1 for l in model.base_model.layers if l.trainable)
    print(f"  Couches backbone entraînables : {trainable_count}/{len(model.base_model.layers)}")
    print(f"  BatchNorm figées : {frozen_bn}")

    model.compile(
        optimizer=keras.optimizers.Adam(learning_rate=5e-5, clipnorm=1.0),
        loss=keras.losses.SparseCategoricalCrossentropy(from_logits=True),
        metrics=["accuracy"],
    )

    return model.fit(
        train_ds,
        validation_data=val_ds,
        epochs=epochs,
        callbacks=[
            keras.callbacks.EarlyStopping(
                monitor="val_loss",   # val_accuracy absent avec train_step custom
                mode="min",
                patience=5,
                restore_best_weights=True,
            ),
            keras.callbacks.ReduceLROnPlateau(
                monitor="val_loss",
                factor=0.5,
                patience=4,       # était 2 → divisait le LR trop vite
                min_lr=1e-6,      # était 1e-8 → plancher trop bas, modèle bloqué
                verbose=1,
            ),
        ],
    )


# ─── Export ───────────────────────────────────────────────────────────────────

def export_onnx(embedding_model, output_path, img_size):
    """
    Export du modèle d'embedding en format ONNX.

    ONNX est le format recommandé pour l'inférence en Go via onnxruntime_go
    (github.com/yalue/onnxruntime_go). Il est plus stable que TFLite en Go
    car TFLite n'a pas de bindings Go officiels maintenus.

    Installation : pip install tf2onnx onnx
    """
    try:
        import tf2onnx
    except ImportError:
        print("\nONNX export ignoré (tf2onnx non installé)")
        print("  pip install tf2onnx onnx")
        return

    print(f"\nExport ONNX → {output_path}")

    input_signature = [
        tf.TensorSpec(
            shape=(None, img_size, img_size, 3),
            dtype=tf.float32,
            name="input_image",
        )
    ]

    model_proto, _ = tf2onnx.convert.from_keras(
        embedding_model,
        input_signature=input_signature,
        opset=17,
        output_path=output_path,
    )

    size_mb = os.path.getsize(output_path) / (1024 * 1024)
    print(f"  ✓ Sauvegardé : {size_mb:.1f} MB")


def export_tflite(embedding_model, output_path, calibration_ds=None):
    """
    Export TFLite avec quantization INT8 réelle.

    CHANGEMENT vs ancienne version :
    L'ancienne version faisait converter.optimizations = [DEFAULT] sans
    representative_dataset. Dans ce cas TFLite fait du float16, pas du vrai INT8.

    Pour un vrai INT8 (4x plus petit, 2-3x plus rapide sur CPU mobile) :
    → il faut fournir un representative_dataset : une centaine d'images réelles
      que le convertisseur utilise pour calibrer les valeurs min/max de chaque
      couche avant de quantifier.

    Si calibration_ds est fourni → INT8 complet (~4x compression, légère perte <2%)
    Sinon → float16 (~2x compression, perte quasi nulle)
    """
    print(f"\nExport TFLite → {output_path}")

    converter = tf.lite.TFLiteConverter.from_keras_model(embedding_model)
    converter.optimizations = [tf.lite.Optimize.DEFAULT]

    if calibration_ds is not None:
        print("  Calibration INT8 avec le dataset de validation...")

        def representative_dataset_gen():
            for images, _ in calibration_ds.take(100):
                for i in range(min(BATCH_SIZE, images.shape[0])):
                    yield [images[i:i + 1]]

        converter.representative_dataset = representative_dataset_gen
        converter.target_spec.supported_ops = [tf.lite.OpsSet.TFLITE_BUILTINS_INT8]
        # On garde les I/O en float32 pour faciliter l'intégration mobile
        converter.inference_input_type = tf.float32
        converter.inference_output_type = tf.float32
        quantization_type = "INT8"
    else:
        converter.target_spec.supported_types = [tf.float16]
        quantization_type = "float16"

    tflite_model = converter.convert()

    with open(output_path, "wb") as f:
        f.write(tflite_model)

    size_mb = os.path.getsize(output_path) / (1024 * 1024)
    print(f"  ✓ Sauvegardé : {size_mb:.1f} MB ({quantization_type})")


# ─── Utilitaires ──────────────────────────────────────────────────────────────

def save_json(path, data):
    with open(path, "w") as f:
        json.dump(data, f, indent=2)
    print(f"  ✓ {path}")


# ─── Main ─────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Entraînement modèle d'embedding de cartes (ArcFace)")
    parser.add_argument("--name", required=True, help="Nom du modèle (ex: riftbound)")
    parser.add_argument(
        "--backbone",
        default="convnext-tiny",
        choices=list(BACKBONES.keys()),
        help="Architecture backbone (défaut: convnext-tiny)",
    )
    parser.add_argument("--epochs", type=int, default=20, help="Epochs par phase (défaut: 20)")
    parser.add_argument("--embedding-dim", type=int, default=512, help="Dimension de l'embedding (défaut: 512)")
    parser.add_argument("--margin", type=float, default=0.5, help="Marge angulaire ArcFace en radians (défaut: 0.5)")
    parser.add_argument("--scale", type=float, default=64.0, help="Scale ArcFace / température softmax (défaut: 64.0)")
    args = parser.parse_args()

    backbone_cfg = BACKBONES[args.backbone]
    img_size = backbone_cfg["img_size"]

    dataset_dir = os.path.join("datasets", args.name)
    output_dir = os.path.join("models", args.name)

    if not os.path.isdir(os.path.join(dataset_dir, "train")):
        print(f"Dossier train/ introuvable dans {dataset_dir}")
        print(f"  Lance d'abord : python augment.py --input images/<jeu> [images/<jeu2> ...] --output datasets/{args.name}")
        return

    os.makedirs(output_dir, exist_ok=True)

    print(f"Modèle    : {args.name}")
    print(f"Backbone  : {args.backbone} ({img_size}x{img_size})")
    print(f"Embedding : {args.embedding_dim}D  margin={args.margin}  scale={args.scale}")
    gpus = tf.config.list_physical_devices('GPU')
    print(f"GPU       : {gpus}")
    if gpus:
        keras.mixed_precision.set_global_policy("mixed_float16")
        print("Mixed precision : float16 (ArcFace pinned to float32)")
    print()

    # Chargement des datasets avec preprocessing dans le pipeline
    train_ds, val_ds, class_names, num_classes = load_datasets(
        dataset_dir, img_size, backbone_cfg["preprocess"]
    )

    # Construction du backbone (poids ImageNet, tête retirée)
    base_model = backbone_cfg["class"](
        input_shape=(img_size, img_size, 3),
        include_top=False,
        weights="imagenet",
    )
    base_model.trainable = False

    # Construction du modèle d'embedding + ArcFace
    model = CardEmbeddingModel(
        base_model=base_model,
        num_classes=num_classes,
        embedding_dim=args.embedding_dim,
        margin=args.margin,
        scale=args.scale,
    )

    # Build : passe factice pour initialiser les poids
    dummy_images = tf.zeros((1, img_size, img_size, 3))
    dummy_labels = tf.zeros((1,), dtype=tf.int32)
    dummy_emb = model.compute_embeddings(dummy_images)
    _ = model.arcface(dummy_emb, dummy_labels)
    print(f"Paramètres backbone     : {base_model.count_params():,}")
    print(f"Dimension embedding out : {dummy_emb.shape}")
    print()

    # Entraînement
    train_phase1(model, train_ds, val_ds, epochs=args.epochs)
    train_phase2(
        model, train_ds, val_ds,
        epochs=args.epochs,
        fine_tune_layers=backbone_cfg["fine_tune_layers"],
    )

    # Évaluation finale
    print("\n=== Évaluation finale ===")
    results = model.evaluate(val_ds, verbose=1)
    loss = float(results["loss"]) if isinstance(results, dict) else float(results[0])
    acc_key = next((k for k in results if "accuracy" in k), None) if isinstance(results, dict) else None
    acc = float(results[acc_key]) if acc_key else None
    acc_str = f"{acc:.4f}" if acc is not None else "n/a"
    print(f"Val loss : {loss:.4f} | Val accuracy : {acc_str}")
    print("(Note : l'accuracy réelle sur la reconnaissance sera mesurée via build_index.py)")

    # Export du modèle d'embedding propre (sans couche ArcFace)
    # Force float32 pour que l'export ONNX ne contienne pas de float16 résiduel
    keras.mixed_precision.set_global_policy("float32")
    print("\n=== Export ===")
    embedding_model = model.get_embedding_model(img_size)

    # Keras (pour réentraînement futur)
    keras_path = os.path.join(output_dir, "embedding_model.keras")
    embedding_model.save(keras_path)
    print(f"  ✓ Keras : {keras_path}")

    # ONNX (pour inférence en Go)
    try:
        export_onnx(embedding_model, os.path.join(output_dir, "embedding_model.onnx"), img_size)
    except Exception as e:
        print(f"  ⚠ ONNX export failed: {e}")

    # Métadonnées
    print("\n=== Métadonnées ===")
    save_json(
        os.path.join(output_dir, "labels.json"),
        {i: name for i, name in enumerate(class_names)},
    )
    save_json(
        os.path.join(output_dir, "config.json"),
        {
            "backbone": args.backbone,
            "img_size": img_size,
            "embedding_dim": args.embedding_dim,
            "margin": args.margin,
            "scale": args.scale,
            "num_classes": num_classes,
        },
    )

    print(f"\n✓ Terminé ! Fichiers dans {output_dir}/")
    print("  embedding_model.keras   → modèle Keras (pour réentraînement)")
    print("  embedding_model.onnx    → ONNX (inférence Go via onnxruntime_go)")
    print("  labels.json             → index classe → card ID")
    print("  config.json             → hyperparamètres")
    print(f"\nÉtape suivante : python build_index.py --name {args.name}")


if __name__ == "__main__":
    main()
