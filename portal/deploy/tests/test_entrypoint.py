import os
import pathlib
import subprocess
import tempfile
import unittest

ENTRYPOINT = pathlib.Path(__file__).resolve().parents[1] / "docker-entrypoint.sh"


class EntrypointTest(unittest.TestCase):
    def test_ssh_key_is_private_and_removed_from_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "key"
            env = dict(
                os.environ,
                ISUXPORTAL_ADMIN_SSH_PRIVATE_KEY="test-private-key\n",
                ISUXPORTAL_ISUCON_ADMIN_SSH_PRIVATE_KEY_PATH=str(path),
            )
            result = subprocess.run(
                [
                    "sh",
                    str(ENTRYPOINT),
                    "sh",
                    "-c",
                    'test -z "${ISUXPORTAL_ADMIN_SSH_PRIVATE_KEY+x}"',
                ],
                env=env,
                check=False,
            )
            self.assertEqual(result.returncode, 0)
            self.assertEqual(path.read_text(), "test-private-key\n")
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
