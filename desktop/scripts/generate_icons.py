#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#   "pillow>=11.2.0",
# ]
# ///

from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image


MAC_ICONSET_SPECS = [
    ("icon_16x16.png", 16),
    ("icon_16x16@2x.png", 32),
    ("icon_32x32.png", 32),
    ("icon_32x32@2x.png", 64),
    ("icon_128x128.png", 128),
    ("icon_128x128@2x.png", 256),
    ("icon_256x256.png", 256),
    ("icon_256x256@2x.png", 512),
    ("icon_512x512.png", 512),
    ("icon_512x512@2x.png", 1024),
]

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_SOURCE = REPO_ROOT / "desktop" / "branding" / "kodelet-icon-source.png"
DEFAULT_OUTPUT_DIR = REPO_ROOT / "desktop" / "assets"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Generate desktop icon assets from a square source PNG.")
    parser.add_argument(
        "--source",
        default=str(DEFAULT_SOURCE),
        help=f"Path to the square source PNG. Defaults to {DEFAULT_SOURCE}.",
    )
    parser.add_argument(
        "--out",
        default=str(DEFAULT_OUTPUT_DIR),
        help=f"Output directory for generated assets. Defaults to {DEFAULT_OUTPUT_DIR}.",
    )
    return parser.parse_args()


def resize_square(source: Image.Image, size: int) -> Image.Image:
    return source.resize((size, size), Image.Resampling.LANCZOS)


def main() -> None:
    args = parse_args()
    source_path = Path(args.source).resolve()
    output_dir = Path(args.out).resolve()
    output_dir.mkdir(parents=True, exist_ok=True)

    if not source_path.exists():
        raise FileNotFoundError(f"source image not found: {source_path}")

    source = Image.open(source_path).convert("RGBA")
    if source.width != source.height:
        raise ValueError(f"source image must be square, got {source.width}x{source.height}")

    source.copy().save(output_dir / "icon.png")
    resize_square(source, 512).save(output_dir / "icon-512.png")
    resize_square(source, 256).save(output_dir / "icon-256.png")

    iconset_dir = output_dir / "icon.iconset"
    iconset_dir.mkdir(parents=True, exist_ok=True)
    for filename, size in MAC_ICONSET_SPECS:
        resize_square(source, size).save(iconset_dir / filename)

    source.copy().save(
        output_dir / "icon.ico",
        format="ICO",
        sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)],
    )


if __name__ == "__main__":
    main()
