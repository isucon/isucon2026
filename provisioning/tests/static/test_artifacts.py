from __future__ import annotations

from pathlib import Path
import tempfile
import unittest

from provisioning.scripts import preflight

from support import PROVISIONING_DIR


class ArtifactContractTest(unittest.TestCase):
    def test_local_artifact_preflight_only_checks_presence(self) -> None:
        with tempfile.TemporaryDirectory(dir=PROVISIONING_DIR.parent) as temporary:
            artifact = Path(temporary) / "backend.tar"
            artifact.write_bytes(b"first build")
            relative = artifact.relative_to(PROVISIONING_DIR.parent)
            release = {
                "artifacts": [
                    {
                        "id": "backend.amd64",
                        "uri": f"file://{relative}",
                    }
                ]
            }

            self.assertEqual([], preflight.check_artifact_files(release))

            artifact.write_bytes(b"changed source build")
            self.assertEqual([], preflight.check_artifact_files(release))

    def test_missing_local_artifact_is_rejected(self) -> None:
        errors = preflight.check_artifact_files(
            {
                "artifacts": [
                    {
                        "id": "backend.amd64",
                        "uri": "file://provisioning/.state/artifacts/missing/backend.tar",
                    }
                ]
            }
        )
        self.assertTrue(any("is missing" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
