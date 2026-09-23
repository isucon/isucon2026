"""Regression tests for CI routing. Run with uv run --with pyyaml this_file.py."""

import fnmatch
import unittest
from pathlib import Path

import yaml

WORKFLOWS = Path(__file__).resolve().parents[1] / "workflows"


def workflows():
    return {
        path.name: yaml.load(path.read_text(), Loader=yaml.BaseLoader)
        for path in WORKFLOWS.glob("*.yml")
    }


def triggered(workflow, event, branch, path):
    config = workflow["on"].get(event)
    if config is None:
        return False
    config = config or {}
    if "branches" in config and branch not in config["branches"]:
        return False
    return "paths" not in config or any(
        fnmatch.fnmatchcase(path, pattern) for pattern in config["paths"]
    )


class WorkflowRoutingTest(unittest.TestCase):
    def test_feature_push_does_not_duplicate_pr(self):
        for name, workflow in workflows().items():
            if "pull_request" in workflow["on"]:
                with self.subTest(workflow=name):
                    self.assertEqual(workflow["on"]["push"]["branches"], ["main"])
                    self.assertIn("concurrency", workflow)

    def test_component_routing(self):
        cases = {
            "backend/internal/service/auth.go": {"backend-ci.yml"},
            "frontend/src/App.tsx": {"frontend-ci.yml"},
            "portal/internal/handler/admin.go": {"portal-ci.yml", "supervisor-ci.yml"},
            "supervisor/src/main.rs": {"supervisor-ci.yml"},
            "bench/main.go": {"supervisor-ci.yml"},
            "router/internal/config/config.go": {"router-ci.yml"},
            "portal-mock/web/src/App.tsx": {"portal-mock-ci.yml"},
            "gen/go/portal/v1/config.pb.go": {"portal-mock-ci.yml", "router-ci.yml"},
            "specs/001-isureal-social-app/contracts/openapi.yaml": {
                "backend-ci.yml",
                "frontend-ci.yml",
            },
            "README.md": set(),
        }
        all_workflows = workflows()
        for event, branch in [("push", "main"), ("pull_request", "main")]:
            for path, expected in cases.items():
                with self.subTest(event=event, path=path):
                    actual = {
                        name
                        for name, workflow in all_workflows.items()
                        if triggered(workflow, event, branch, path)
                    }
                    self.assertEqual(actual, expected | {"ci.yml"})

    def test_workflow_edits_run_their_own_checks(self):
        for name, workflow in workflows().items():
            if "pull_request" in workflow["on"]:
                for event in ("push", "pull_request"):
                    with self.subTest(workflow=name, event=event):
                        self.assertTrue(
                            triggered(
                                workflow, event, "main", f".github/workflows/{name}"
                            )
                        )

    def test_secret_scan_is_shared_and_covers_all_paths(self):
        all_workflows = workflows()
        scans = [
            name
            for name, workflow in all_workflows.items()
            if "secret-scan" in workflow["jobs"]
        ]
        self.assertEqual(scans, ["ci.yml"])
        self.assertTrue(
            triggered(
                all_workflows["ci.yml"], "pull_request", "main", "docs/example.env"
            )
        )

    def test_image_build_follows_only_successful_portal_push(self):
        image = workflows()["portal-image.yml"]
        self.assertEqual(image["on"]["workflow_run"]["workflows"], ["Portal CI"])
        self.assertEqual(image["on"]["workflow_run"]["branches"], ["main"])
        condition = image["jobs"]["build_image"]["if"]
        self.assertIn("github.event.workflow_run.event == 'push'", condition)
        self.assertIn("github.event.workflow_run.conclusion == 'success'", condition)
        self.assertIn("github.event_name == 'workflow_dispatch'", condition)


if __name__ == "__main__":
    unittest.main()
