"""Check that the documented command resolves to the deployed checker."""

import pathlib
import unittest

import yaml


class EnvcheckCommandTest(unittest.TestCase):
    def test_boot_check_waits_for_baseline_and_registration(self):
        root = pathlib.Path(__file__).resolve().parents[1]
        unit = (root / "roles/envcheck/files/envcheck.service").read_text()
        self.assertIn("Requires=envcheck-baseline.service", unit)
        after = next(line for line in unit.splitlines() if line.startswith("After="))
        for dependency in [
            "cloud-final.service",
            "envcheck-baseline.service",
            "isucon-register.service",
        ]:
            self.assertIn(dependency, after)
        self.assertIn("WantedBy=cloud-init.target", unit)
        baseline = (root / "roles/envcheck/files/envcheck-baseline.service").read_text()
        self.assertIn(
            "ExecStartPost=/usr/bin/systemctl --no-block start envcheck.service",
            baseline,
        )
        template = (
            root.parents[1]
            / "portal/internal/cloudformation/templates/contest.yaml.tmpl"
        ).read_text()
        self.assertEqual(
            template.count("systemctl enable --now --no-block envcheck.service"), 2
        )
        tasks = yaml.safe_load((root / "roles/envcheck/tasks/main.yml").read_text())
        enabled = [
            task["ansible.builtin.systemd_service"]["name"]
            for task in tasks
            if task.get("ansible.builtin.systemd_service", {}).get("enabled")
        ]
        self.assertIn("envcheck.service", enabled)

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
        unit = (root / "roles/envcheck/files/envcheck.service").read_text()
        self.assertIn("ExecStart=" + link["src"] + "\n", unit)
        self.assertEqual(link["owner"], "root")
        manual = (root.parents[1] / "docs/manual.md").read_text()
        self.assertIn("sudo " + link["path"], manual)


if __name__ == "__main__":
    unittest.main()
