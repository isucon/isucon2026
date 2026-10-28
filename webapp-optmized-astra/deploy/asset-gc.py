"""Collect unreferenced asset rows with every application stopped.

Run only through apply.sh image-gc or verify-regional-restart.py --gc; both hold
the same local operation lock as the benchmark wrapper. Existing ECS tasks are also checked before stopping services.
Published assets and legacy image bytes are never removed.
"""
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import tempfile
import uuid

ROOT = Path(__file__).resolve().parents[2]
DEPLOY = ROOT / "webapp-optmized-astra/deploy"
SSH = str(DEPLOY / "ssh.sh")


def remote(instance, command, **kwargs):
    return subprocess.run([SSH, instance, command], check=True, **kwargs)


def ensure_idle():
    cluster = "isucon2026-bench-runner-dev"
    tasks = json.loads(subprocess.check_output([
        "aws", "ecs", "list-tasks", "--cluster", cluster, "--family", cluster,
        "--output", "json",
    ]))["taskArns"]
    if not tasks:
        return
    description = json.loads(subprocess.check_output([
        "aws", "ecs", "describe-tasks", "--cluster", cluster, "--tasks", *tasks,
        "--output", "json",
    ]))
    if description.get("failures"):
        raise RuntimeError("Could not verify existing benchmark tasks")
    for task in description["tasks"]:
        for container in task.get("overrides", {}).get("containerOverrides", []):
            if "app.team10-aws.127.0.0.1.nip.io" in container.get("command", []):
                raise RuntimeError("Team 10 benchmark is still running; leave it undisturbed")


def reference_sql(reference_path, expected_count):
    # The file is written by this script, consists only of validated UUIDs and
    # lives in a private, postgres-owned directory on the asset VM.
    assert re.fullmatch(r"/var/lib/astra-maint/[a-f0-9]+/refs", reference_path)
    assert isinstance(expected_count, int) and expected_count >= 0
    return f"""\\set ON_ERROR_STOP on
BEGIN;
CREATE TEMP TABLE live_assets(id varchar(36) PRIMARY KEY) ON COMMIT DROP;
\\copy live_assets FROM '{reference_path}'
DO $$ BEGIN
  IF (SELECT count(*) FROM live_assets) <> {expected_count} THEN
    RAISE EXCEPTION 'live asset count mismatch';
  END IF;
  IF EXISTS (SELECT 1 FROM live_assets l LEFT JOIN astra_image_data a ON a.id=l.id WHERE a.id IS NULL) THEN
    RAISE EXCEPTION 'a published image is missing; abort cleanup';
  END IF;
END $$;
"""


def cleanup_sql(reference_path, expected_count, compact=False):
    vacuum = "FULL, ANALYZE" if compact else "ANALYZE"
    return reference_sql(reference_path, expected_count) + f"""DELETE FROM astra_image_data a WHERE NOT EXISTS (SELECT 1 FROM live_assets l WHERE l.id=a.id);
COMMIT;
VACUUM ({vacuum}) astra_image_data;
SELECT count(*) AS retained_assets,pg_database_size(current_database()) AS database_bytes FROM astra_image_data;
"""


def split_references(keys, regions, file_regions=()):
    """Validate every published key before deleting from any configured store."""
    grouped = {region: [] for region in regions}
    grouped.update({"fs:"+region: [] for region in file_regions})
    if len(keys) != len(set(keys)):
        raise ValueError("Duplicate reference snapshot")
    for key in keys:
        if key.startswith("fs:"):
            kind, region, raw = key.split(":")
            region = kind+":"+region
        else:
            region, raw = key.split(":", 1) if ":" in key else ("asia", key)
        if region not in grouped or str(uuid.UUID(raw)) != raw:
            raise ValueError("Unknown region or invalid asset reference")
        grouped[region].append(raw)
    for values in grouped.values():
        if len(values) != len(set(values)):
            raise ValueError("Aliased reference snapshot")
    return grouped


