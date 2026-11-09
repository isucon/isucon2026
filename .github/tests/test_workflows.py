"""Regression tests for CI routing. Run with uv run --with pyyaml this_file.py."""

import fnmatch
import json
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
    def test_retired_components_are_removed(self):
        root = WORKFLOWS.parents[1]
        for path in (
            "backend",
            "specs",
            ".specify",
            "portal-mock",
            "webapp-optimized",
            "webapp-asia-optimized",
            "webapp-optmized-astra",
        ):
            with self.subTest(path=path):
                self.assertFalse((root / path).exists())
        self.assertFalse(list((root / ".agents/skills").glob("speckit-*")))

    def test_proto_job_has_pinned_versions(self):
        workflow = workflows()["router-ci.yml"]
        for name in (
            "BUF_VERSION",
            "PROTOC_VERSION",
            "PROTOC_GEN_GO_VERSION",
            "PROTOC_GEN_CONNECT_GO_VERSION",
            "PROTOC_GEN_ES_VERSION",
        ):
            self.assertIn(name, workflow["env"])

    def test_retired_development_environments_are_removed(self):
        root = WORKFLOWS.parents[1]
        for path in (
            "compose.yaml",
            "deploy",
            "router/compose.e2e.yaml",
            "router/tests/e2e",
            "router/test-fixtures/server",
            "router/test-fixtures/portal",
            "proto/portal_mock",
            "gen/go/portal_mock",
            "gen/ts/portal_mock",
        ):
            with self.subTest(path=path):
                self.assertFalse((root / path).exists())
        self.assertNotIn("compose-e2e", workflows()["router-ci.yml"]["jobs"])

    def test_frontend_does_not_use_retired_contract(self):
        root = WORKFLOWS.parents[1]
        package = json.loads((root / "frontend/package.json").read_text())
        for name in ("test:contract", "generate:api", "check:openapi"):
            self.assertNotIn(name, package["scripts"])
        self.assertFalse((root / "frontend/src/api/schema.d.ts").exists())
        workflow = (WORKFLOWS / "frontend-ci.yml").read_text()
        self.assertNotIn("test:contract", workflow)
        self.assertNotIn("check:openapi", workflow)

    def test_ci_builds_pull_from_mirror(self):
        for job in workflows()["portal-ci.yml"]["jobs"].values():
            for service in job.get("services", {}).values():
                self.assertTrue(service["image"].startswith("mirror.gcr.io/library/"))
        root = WORKFLOWS.parents[1]
        for path in [
            "portal/deploy/Dockerfile",
            "portal/rspec/Dockerfile",
            "supervisor/Dockerfile",
            "router/Dockerfile",
        ]:
            for line in (root / path).read_text().splitlines():
                if line.startswith("# syntax="):
                    self.assertTrue(line.startswith("# syntax=mirror.gcr.io/"))
                if line.startswith("FROM "):
                    image = line.split()[1]
                    if ":" in image:
                        self.assertTrue(
                            image.startswith("mirror.gcr.io/library/"), image
                        )

    def test_router_checks_use_mirror(self):
        image = workflows()["router-ci.yml"]["jobs"]["digest"]["container"]["image"]
        self.assertTrue(image.startswith("mirror.gcr.io/library/"))
        makefile = (WORKFLOWS.parents[1] / "Makefile").read_text()
        commands = [
            line
            for line in makefile.splitlines()
            if "docker run" in line and "haproxy" in line
        ]
        self.assertEqual(len(commands), 2)
        for command in commands:
            self.assertIn("mirror.gcr.io/library/haproxy:", command)

    def test_feature_push_does_not_duplicate_pr(self):
        for name, workflow in workflows().items():
            if "pull_request" in workflow["on"]:
                with self.subTest(workflow=name):
                    self.assertEqual(workflow["on"]["push"]["branches"], ["main"])
                    self.assertIn("concurrency", workflow)

    def test_all_workflows_use_processor_runner(self):
        for name, workflow in workflows().items():
            for job_name, job in workflow["jobs"].items():
                with self.subTest(workflow=name, job=job_name):
                    self.assertEqual(job["runs-on"], "actions-runner-processor")

    def test_component_routing(self):
        cases = {
            "backend/internal/service/auth.go": set(),
            "frontend/src/App.tsx": {"frontend-ci.yml"},
            "portal/internal/handler/admin.go": {"portal-ci.yml", "supervisor-ci.yml"},
            "supervisor/src/main.rs": {"supervisor-ci.yml"},
            "bench/main.go": {"supervisor-ci.yml"},
            "router/internal/config/config.go": {"router-ci.yml"},
            "portal-mock/web/src/App.tsx": set(),
            "router/internal/control/server.go": {"router-ci.yml"},
            "gen/go/portal/v1/config.pb.go": {"router-ci.yml"},
            "specs/001-isureal-social-app/contracts/openapi.yaml": set(),
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


if __name__ == "__main__":
    unittest.main()
