"""Validate the extracted binary before building an x86_64 Router AMI."""

import os
import pathlib
import unittest


class EnvoyArtifactTest(unittest.TestCase):
    def test_binary_matches_router_architecture(self):
        default = pathlib.Path(__file__).resolve().parents[1] / "build" / "envoy"
        binary = pathlib.Path(os.environ.get("ENVOY_BINARY", str(default)))
        self.assertTrue(binary.is_file(), "Run the prepare playbook first")
        with binary.open("rb") as stream:
            header = stream.read(20)
        self.assertEqual(header[:4], b"\x7fELF")
        self.assertEqual(header[4:6], b"\x02\x01", "Expected 64-bit little-endian ELF")
        self.assertEqual(
            int.from_bytes(header[18:20], "little"),
            62,
            "Router AMI requires x86_64, regardless of build host architecture",
        )


if __name__ == "__main__":
    unittest.main()