def main():
    runtime = json.loads((DEPLOY / "runtime.json").read_text())
    store = runtime["imageStore"]
    assert store["enabled"] and store["port"] == 5433
    apps = runtime["applications"]
    asia = next(app for app in apps if app["region"] == "asia")
    assert store["instanceId"] == asia["instanceId"]
    stores = apps if runtime.get("regionalImageStoresEnabled", False) else [asia]
    services = [(app["instanceId"], "webapp-optmized-astra") for app in apps]
    if runtime.get("writeProxyEnabled"):
        services.append((asia["instanceId"], "webapp-optmized-astra-writer"))
    ensure_idle()
    token = uuid.uuid4().hex
    remote_dir = f"/var/lib/astra-maint/{token}"
    incoming = f"/home/isucon/.astra-gc-{token}"
    stopped = []
    try:
        # Stop every producer, then wait for in-flight metadata transactions.
        for instance, service in services:
            stopped.append((instance, service))
            remote(instance, f"sudo systemctl stop {service}")
        with tempfile.TemporaryDirectory(prefix="astra-asset-gc-") as directory:
            snapshot_path = Path(directory) / "snapshot"
            fd = os.open(snapshot_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            snapshot = "BEGIN; LOCK TABLE image_objects IN ACCESS EXCLUSIVE MODE; COPY (SELECT DISTINCT asset_key FROM image_objects WHERE asset_key IS NOT NULL ORDER BY asset_key) TO STDOUT; COMMIT;"
            with os.fdopen(fd, "wb") as output:
                remote(runtime["database"]["instanceId"],
                       "sudo -u postgres psql -XqAt -v ON_ERROR_STOP=1 -d isureal -c " + shlex.quote(snapshot),
                       stdout=output)
            grouped = split_references(snapshot_path.read_text().splitlines(),
                                       [app["region"] for app in stores],
                                       [app["region"] for app in apps] if runtime.get("fileImageStore",{}).get("enabled") else [])
            file_stores = apps if runtime.get("fileImageStore",{}).get("enabled") else []

            def file_command(app, mode):
                count = len(grouped["fs:"+app["region"]])
                return f"sudo python3 {incoming}/file-gc.py {mode} {remote_dir}/file-refs {count}"

            # Validate every published file before any store begins deleting.
            for app in file_stores:
                region, instance = app["region"], app["instanceId"]
                refs = Path(directory) / ("file-refs-"+region)
                refs.write_text("".join(key+"\n" for key in grouped["fs:"+region]))
                refs.chmod(0o600)
                remote(instance, "mkdir -p -m700 " + shlex.quote(incoming))
                subprocess.run([SSH,instance,"--copy",str(refs),incoming+"/file-refs"],check=True)
                subprocess.run([SSH,instance,"--copy",str(DEPLOY/"file-gc.py"),incoming+"/file-gc.py"],check=True)
                remote(instance, f"sudo install -d -o postgres -g postgres -m700 {remote_dir} && sudo install -o postgres -g postgres -m600 {incoming}/file-refs {remote_dir}/file-refs")
                remote(instance, file_command(app,"check"))

            def collect(app):
                region, instance = app["region"], app["instanceId"]
                folder = Path(directory) / region
                folder.mkdir(mode=0o700)
                refs, preflight, sql = folder / "refs", folder / "preflight.sql", folder / "cleanup.sql"
                refs.write_text("".join(key + "\n" for key in grouped[region]))
                preflight.write_text(reference_sql(remote_dir + "/refs", len(grouped[region])) +
                                     "SELECT coalesce(sum(octet_length(a.data)),0) FROM astra_image_data a JOIN live_assets l ON l.id=a.id; COMMIT;\n")
                for path in (refs, preflight):
                    path.chmod(0o600)
                remote(instance, "mkdir -p -m700 " + shlex.quote(incoming))
                for path in (refs, preflight):
                    subprocess.run([SSH, instance, "--copy", str(path), incoming + "/" + path.name], check=True)
                remote(instance,
                       "sudo install -d -o root -g root -m711 /var/lib/astra-maint && "
                       f"sudo install -d -o postgres -g postgres -m700 {remote_dir} && "
                       f"sudo install -o postgres -g postgres -m600 {incoming}/refs {remote_dir}/refs && "
                       f"sudo install -o postgres -g postgres -m600 {incoming}/preflight.sql {remote_dir}/preflight.sql")
                live_bytes = int(remote(instance,
                    f"sudo -u postgres psql -XqAt -p 5433 -d isureal_assets -f {remote_dir}/preflight.sql",
                    capture_output=True, text=True).stdout.strip())
                available = int(remote(instance, "df -B1 --output=avail /var/lib/postgresql | tail -n1",
                                       capture_output=True, text=True).stdout.strip())
                # A rewrite needs a second copy and WAL headroom. Without that
                # room, ordinary VACUUM still makes deleted pages reusable.
                compact = available > live_bytes * 2 + 1024**3
                sql.write_text(cleanup_sql(remote_dir + "/refs", len(grouped[region]), compact))
                sql.chmod(0o600)
                subprocess.run([SSH, instance, "--copy", str(sql), incoming + "/cleanup.sql"], check=True)
                remote(instance, f"sudo install -o postgres -g postgres -m600 {incoming}/cleanup.sql {remote_dir}/cleanup.sql")
                print(f"{region}: preserving {len(grouped[region])} references; live bytes={live_bytes}; free bytes={available}; compact={compact}", flush=True)
                remote(instance, f"sudo -u postgres psql -X -p 5433 -d isureal_assets -f {remote_dir}/cleanup.sql")
                if app in file_stores:
                    remote(instance, file_command(app,"apply"))
                print(f"{region}: cleanup and vacuum complete", flush=True)

            # Each region owns an independent database. Keep all producers
            # stopped until every deletion/vacuum has finished, even on failure.
            with ThreadPoolExecutor(max_workers=len(stores)) as pool:
                list(pool.map(collect, stores))
    finally:
        def start(item):
            instance, service = item
            host, port = (asia["host"], 8081) if service.endswith("-writer") else ("127.0.0.1", 8080)
            ready = f"""import socket,time
end=time.monotonic()+30
while True:
    try:
        socket.create_connection(({host!r},{port}),timeout=1).close()
        break
    except OSError:
        if time.monotonic()>=end: raise
        time.sleep(0.2)
"""
            remote(instance, f"sudo systemctl start {service} && systemctl is-active {service} && python3 -c " + shlex.quote(ready))
        try:
            with ThreadPoolExecutor(max_workers=4) as pool:
                list(pool.map(start, reversed(stopped)))
        finally:
            for app in stores:
                remote(app["instanceId"], f"sudo rm -rf -- {remote_dir} {incoming}")
    print("All regional asset cleanup complete; application processes restarted", flush=True)


if __name__ == "__main__":
    main()
