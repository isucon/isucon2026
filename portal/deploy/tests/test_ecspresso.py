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

    def test_migration_only_has_database_secrets(self):
        task = self.render("migrate", "taskdef")
        self.assertEqual(task["containerDefinitions"][0]["command"], ["migrate"])
        self.assertEqual(
            {s["name"] for s in task["containerDefinitions"][0]["secrets"]},
            {"PGUSER", "PGPASSWORD"},
        )
        self.assertNotIn("loadBalancers", self.render("migrate", "servicedef"))

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
