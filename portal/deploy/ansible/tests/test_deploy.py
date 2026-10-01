import gzip
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from jinja2 import Environment, FileSystemLoader, StrictUndefined
import yaml

ROOT = Path(__file__).resolve().parents[1]


class DeploymentTests(unittest.TestCase):
    def test_secrets_are_stable_and_adopt_existing_env(self):
        script = ROOT / "roles/config/files/ensure_secrets.py"
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "env").write_text(
                "ISUXPORTAL_ADMIN_PASSWORD=existing-admin\nISUXPORTAL_SESSION_SECRET=existing-session\nISUXPORTAL_DATABASE_URL=postgres://isuxportal:existing-db@postgres/isuxportal\n"
            )
            for expected in ("CREATED", "PRESENT"):
                result = subprocess.run(
                    ["python3", str(script), str(root)], capture_output=True, text=True
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.strip(), expected)
                data = json.loads((root / "secrets.json").read_text())
                self.assertEqual(data["portal"]["admin_password"], "existing-admin")
                self.assertEqual(data["portal"]["session_secret"], "existing-session")
                self.assertEqual(data["postgres"]["password"], "existing-db")
                self.assertEqual((root / "secrets.json").stat().st_mode & 0o777, 0o600)
                if expected == "CREATED":
                    first = data
                else:
                    self.assertEqual(data, first)

    def context(self):
        defaults = yaml.safe_load((ROOT / "vars/defaults.yml").read_text())
        return {
            **defaults,
            "app": defaults["app_defaults"],
            "postgres": {**defaults["postgres_defaults"], "password": "test-db"},
            "redis": defaults["redis_defaults"],
            "portal": defaults["portal_defaults"],
            "app_fqdn": "portal.example.test",
            "stage": "develop",
            "portal_image": "isuxportal:test",
        }

    def render(self, name):
        env = Environment(
            loader=FileSystemLoader(ROOT / "roles/app/templates"),
            undefined=StrictUndefined,
        )
        env.filters["to_json"] = json.dumps
        return env.get_template(name).render(**self.context())

    def test_postgres18_storage_and_private_services(self):
        stack = yaml.safe_load(self.render("docker-compose.yml"))
        pg = stack["services"]["postgres"]
        self.assertTrue(pg["image"].startswith("postgres:18.6-alpine@sha256:"))
        self.assertEqual(pg["volumes"], ["pgdata18:/var/lib/postgresql"])
        self.assertIn("pgdata18", stack["volumes"])
        self.assertNotIn("ports", pg)
        self.assertNotIn("ports", stack["services"]["redis"])
        self.assertTrue(
            stack["services"]["redis"]["image"].startswith("redis:8.2.9-alpine@sha256:")
        )
        self.assertEqual(stack["services"]["portal"]["image"], "isuxportal:test")
        self.assertNotIn("bot", stack["services"])

    def test_schedule_is_unset_and_admin_only_by_default(self):
        ctx = self.context()
        self.assertTrue(ctx["portal"]["admin_only"])
        self.assertTrue(all(v == "" for v in ctx["portal"]["timing"].values()))

    def test_nginx_health_and_internal_route_protection(self):
        conf = self.render("nginxsite.conf")
        self.assertIn("location = /site/sha", conf)
        self.assertIn("proxy_method GET;", conf)
        self.assertIn("location = /admin/long_running_check", conf)
        self.assertIn("location /debug/", conf)

    def test_backup_failure_preserves_last_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            script = (
                self.render("backup.sh")
                .replace("/var/backups/portal", str(root / "backups"))
                .replace("/run/lock/portal-backup.lock", str(root / "backup.lock"))
            )
            (root / "backup.sh").write_text(script)
            (root / "docker").write_text(
                '#!/bin/sh\ncase "$1" in ps) echo test-container;; exec) echo incomplete; exit 42;; esac\n'
            )
            (root / "docker").chmod(0o755)
            (root / "flock").write_text("#!/bin/sh\nexit 0\n")
            (root / "flock").chmod(0o755)
            (root / "backups").mkdir()
            dest = root / "backups" / "isuxportal-2000-01-01.sql.gz"
            dest.write_bytes(b"last successful backup")
            result = subprocess.run(
                ["bash", str(root / "backup.sh")],
                env={**os.environ, "PATH": str(root) + ":" + os.environ["PATH"]},
                capture_output=True,
            )
            self.assertEqual(result.returncode, 42, result.stderr)
            self.assertEqual(list((root / "backups").iterdir()), [dest])
            self.assertEqual(dest.read_bytes(), b"last successful backup")
            (root / "docker").write_text(
                '#!/bin/sh\ncase "$1" in ps) echo test-container;; exec) echo complete;; esac\n'
            )
            result = subprocess.run(
                ["bash", str(root / "backup.sh")],
                env={**os.environ, "PATH": str(root) + ":" + os.environ["PATH"]},
                capture_output=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            files = [p for p in (root / "backups").iterdir() if p != dest]
            self.assertEqual(len(files), 1)
            self.assertEqual(gzip.decompress(files[0].read_bytes()), b"complete\n")


if __name__ == "__main__":
    unittest.main()
