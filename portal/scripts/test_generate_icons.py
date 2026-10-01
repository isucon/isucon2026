"""Regression checks for the cropped browser favicon artwork."""

import unittest
from pathlib import Path

from PIL import Image

PUBLIC = Path(__file__).resolve().parents[1] / "frontend/public"


class FaviconTests(unittest.TestCase):
    def test_browser_icons_contain_only_red_digits(self):
        paths = [PUBLIC / "favicon.ico", *sorted(PUBLIC.glob("icon-*.png"))]
        for path in paths:
            with self.subTest(path=path.name), Image.open(path) as image:
                rgb = image.convert("RGB")
                ink = []
                for y in range(rgb.height):
                    for x in range(rgb.width):
                        r, g, b = rgb.getpixel((x, y))
                        self.assertFalse(
                            max(r, g, b) < 120, "ISUCON's black text must be excluded"
                        )
                        if r > g + 40 and r > b + 40:
                            ink.append((x, y))
                self.assertTrue(ink)
                width = max(x for x, _ in ink) - min(x for x, _ in ink) + 1
                self.assertGreaterEqual(
                    width / rgb.width, 0.8, "digits must fill the favicon width"
                )


if __name__ == "__main__":
    unittest.main()
