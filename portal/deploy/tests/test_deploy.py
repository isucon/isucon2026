import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


class DeployTest(unittest.TestCase):
    def run_deploy(self, migration_failure=False, digest=None):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            trace = path / "trace"
            for name, script in {
                "aws": '#!/bin/sh\nprintf "aws\\n" >> "$TRACE"\n',
                "ecspresso": '#!/bin/sh\nprintf "%s %s\\n" "$SERVICE" "$*" >> "$TRACE"\nif [ "$SERVICE" = migrate ] && [ "$FAIL_MIGRATION" = 1 ]; then exit 1; fi\n',
            }.items():
                command = path / name
                command.write_text(script)
                command.chmod(0o755)
            result = subprocess.run(
                ["sh", str(ROOT / "deploy.sh")],
                env=dict(
                    os.environ,
                    PATH=str(path) + os.pathsep + os.environ["PATH"],
                    TRACE=str(trace),
                    FAIL_MIGRATION="1" if migration_failure else "0",
                    PORTAL_CONFIG_URI="s3://config/infra.json",
                    IMAGE_DIGEST=digest or "sha256:" + "a" * 64,
                ),
                capture_output=True,
                text=True,
                check=False,
            )
            return result, trace.read_text().splitlines() if trace.exists() else []

    def test_failed_migration_never_deploys_services(self):
        result, calls = self.run_deploy(migration_failure=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 2)
        self.assertIn("migrate", calls[-1])
        self.assertIn("--watch-container migrate", calls[-1])
        self.assertIn("--wait", calls[-1])

    def test_success_uses_ecspresso_for_all_services(self):
        result, calls = self.run_deploy()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(calls), 6)
        self.assertIn("migrate", calls[1])
        for call in calls[2:]:
            self.assertIn("deploy --wait --wait-until deployed", call)

    def test_mutable_image_is_rejected_before_aws(self):
        result, calls = self.run_deploy(digest="latest")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
