import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import uuid

spec = importlib.util.spec_from_file_location("asset_gc", Path(__file__).with_name("asset-gc.py"))
gc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gc)


class CleanupTests(unittest.TestCase):
    def test_partial_stop_failure_restarts_every_attempted_service(self):
        runtime = {"applications": [
            {"instanceId": "asia", "region": "asia"},
            {"instanceId": "eu", "region": "europe"},
        ], "imageStore": {"enabled": True, "port": 5433, "instanceId": "asia"}}
        calls = []

        def remote(instance, command, **kwargs):
            calls.append((instance, command))
            if instance == "eu" and "systemctl stop" in command:
                raise RuntimeError("injected stop failure")

        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "runtime.json").write_text(json.dumps(runtime))
            with patch.object(gc, "DEPLOY", Path(directory)), patch.object(gc, "ensure_idle"), patch.object(gc, "remote", remote):
                with self.assertRaisesRegex(RuntimeError, "injected stop failure"):
                    gc.main()
        starts = [host for host, cmd in calls if "systemctl start" in cmd]
        self.assertCountEqual(starts, ["asia", "eu"])
        self.assertFalse(any("DELETE FROM" in cmd for _, cmd in calls))

    @unittest.skipUnless(os.getenv("ASTRA_GC_TEST_CONTAINER"), "requires isolated PostgreSQL test container")
    def test_gc_preserves_live_bytes_and_aborts_on_missing_or_miscounted_refs(self):
        container = os.environ["ASTRA_GC_TEST_CONTAINER"]
        schema = "astra_gc_" + uuid.uuid4().hex
        refs_path = "/tmp/" + schema
        kept, orphan, missing = [str(uuid.uuid4()) for _ in range(3)]

        def sql(source, check=True):
            return subprocess.run(["docker", "exec", "-i", container, "psql", "-XqAt", "-U", "postgres", "-v", "ON_ERROR_STOP=1"],
                input=f"SET search_path={schema};\n{source}", text=True, capture_output=True, check=check)

        def refs(keys):
            subprocess.run(["docker", "exec", "-i", container, "sh", "-c", f"umask 077; cat > {refs_path}"],
                input="".join(key + "\n" for key in keys), text=True, check=True)

        def cleanup(expected):
            source = gc.cleanup_sql("/var/lib/astra-maint/" + "a" * 32 + "/refs", expected)
            return source.replace("/var/lib/astra-maint/" + "a" * 32 + "/refs", refs_path)

        sql(f"CREATE SCHEMA {schema}; CREATE TABLE astra_image_data(id varchar(36) PRIMARY KEY,data bytea NOT NULL); INSERT INTO astra_image_data VALUES ('{kept}',decode('1234','hex')),('{orphan}',decode('abcd','hex'));")
        try:
            refs([kept, missing])
            result = sql(cleanup(2), check=False)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("published image is missing", result.stderr)
            self.assertEqual(sql("SELECT count(*) FROM astra_image_data").stdout.strip(), "2")
            refs([kept])
            result = sql(cleanup(2), check=False)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("live asset count mismatch", result.stderr)
            self.assertEqual(sql("SELECT count(*) FROM astra_image_data").stdout.strip(), "2")
            sql(cleanup(1))
            self.assertEqual(sql("SELECT id||':'||encode(data,'hex') FROM astra_image_data").stdout.strip(), kept + ":1234")
        finally:
            sql(f"DROP SCHEMA {schema} CASCADE")
            subprocess.run(["docker", "exec", container, "rm", "-f", refs_path], check=True)


if __name__ == "__main__":
    unittest.main()
