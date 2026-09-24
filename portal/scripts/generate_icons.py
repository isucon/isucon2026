# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow==11.3.0"]
# ///
"""Generate portal device icons from the supplied RGB Illustrator artwork.

Run: uv run portal/scripts/generate_icons.py (requires Poppler's pdftocairo).
"""

import re
import subprocess
import tempfile
from pathlib import Path

from PIL import Image, ImageChops, ImageOps

FRONTEND = Path(__file__).resolve().parents[1] / "frontend"


def main():
    public = FRONTEND / "public"
    with tempfile.TemporaryDirectory() as tmp:
        images = {}
        for page, name in [(1, "wide"), (3, "square")]:
            output = Path(tmp) / name
            subprocess.run(
                [
                    "pdftocairo",
                    "-f",
                    str(page),
                    "-l",
                    str(page),
                    "-singlefile",
                    "-scale-to",
                    "1024",
                    "-png",
                    str(FRONTEND / "materials/isucon2026-logo-rgb.ai"),
                    str(output),
                ],
                check=True,
            )
            with Image.open(output.with_suffix(".png")) as image:
                images[name] = image.convert("RGB")

        # The supplied square artboard has "2026" on its lower row.
        # Isolate the right-hand "26", then trim the surrounding white space.
        square = images["square"]
        digits = square.crop(
            (square.width // 2, square.height // 2, square.width, square.height)
        )
        bounds = ImageChops.difference(
            digits, Image.new("RGB", digits.size, "white")
        ).getbbox()
        if bounds is None:
            raise ValueError("The source artwork has no digits in the expected region")
        digits = digits.crop(bounds)
        favicon = ImageOps.pad(
            digits, (960, 960), method=Image.Resampling.LANCZOS, color="white"
        )
        favicon = ImageOps.expand(favicon, border=32, fill="white")

        for target in sorted(public.glob("*.png")):
            if not target.name.startswith(
                ("icon-", "android-chrome-", "apple-touch-icon", "site-tile-")
            ):
                continue
            dimensions = re.search(r"(\d+)x(\d+)", target.name)
            size = tuple(map(int, dimensions.groups())) if dimensions else (180, 180)
            source = (
                favicon
                if target.name.startswith("icon-")
                else images["square" if size[0] == size[1] else "wide"]
            )
            ImageOps.pad(
                source, size, method=Image.Resampling.LANCZOS, color="white"
            ).save(target)

        favicon.save(public / "favicon.ico", sizes=[(16, 16), (32, 32), (48, 48)])


if __name__ == "__main__":
    main()
