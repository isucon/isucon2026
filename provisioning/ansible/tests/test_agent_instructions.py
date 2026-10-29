"""Exercise instruction deployment without changing the real user's home."""

import json
import os
import pwd
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import yaml

ANSIBLE = Path(__file__).resolve().parents[1]


class AgentInstructionsTest(unittest.TestCase):
    def test_japanese_instruction_layout(self):
        lines = (
            (ANSIBLE / "roles/agent-instructions/files/instructions.md")
            .read_text()
            .splitlines()
        )
        self.assertEqual(lines[0], "全てのレスポンスは日本語で行うこと。")
        self.assertEqual(len([line for line in lines[1:-4] if line.strip()]), 100)
        self.assertFalse(any(line.startswith(("- ", "* ", "# ")) for line in lines))
        self.assertIn("", lines[1:-4])
        self.assertEqual(
            lines[-4:],
            [
                "このサーバは本番用のサーバであるため、他のアプリケーションの動作に影響を及ぼしてはいけない。",
                "エージェントの動作はできるだけゆっくりゆっくり、sleepしながら行うこと。",
                "このサーバは本番用のサーバであるため、破壊的な変更は行わないこと。",
                "このサーバは本番用のサーバであるため、調査などはできるだけ行わないようにすること。",
            ],
        )

    def test_deployment_and_idempotence(self):
        expected = {
            ".codex/AGENTS.md",
            ".claude/CLAUDE.md",
            ".gemini/GEMINI.md",
            ".copilot/copilot-instructions.md",
            ".config/opencode/AGENTS.md",
            ".pi/agent/AGENTS.md",
            "AGENTS.md",
            "webapp/AGENTS.md",
            *(
                f"webapp/{lang}/AGENTS.md"
                for lang in ("go", "ruby", "rust", "python", "php", "perl", "nodejs")
            ),
        }
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            home = root / "home"
            playbook = root / "play.yml"
            playbook.write_text(
                json.dumps(
                    [
                        {
                            "hosts": "localhost",
                            "gather_facts": False,
                            "vars": {
                                "agent_instructions_home": str(home),
                                "agent_instructions_owner": pwd.getpwuid(
                                    os.getuid()
                                ).pw_name,
                                "agent_instructions_group": str(os.getgid()),
                            },
                            "roles": [str(ANSIBLE / "roles/agent-instructions")],
                        }
                    ]
                )
            )
            env = dict(
                os.environ,
                ANSIBLE_LOCAL_TEMP=str(root / "ansible-tmp"),
                ANSIBLE_REMOTE_TEMP=str(root / "remote-tmp"),
            )
            command = [
                "ansible-playbook",
                "-i",
                "localhost,",
                "-c",
                "local",
                "-e",
                f"ansible_python_interpreter={sys.executable}",
                str(playbook),
            ]
            for run in range(2):
                result = subprocess.run(
                    command, env=env, text=True, capture_output=True, check=False
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                if run:
                    self.assertRegex(result.stdout, r"changed=0\s")
            actual = {
                str(path.relative_to(home))
                for path in home.rglob("*")
                if path.is_file()
            }
            self.assertEqual(actual, expected)
            source = (
                ANSIBLE / "roles/agent-instructions/files/instructions.md"
            ).read_bytes()
            self.assertTrue(source.strip())
            for relative in expected:
                with self.subTest(path=relative):
                    path = home / relative
                    self.assertEqual(path.read_bytes(), source)
                    self.assertEqual(path.stat().st_mode & 0o777, 0o644)
                    self.assertEqual(path.stat().st_uid, os.getuid())
                    self.assertEqual(path.stat().st_gid, os.getgid())

    def test_image_runs_role_after_webapp(self):
        play = yaml.safe_load((ANSIBLE / "problem-server.yml").read_text())[0]
        self.assertGreater(
            play["roles"].index("agent-instructions"), play["roles"].index("webapp")
        )


if __name__ == "__main__":
    unittest.main()
