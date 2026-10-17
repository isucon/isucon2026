"""Render the real ecspresso templates without AWS access."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


class EcspressoTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name)
        for source in (ROOT / "ecspresso").glob("*.jsonnet"):
            shutil.copy(source, self.path)
        self.infra = {
            "name": "portal-test",
            "region": "ap-northeast-1",
            "cluster": "portal-test",
            "repository": "123.dkr.ecr.ap-northeast-1.amazonaws.com/portal",
            "subnets": ["subnet-a", "subnet-c"],
            "securityGroup": "sg-task",
            "targetGroupArn": "arn:target",
            "logGroup": "/ecs/portal-test",
            "environment": {"ISUXPORTAL_ADMIN_ONLY": "1"},
            "secretKeys": ["ISUXPORTAL_SESSION_SECRET"],
            "applicationSecretArn": "arn:application",
            "databaseSecretArn": "arn:database",
            "sshSecretArn": "arn:ssh",
            "enableBot": False,
            "executionRoles": {
                name: "arn:exec-" + name
                for name in ("portal", "worker", "notifyloop", "bot", "migrate")
            },
            "taskRoles": {
                name: "arn:task-" + name
                for name in ("portal", "worker", "notifyloop", "bot", "migrate")
            },
        }
        (self.path / "infra.json").write_text(json.dumps(self.infra))

    def render(self, service, target, digest=None):
        result = subprocess.run(
            [
                os.environ.get("ECSPRESSO_BIN", "ecspresso"),
                "--config",
                str(self.path / "ecspresso.jsonnet"),
                "render",
                target,
            ],
            env=dict(
                os.environ,
                SERVICE=service,
                IMAGE_DIGEST=digest or "sha256:" + "a" * 64,
                AWS_EC2_METADATA_DISABLED="true",
            ),
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_fargate_security_and_immutable_image(self):
        task = self.render("portal", "taskdef")
        self.assertEqual(task["runtimePlatform"]["cpuArchitecture"], "ARM64")
        container = task["containerDefinitions"][0]
        self.assertTrue(container["readonlyRootFilesystem"])
        self.assertEqual(
            container["image"], self.infra["repository"] + "@sha256:" + "a" * 64
        )
        service = self.render("portal", "servicedef")
        self.assertEqual(service["desiredCount"], 2)
        self.assertEqual(
            service["networkConfiguration"]["awsvpcConfiguration"]["assignPublicIp"],
            "DISABLED",
        )
        self.assertEqual(service["loadBalancers"][0]["containerPort"], 8081)

    def test_standby_runs_only_portal_even_with_bot_enabled(self):
        self.infra.update(standby=True, enableBot=True)
        (self.path / "infra.json").write_text(json.dumps(self.infra))
        self.assertEqual(self.render("portal", "servicedef")["desiredCount"], 2)
        for service in ("worker", "notifyloop", "bot"):
            with self.subTest(service=service):
                self.assertEqual(self.render(service, "servicedef")["desiredCount"], 0)

    def test_create_and_update_preserve_service_defaults(self):
        for name in ("portal", "worker", "bot", "notifyloop"):
            with self.subTest(service=name):
                service = self.render(name, "servicedef")
                self.assertEqual(
                    service["availabilityZoneRebalancing"],
                    "DISABLED" if name in ("bot", "notifyloop") else "ENABLED",
                )
                self.assertEqual(service["propagateTags"], "NONE")
                self.assertEqual(
                    service["healthCheckGracePeriodSeconds"],
                    60 if name == "portal" else 0,
                )
                self.assertEqual(
                    self.render(name, "taskdef")["volumes"],
                    [{"name": "tmp", "host": {}}],
                )

    def test_migration_only_has_database_secrets(self):
        task = self.render("migrate", "taskdef")
        self.assertEqual(task["containerDefinitions"][0]["command"], ["migrate"])
        self.assertEqual(
            {s["name"] for s in task["containerDefinitions"][0]["secrets"]},
            {"PGUSER", "PGPASSWORD"},
        )
        self.assertNotIn("loadBalancers", self.render("migrate", "servicedef"))

    def test_discord_bot_token_is_limited_to_consumers(self):
        self.infra["secretKeys"].append("ISUXPORTAL_DISCORD_BOT_TOKEN")
        for enabled in (False, True):
            self.infra["enableBot"] = enabled
            (self.path / "infra.json").write_text(json.dumps(self.infra))
            for service in ("portal", "worker", "notifyloop", "bot", "migrate"):
                with self.subTest(enabled=enabled, service=service):
                    task = self.render(service, "taskdef")
                    names = {
                        s["name"] for s in task["containerDefinitions"][0]["secrets"]
                    }
                    self.assertEqual(
                        "ISUXPORTAL_DISCORD_BOT_TOKEN" in names,
                        service == "worker" or (service == "bot" and enabled),
                    )

    def test_singletons_and_worker_secret_isolation(self):
        for name in ("bot", "notifyloop"):
            definition = self.render(name, "servicedef")
            self.assertEqual(
                definition["deploymentConfiguration"]["maximumPercent"], 100
            )
            self.assertEqual(
                definition["deploymentConfiguration"]["minimumHealthyPercent"], 0
            )
        self.assertEqual(self.render("bot", "servicedef")["desiredCount"], 0)
        for name in ("portal", "worker", "bot", "notifyloop"):
            secrets = self.render(name, "taskdef")["containerDefinitions"][0]["secrets"]
            self.assertEqual(
                any(s["valueFrom"] == "arn:ssh" for s in secrets), name == "worker"
            )


if __name__ == "__main__":
    unittest.main()
