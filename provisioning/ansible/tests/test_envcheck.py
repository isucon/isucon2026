"""Check that the documented command resolves to the deployed checker."""

import pathlib
import unittest

import yaml


class EnvcheckCommandTest(unittest.TestCase):
    def test_documented_command_uses_deployed_binary(self):
        root = pathlib.Path(__file__).resolve().parents[1]
        tasks = yaml.safe_load((root / "roles/envcheck/tasks/main.yml").read_text())
        links = [
            task["ansible.builtin.file"]
            for task in tasks
            if task.get("ansible.builtin.file", {}).get("path")
            == "/usr/local/bin/envcheck"
        ]
        self.assertEqual(len(links), 1, "envcheck command must be installed")
        link = links[0]
        self.assertEqual(link["state"], "link")
        binaries = [
            task["ansible.builtin.copy"]["dest"]
            for task in tasks
            if "ansible.builtin.copy" in task
        ]
        self.assertIn(link["src"], binaries)
        self.assertEqual(link["owner"], "root")
        manual = (root.parents[1] / "docs/manual.md").read_text()
        self.assertIn("sudo " + link["path"], manual)


if __name__ == "__main__":
    unittest.main()
