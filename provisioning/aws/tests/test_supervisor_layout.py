"""Check that the consolidated deployment can be used from its documented path."""

import json
import pathlib
import subprocess
import tempfile
import unittest

AWS = pathlib.Path(__file__).resolve().parents[1]
SUPERVISOR = AWS / "supervisor"
POOL = SUPERVISOR / "pool"


class SupervisorLayoutTest(unittest.TestCase):
    def test_pool_cli_builds_and_exports_from_consolidated_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            task = root / "task.json"
            service = root / "service.json"
            task.write_text(
                json.dumps({"containerDefinitions": [{"name": "supervisor"}]})
            )
            service.write_text(
                json.dumps(
                    {
                        "clusterArn": "test-cluster",
                        "networkConfiguration": {
                            "awsvpcConfiguration": {"subnets": ["test-subnet"]}
                        },
                    }
                )
            )
            built = subprocess.run(
                ["python3", "render.py", "build", "pool.json", str(task), str(service)],
                cwd=POOL,
                capture_output=True,
                text=True,
                check=True,
            )
            deployments = json.loads(built.stdout)
            subprocess.run(
                ["python3", "render.py", str(root / "generated")],
                input=json.dumps({"deployments": deployments}),
                cwd=POOL,
                capture_output=True,
                text=True,
                check=True,
            )
            output = root / "generated" / "isucon2026-shared-supervisor"
            config = json.loads((output / "ecspresso.json").read_text())
            self.assertEqual(config["cluster"], "test-cluster")
            for key in ("task_definition", "service_definition"):
                self.assertTrue((output / config[key]).is_file())
            self.assertEqual(
                json.loads((output / config["service_definition"]).read_text())[
                    "networkConfiguration"
                ],
                {
                    "awsvpcConfiguration": {
                        "subnets": ["test-subnet"],
                        "assignPublicIp": "ENABLED",
                    }
                },
            )

    def test_stack_identities_and_typescript_resolution_are_preserved(self):
        for directory, project, backend in (
            (SUPERVISOR, "isucon2026-trial-supervisor", "supervisor"),
            (POOL, "isucon2026-shared-supervisor", "shared-supervisor"),
        ):
            with self.subTest(project=project):
                self.assertIn(
                    f"name: {project}\n", (directory / "Pulumi.yaml").read_text()
                )
                self.assertIn(
                    f'PULUMI_BACKEND_URL = "s3://isucon2026-pulumi-state-124502390568/{backend}"',
                    (directory / "mise.toml").read_text(),
                )
        config = json.loads((POOL / "tsconfig.json").read_text())
        self.assertEqual((POOL / config["extends"]).resolve(), AWS / "tsconfig.json")
        self.assertFalse((AWS / "shared-supervisor").exists())


if __name__ == "__main__":
    unittest.main()
