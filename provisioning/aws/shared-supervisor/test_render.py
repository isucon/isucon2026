import copy
import json
import os
import pathlib
import subprocess
import tempfile
import unittest

from render import build, export

TASK = {
    "cpu": "4096",
    "memory": "8192",
    "containerDefinitions": [
        {
            "name": "supervisor",
            "image": "repo@sha256:" + "0" * 64,
            "environment": [
                {"name": "ISUXPORTAL_SUPERVISOR_TEAM_ID", "value": "42"},
                {"name": "ISUXPORTAL_SUPERVISOR_INSTANCE_NAME", "value": "old-az"},
                {"name": "ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT", "value": "150"},
                {"name": "ISUXBENCH_IMAGES", "value": "/old/archive.zip"},
            ],
            "secrets": [{"name": "TOKEN", "valueFrom": "reference"}],
        }
    ],
}
SERVICE = {
    "clusterArn": "cluster",
    "networkConfiguration": {
        "awsvpcConfiguration": {"subnets": ["subnet"], "securityGroups": ["sg"]}
    },
}
CONFIG = {
    "name": "shared-test",
    "endpoint": "https://portal.example.com",
    "image": "repo@sha256:" + "a" * 64,
    "desiredCount": 3,
    "routes": {
        "192.0.2.1": {"target": "203.0.113.1", "host": "app.one.example.com"},
        "192.0.2.2": {"target": "203.0.113.2", "host": "app.two.example.com"},
    },
}


def run_proxy(script, address, *args):
    return subprocess.run(
        [
            "/bin/sh",
            "-c",
            script.replace("/usr/local/bin/isureal-bench", "/bin/echo"),
            "proxy",
            *args,
        ],
        env={**os.environ, "ISUXBENCH_TARGET": address},
        capture_output=True,
        text=True,
    )


class SharedTest(unittest.TestCase):
    def test_registered_router_uses_job_target_without_static_route(self):
        script = build(TASK, SERVICE, CONFIG)["taskDefinition"]["containerDefinitions"][
            0
        ]["command"][2]
        result = run_proxy(
            script,
            "203.0.113.99",
            "--host",
            "app.team42-aws.127.0.0.1.nip.io",
            "--only-post-validation",
            "--target",
            "203.0.113.99",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--target 203.0.113.99", result.stdout)
        self.assertIn("--host app.team42-aws.127.0.0.1.nip.io", result.stdout)
        self.assertIn("--only-post-validation", result.stdout)
        self.assertIn("--target-port 8080 --scale large", result.stdout)

    def test_shared_pool_routes_and_removes_filters(self):
        task = copy.deepcopy(TASK)
        original = copy.deepcopy(task)
        result = build(task, SERVICE, CONFIG)
        c = result["taskDefinition"]["containerDefinitions"][0]
        env = {e["name"]: e["value"] for e in c["environment"]}
        self.assertNotIn("ISUXPORTAL_SUPERVISOR_TEAM_ID", env)
        self.assertEqual(env["ISUXPORTAL_SUPERVISOR_INSTANCE_NAME"], "")
        self.assertEqual(env["ISUXPORTAL_SUPERVISOR_ENDPOINT_URL"], CONFIG["endpoint"])
        # 指定がなければ雛形の値を保ち、アーカイブの指定は雛形から引き継がない
        self.assertEqual(env["ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT"], "150")
        self.assertNotIn("ISUXBENCH_IMAGES", env)
        self.assertEqual(result["serviceDefinition"]["desiredCount"], 3)
        self.assertEqual(result["taskDefinition"]["family"], "shared-test")
        self.assertEqual(c["image"], CONFIG["image"])
        self.assertEqual(c["secrets"], task["containerDefinitions"][0]["secrets"])
        self.assertEqual(task, original)
        script = c["command"][2]
        for address, expected in [
            ("192.0.2.1", "203.0.113.1"),
            ("192.0.2.2", "203.0.113.2"),
        ]:
            r = run_proxy(script, address, "--only-post-validation")
            self.assertEqual(r.returncode, 0)
            self.assertIn(f"--only-post-validation --target {expected} ", r.stdout)
            self.assertTrue(
                r.stdout.rstrip().endswith("--target-port 8080 --scale large"), r.stdout
            )
        self.assertEqual(run_proxy(script, "192.0.2.99").returncode, 2)

    def test_timeouts_and_photo_archive(self):
        config = {
            **CONFIG,
            "benchTimeout": "300s",
            "hardTimeout": "310",
            "benchImages": "/opt/isucon/photos/merged.zip",
        }
        result = build(copy.deepcopy(TASK), SERVICE, config)
        c = result["taskDefinition"]["containerDefinitions"][0]
        env = {e["name"]: e["value"] for e in c["environment"]}
        self.assertEqual(env["ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT"], "310")
        self.assertEqual(env["ISUXBENCH_IMAGES"], "/opt/isucon/photos/merged.zip")
        self.assertEqual(
            [e["name"] for e in c["environment"]],
            sorted(e["name"] for e in c["environment"]),
        )
        r = run_proxy(c["command"][2], "192.0.2.1")
        self.assertTrue(
            r.stdout.rstrip().endswith(
                "--target-port 8080 --scale large --timeout 300s"
            ),
            r.stdout,
        )
        by_url = build(
            copy.deepcopy(TASK),
            SERVICE,
            {**config, "benchImages": "https://assets.example/merged.zip"},
        )
        self.assertIn(
            {"name": "ISUXBENCH_IMAGES", "value": "https://assets.example/merged.zip"},
            by_url["taskDefinition"]["containerDefinitions"][0]["environment"],
        )

    def test_rejects_invalid_config(self):
        for key, value in [
            ("image", "repo:latest"),
            ("endpoint", "http://example.com"),
            ("desiredCount", 0),
            ("benchTimeout", "300"),
            ("benchTimeout", "300s; exit 0"),
            ("hardTimeout", "310s"),
            ("benchImages", "var/photos/merged.zip"),
            ("benchImages", "http://assets.example/merged.zip"),
            ("benchImages", "/opt/isucon/photos/merged.zip\nX=1"),
            (
                "routes",
                {"192.0.2.1": {"target": "203.0.113.1", "host": "bad; command"}},
            ),
            ("routes", {}),
        ]:
            with self.assertRaises(ValueError, msg=f"{key}={value!r}"):
                build(copy.deepcopy(TASK), SERVICE, {**CONFIG, key: value})

    def test_export_writes_ecspresso_files(self):
        deployment = build(copy.deepcopy(TASK), SERVICE, CONFIG)
        with tempfile.TemporaryDirectory() as d:
            export([deployment], d)
            root = pathlib.Path(d) / "shared-test"
            self.assertEqual(
                json.loads((root / "task-definition.json").read_text()),
                deployment["taskDefinition"],
            )
            self.assertEqual(
                json.loads((root / "service-definition.json").read_text()),
                deployment["serviceDefinition"],
            )
            self.assertEqual(
                json.loads((root / "ecspresso.json").read_text())["service"],
                "shared-test",
            )


if __name__ == "__main__":
    unittest.main()
