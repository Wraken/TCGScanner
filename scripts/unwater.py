"""
Remove SAMPLE watermark from One Piece card images.

Step 1: Generate mask from multiple cards
    python remove_watermark.py mask --input images/ --output sample_mask.png

Step 2: Remove watermark from all cards
    python remove_watermark.py remove --input images/ --output images_clean/ --mask sample_mask.png

Step 3 (optional): Preview a single card
    python remove_watermark.py preview --input images/OP01-001.webp --mask sample_mask.png
"""

import argparse
import os
import cv2
import numpy as np
from pathlib import Path
from tqdm import tqdm


def generate_mask(input_dir, output_path, n_samples=20):
    """
    Generate a watermark mask by comparing multiple cards.
    The SAMPLE text is the same on all cards, so averaging many cards
    reveals the watermark (card-specific art cancels out).
    """
    input_path = Path(input_dir)
    images = sorted(
        list(input_path.glob("*.png")) +
        list(input_path.glob("*.webp")) +
        list(input_path.glob("*.jpg"))
    )

    if len(images) < 2:
        print("Need at least 2 images to generate mask")
        return

    # Use a subset for speed
    step = max(1, len(images) // n_samples)
    sample_images = images[::step][:n_samples]
    print(f"Using {len(sample_images)} images to generate mask")

    # Read and resize all to same dimensions
    target_h, target_w = None, None
    imgs = []
    for p in tqdm(sample_images, desc="Loading images"):
        img = cv2.imread(str(p))
        if img is None:
            continue
        if target_h is None:
            target_h, target_w = img.shape[:2]
        img = cv2.resize(img, (target_w, target_h))
        imgs.append(img.astype(np.float32))

    if len(imgs) < 2:
        print("Not enough valid images")
        return

    # Average all images — card art cancels, watermark remains
    avg = np.mean(imgs, axis=0)

    # The watermark is bright (white text)
    # Convert to grayscale and threshold
    avg_gray = cv2.cvtColor(avg.astype(np.uint8), cv2.COLOR_BGR2GRAY)

    # The SAMPLE zone is roughly center of the card
    h, w = avg_gray.shape
    # Focus on the center where SAMPLE is
    center_mask = np.zeros_like(avg_gray)
    y1, y2 = int(h * 0.41), int(h * 0.59)
    x1, x2 = int(w * 0.08), int(w * 0.92)
    center_mask[y1:y2, x1:x2] = 255

    # Threshold: bright pixels in the average = white watermark text
    _, bright = cv2.threshold(avg_gray, 160, 255, cv2.THRESH_BINARY)
    white_mask = cv2.bitwise_and(bright, center_mask)

    # Small dilation for white text
    kernel = np.ones((3, 3), np.uint8)
    white_mask = cv2.dilate(white_mask, kernel, iterations=2)
    white_mask = cv2.GaussianBlur(white_mask, (5, 5), 0)

    # Now detect the dark outline
    # In the average, the dark outline appears darker than surroundings
    # We need to compare each pixel to its local neighborhood
    # Local mean gives us "expected" brightness without the watermark
    local_mean = cv2.GaussianBlur(avg_gray, (51, 51), 0)

    # Dark pixels = significantly darker than local mean
    diff = local_mean.astype(np.float32) - avg_gray.astype(np.float32)
    # Where diff is large = dark outline pixels
    _, dark = cv2.threshold(diff.astype(np.uint8), 15, 255, cv2.THRESH_BINARY)
    dark_mask = cv2.bitwise_and(dark, center_mask)

    # Remove any overlap with white mask
    dark_mask = cv2.subtract(dark_mask, white_mask)

    # Small dilation for dark outline
    dark_mask = cv2.dilate(dark_mask, kernel, iterations=1)

    # Save both masks
    cv2.imwrite(output_path, white_mask)
    dark_path = output_path.replace(".png", "_dark.png")
    cv2.imwrite(dark_path, dark_mask)

    # Also save combined for visualization
    combined = cv2.add(white_mask, dark_mask)
    cv2.imwrite(output_path.replace(".png", "_combined.png"), combined)

    print(f"White mask saved to {output_path}")
    print(f"Dark mask saved to {dark_path}")
    print(f"Combined mask saved to {output_path.replace('.png', '_combined.png')}")
    print(f"White pixels: {np.count_nonzero(white_mask)}")
    print(f"Dark pixels: {np.count_nonzero(dark_mask)}")


def remove_watermark(image, mask, strength=0.7):
    """
    Two-pass watermark removal:
    1. Math inversion for the white semi-transparent text
    2. Inpainting for the dark outline
    """
    h, w = image.shape[:2]
    mask_resized = cv2.resize(mask, (w, h))

    # --- Pass 1: Remove white text with math ---
    alpha = (mask_resized.astype(np.float32) / 255.0) * strength
    result = image.astype(np.float32)
    for c in range(3):
        result[:, :, c] = (result[:, :, c] - 255.0 * alpha) / (1.0 - alpha + 1e-6)
    result = np.clip(result, 0, 255).astype(np.uint8)

    # --- Pass 2: Inpaint dark outline ---
    dark_path = None
    # Try to find the dark mask next to the white mask
    # (handled by caller passing dark_mask separately)
    return result


def remove_watermark_full(image, white_mask, dark_mask, strength=0.7):
    """
    Full two-pass removal with both masks.
    """
    h, w = image.shape[:2]

    # Pass 1: Math removal of white text
    wm = cv2.resize(white_mask, (w, h))
    alpha = (wm.astype(np.float32) / 255.0) * strength
    result = image.astype(np.float32)
    for c in range(3):
        result[:, :, c] = (result[:, :, c] - 255.0 * alpha) / (1.0 - alpha + 1e-6)
    result = np.clip(result, 0, 255).astype(np.uint8)

    # Pass 2: Inpaint dark outline
    dm = cv2.resize(dark_mask, (w, h))
    _, dm_bin = cv2.threshold(dm, 30, 255, cv2.THRESH_BINARY)
    result = cv2.inpaint(result, dm_bin, inpaintRadius=5, flags=cv2.INPAINT_TELEA)

    return result


def process_all(input_dir, output_dir, mask_path, strength=0.7):
    """Remove watermark from all images in a directory."""
    white_mask = cv2.imread(mask_path, cv2.IMREAD_GRAYSCALE)
    dark_path = mask_path.replace(".png", "_dark.png")
    dark_mask = cv2.imread(dark_path, cv2.IMREAD_GRAYSCALE)

    if white_mask is None:
        print(f"Cannot read white mask: {mask_path}")
        return
    if dark_mask is None:
        print(f"Cannot read dark mask: {dark_path}")
        print("Running without dark outline removal")
        dark_mask = np.zeros_like(white_mask)

    input_path = Path(input_dir)
    os.makedirs(output_dir, exist_ok=True)

    images = sorted(
        list(input_path.glob("*.png")) +
        list(input_path.glob("*.webp")) +
        list(input_path.glob("*.jpg"))
    )

    print(f"Processing {len(images)} images")

    for img_path in tqdm(images, desc="Removing watermark"):
        img = cv2.imread(str(img_path))
        if img is None:
            continue

        clean = remove_watermark_full(img, white_mask, dark_mask, strength)

        out_path = os.path.join(output_dir, img_path.stem + ".png")
        cv2.imwrite(out_path, clean)

    print(f"Done! Clean images saved to {output_dir}/")


def preview(image_path, mask_path, strength=0.7):
    """Preview watermark removal on a single image."""
    img = cv2.imread(image_path)
    white_mask = cv2.imread(mask_path, cv2.IMREAD_GRAYSCALE)
    dark_path = mask_path.replace(".png", "_dark.png")
    dark_mask = cv2.imread(dark_path, cv2.IMREAD_GRAYSCALE)

    if img is None or white_mask is None:
        print("Cannot read image or mask")
        return

    if dark_mask is None:
        print("No dark mask found, using white mask only")
        dark_mask = np.zeros_like(white_mask)

    h, w = img.shape[:2]
    wm = cv2.resize(white_mask, (w, h))
    dm = cv2.resize(dark_mask, (w, h))

    # Pass 1: white removal only
    alpha = (wm.astype(np.float32) / 255.0) * strength
    white_clean = img.astype(np.float32)
    for c in range(3):
        white_clean[:, :, c] = (white_clean[:, :, c] - 255.0 * alpha) / (1.0 - alpha + 1e-6)
    white_clean = np.clip(white_clean, 0, 255).astype(np.uint8)

    # Pass 2: dark inpainting on top
    _, dm_bin = cv2.threshold(dm, 30, 255, cv2.THRESH_BINARY)
    final_clean = cv2.inpaint(white_clean, dm_bin, inpaintRadius=5, flags=cv2.INPAINT_TELEA)

    # Build comparison: original | white mask | white clean | dark mask | final
    wm_color = cv2.cvtColor(wm, cv2.COLOR_GRAY2BGR)
    dm_color = cv2.cvtColor(dm, cv2.COLOR_GRAY2BGR)

    comparison = np.hstack([img, wm_color, white_clean, dm_color, final_clean])
    cv2.imwrite("watermark_preview.png", comparison)
    print("Preview saved to watermark_preview.png")
    print("  original | white mask | white clean | dark mask | final")


def main():
    parser = argparse.ArgumentParser(description="Remove SAMPLE watermark")
    sub = parser.add_subparsers(dest="command")

    # mask command
    p_mask = sub.add_parser("mask", help="Generate watermark mask")
    p_mask.add_argument("--input", required=True, help="Card images directory")
    p_mask.add_argument("--output", default="sample_mask.png", help="Output mask path")
    p_mask.add_argument("--samples", type=int, default=20, help="Number of cards to average")

    # remove command
    p_remove = sub.add_parser("remove", help="Remove watermark from all cards")
    p_remove.add_argument("--input", required=True, help="Card images directory")
    p_remove.add_argument("--output", required=True, help="Output directory")
    p_remove.add_argument("--mask", required=True, help="Watermark mask path")
    p_remove.add_argument("--strength", type=float, default=0.7, help="Removal strength (0-1)")

    # preview command
    p_preview = sub.add_parser("preview", help="Preview removal on one card")
    p_preview.add_argument("--input", required=True, help="Single card image")
    p_preview.add_argument("--mask", required=True, help="Watermark mask path")
    p_preview.add_argument("--strength", type=float, default=0.7, help="Removal strength (0-1)")

    args = parser.parse_args()

    if args.command == "mask":
        generate_mask(args.input, args.output, args.samples)
    elif args.command == "remove":
        process_all(args.input, args.output, args.mask, args.strength)
    elif args.command == "preview":
        preview(args.input, args.mask, args.strength)
    else:
        parser.print_help()


if __name__ == "__main__":
    main()
