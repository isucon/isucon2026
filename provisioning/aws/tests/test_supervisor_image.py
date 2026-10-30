"""Verify that image builds and archive verification use the same platform."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


class SupervisorImageTest(unittest.TestCase):
    def test_platform_selection(self):
        source = Path(__file__).resolve().parents[1] / "supervisor" / "image.sh"
        for platform in (None, "linux/arm64"):
            with self.subTest(platform=platform), tempfile.TemporaryDirectory() as d:
                root = Path(d)
                script = root / "provisioning/aws/supervisor/image.sh"
                script.parent.mkdir(parents=True)
                shutil.copyfile(source, script)
                photos = root / "var/photos/merged.zip"
                photos.parent.mkdir(parents=True)
                photos.write_bytes(b"archive")
                binaries = root / "bin"
                binaries.mkdir()
                docker = binaries / "docker"
                docker.write_text(
                    "#!/usr/bin/env python3\n"
                    "import hashlib, json, os, pathlib, sys\n"
                    "args = sys.argv[1:]\n"
                    "with open(os.environ['CALLS'], 'a') as f:\n"
                    "    f.write(json.dumps(args) + '\\n')\n"
                    "if args[0] == 'build':\n"
                    "    pathlib.Path(args[args.index('--iidfile') + 1]).write_text('sha256:abc')\n"
                    "elif args[0] == 'image':\n"
                    "    print('sha256:resolved')\n"
                    "elif args[0] == 'run':\n"
                    "    print(hashlib.sha256(b'archive').hexdigest(), '/opt/isucon/photos/merged.zip')\n"
                )
                docker.chmod(0o755)
                aws = binaries / "aws"
                aws.write_text("#!/bin/sh\necho sha256:existing\n")
                aws.chmod(0o755)
                calls = root / "calls.jsonl"
                env = {
                    **os.environ,
                    "PATH": f"{binaries}:{os.environ['PATH']}",
                    "CALLS": str(calls),
                    "REPOSITORY": "123.dkr.ecr.ap-northeast-1.amazonaws.com/supervisor",
                }
                env.pop("SUPERVISOR_PLATFORM", None)
                if platform:
                    env["SUPERVISOR_PLATFORM"] = platform
                subprocess.run(
                    ["bash", str(script)], env=env, check=True, capture_output=True
                )
                commands = [json.loads(line) for line in calls.read_text().splitlines()]
                self.assertEqual(
                    [command[0] for command in commands], ["build", "image", "run"]
                )
                self.assertIn("sha256:resolved", commands[-1])
                for command in (commands[0], commands[-1]):
                    self.assertEqual(
                        command[command.index("--platform") + 1],
                        platform or "linux/amd64",
                    )


if __name__ == "__main__":
    unittest.main()
