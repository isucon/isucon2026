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
                {"name": "ISUXBENCH_IMAGE_DIGESTS", "value": "/old/digests.bin"},
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
        check=False,
        env={**os.environ, "ISUXBENCH_TARGET": address},
        capture_output=True,
        text=True,
    )


class SharedTest(unittest.TestCase):
    def test_public_ip_is_enabled_even_with_private_template(self):
        service = copy.deepcopy(SERVICE)
        service["networkConfiguration"]["awsvpcConfiguration"]["assignPublicIp"] = (
            "DISABLED"
        )
        original = copy.deepcopy(service)
        result = build(TASK, service, CONFIG)["serviceDefinition"]
        network = result["networkConfiguration"]["awsvpcConfiguration"]
        self.assertEqual(network["assignPublicIp"], "ENABLED")
        self.assertEqual(network["subnets"], ["subnet"])
        self.assertEqual(network["securityGroups"], ["sg"])
        self.assertEqual(service, original)

    def test_runtime_platform_is_arm64_even_with_x86_template(self):
        for platform in (
            None,
            {"cpuArchitecture": "X86_64", "operatingSystemFamily": "LINUX"},
        ):
            with self.subTest(platform=platform):
                task = copy.deepcopy(TASK)
                if platform is not None:
                    task["runtimePlatform"] = platform
                original = copy.deepcopy(task)
                result = build(task, SERVICE, CONFIG)["taskDefinition"]
                self.assertEqual(
                    result["runtimePlatform"],
                    {"cpuArchitecture": "ARM64", "operatingSystemFamily": "LINUX"},
                )
                self.assertEqual(task, original)

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

    def test_shared_pool_removes_filters(self):
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
        self.assertNotIn("ISUXBENCH_IMAGE_DIGESTS", env)
        self.assertEqual(result["serviceDefinition"]["desiredCount"], 3)
        self.assertEqual(result["taskDefinition"]["family"], "shared-test")
        self.assertEqual(c["image"], CONFIG["image"])
        self.assertEqual(c["secrets"], task["containerDefinitions"][0]["secrets"])
        self.assertEqual(task, original)
        script = c["command"][2]
        for address in ("192.0.2.1", "192.0.2.99"):
            r = run_proxy(script, address, "--only-post-validation")
            self.assertEqual(r.returncode, 2)
            self.assertIn("Router registration required", r.stderr)
        self.assertEqual(run_proxy(script, "203.0.113.99", "--host", "").returncode, 2)
        self.assertNotIn("case", script)

    def test_timeouts_and_photo_archive(self):
        config = {
            **CONFIG,
            "benchTimeout": "300s",
            "hardTimeout": "310",
            "benchImages": "/opt/isucon/photos/merged.zip",
            "benchImageDigests": "/opt/isucon/photos/image-digests-large.bin",
        }
        result = build(copy.deepcopy(TASK), SERVICE, config)
        c = result["taskDefinition"]["containerDefinitions"][0]
        env = {e["name"]: e["value"] for e in c["environment"]}
        self.assertEqual(env["ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT"], "310")
        self.assertEqual(env["ISUXBENCH_IMAGES"], "/opt/isucon/photos/merged.zip")
        self.assertEqual(
            env["ISUXBENCH_IMAGE_DIGESTS"], "/opt/isucon/photos/image-digests-large.bin"
        )
        self.assertEqual(
            [e["name"] for e in c["environment"]],
            sorted(e["name"] for e in c["environment"]),
        )
        r = run_proxy(
            c["command"][2], "203.0.113.99", "--host", "app.team42-aws.127.0.0.1.nip.io"
        )
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
            ("benchImageDigests", "var/photos/image-digests-large.bin"),
            ("benchImageDigests", "/opt/isucon/photos/image-digests-large.bin\nX=1"),
        ]:
            with self.assertRaises(ValueError, msg=f"{key}={value!r}"):
                build(copy.deepcopy(TASK), SERVICE, {**CONFIG, key: value})

    def test_checked_in_deployment_matches_generated_task(self):
        root = pathlib.Path(__file__).parent
        config = json.loads((root / "pool.json").read_text())
        self.assertNotIn("routes", config)
        line = next(
            line
            for line in (root / "Pulumi.portal-dev.yaml").read_text().splitlines()
            if ":deployments:" in line
        )
        deployments = json.loads(
            line.split(":deployments: ", 1)[1][1:-1].replace("''", "'")
        )
        for deployment in deployments:
            task = deployment["taskDefinition"]
            service = {
                **deployment["serviceDefinition"],
                "clusterArn": deployment["ecspressoConfig"]["cluster"],
            }
            expected = build(task, service, config)
            self.assertEqual(task, expected["taskDefinition"])
            self.assertEqual(
                deployment["serviceDefinition"], expected["serviceDefinition"]
            )

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
