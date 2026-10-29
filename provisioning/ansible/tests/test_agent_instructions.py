"""Exercise instruction deployment without changing the real user's home."""

import hashlib
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
PACING = (
    "**エージェントはできるだけゆっくり操作すること。** "
    "許可された読み取り専用の操作であっても、操作の間には `sleep` による待機を挟み、"
    "連続実行や並列実行で本番環境に負荷を集中させないこと。\n\n"
)
HOLIDAY = "- 休日は対応できる人が少ないため、破壊的作業を行わないこと。\n"


class AgentInstructionsTest(unittest.TestCase):
    def test_supplied_instructions_and_daily_notes(self):
        content = (
            ANSIBLE / "roles/agent-instructions/files/instructions.md"
        ).read_bytes()
        source, separator, supplement = content.partition("\n## 8. 応答言語\n".encode())
        self.assertTrue(separator)
        self.assertNotIn(b"<!-- participant-supplement -->", content)
        operations = source.decode("utf-8").split("## 4. 許可される操作\n", 1)[1]
        operations = operations.split("## 5.", 1)[0]
        self.assertIn(PACING, operations)
        self.assertEqual(source.count(PACING.encode()), 1)
        data_rules = source.decode("utf-8").split("### 2.4 データの変更・削除\n", 1)[1]
        self.assertIn(HOLIDAY, data_rules.split("### 2.5", 1)[0])
        self.assertEqual(source.count(HOLIDAY.encode()), 1)
        # Preserve the supplied document except for the requested additions.
        original = source.replace(PACING.encode(), b"", 1).replace(
            HOLIDAY.encode(), b"", 1
        )
        self.assertEqual(
            hashlib.sha256(original).hexdigest(),
            "22b2412fa165cb282b86064ada6a74faca31c3386d14a93ce4d9dae65a60be0c",
        )
        notes = supplement.decode("utf-8")
        for topic in (
            "全てのレスポンスは日本語で行うこと。",
            "プレミアムフライデー",
            "ビール",
            "ノンアルコール",
            "休日",
            "読書や音楽",
            "十分な休息",
        ):
            with self.subTest(topic=topic):
                self.assertIn(topic, notes)

    def test_no_language_specific_destinations(self):
        defaults = yaml.safe_load(
            (ANSIBLE / "roles/agent-instructions/defaults/main.yml").read_text()
        )
        paths = defaults["agent_instructions_paths"]
        self.assertEqual(len(paths), 8)
        self.assertEqual(
            [path for path in paths if path.startswith("webapp/")],
            ["webapp/AGENTS.md"],
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
