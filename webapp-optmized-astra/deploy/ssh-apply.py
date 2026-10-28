"""Apply the Pulumi-referenced runtime configuration to existing Team 10 VMs.

No AWS infrastructure resources are created. Transfer files with scp and execute
configuration scripts over SSH through the existing Instance Connect Endpoint.
"""
from concurrent.futures import ThreadPoolExecutor
import json
import os
import secrets
from pathlib import Path
import shlex
import subprocess
import sys
import tarfile
from runtime_topology import metadata_replicas, read_settings, read_scope

ROOT = Path(__file__).resolve().parents[2]
DEPLOY = ROOT / "webapp-optmized-astra/deploy"
runtime = json.loads((DEPLOY / "runtime.json").read_text())
mode = sys.argv[1] if len(sys.argv) > 1 else "all"
assert mode in ("all", "application", "database", "replication", "image-store", "image-gc", "upgrade")
read_fence = runtime.get("readFenceEnabled", False)
write_fence = runtime.get("writeFenceEnabled", False)
write_fence_mode = runtime.get("writeFenceMode", "poll")
assert write_fence_mode in ("poll", "native")
native_fence = write_fence_mode == "native"
assert not native_fence or write_fence
assert not runtime["replicaReads"] or runtime["replicasSynchronous"] or read_fence or write_fence
ssh = str(DEPLOY / "ssh.sh")
database = runtime["database"]["instanceId"]
applications = runtime["applications"]
replicas = metadata_replicas(runtime)
write_replicas = [app for app in replicas if app["region"] != "asia"]
write_fence_hosts = ",".join(app["host"] for app in write_replicas) if write_fence else ""
assert not write_fence or (runtime["replicasEnabled"] and len(write_replicas) == 2)
write_proxy = runtime.get("writeProxyEnabled", False)
write_host = next(app for app in applications if app["region"] == "asia")["instanceId"]
remote_apps = [app for app in applications if app["region"] != "asia"]


def remote(instance, command):
    subprocess.run([ssh, instance, command], check=True)


def copy(instance, source, target):
    subprocess.run([ssh, instance, "--copy", str(source), target], check=True)


def parallel(function, items):
    with ThreadPoolExecutor(max_workers=3) as pool:
        list(pool.map(function, items))


def require_write_fences():
    # Check actual running writers before readers can lose their own barriers.
    # The two-stage deployment first enables write fences with read fences on.
    services = [(app["instanceId"], "webapp-optmized-astra") for app in applications]
    if write_proxy:
        services.append((write_host, "webapp-optmized-astra-writer"))
    expected = shlex.quote("ISUREAL_WRITE_FENCE_HOSTS=" + write_fence_hosts)
    for instance, service in services:
        remote(instance, f"systemctl is-active --quiet {service} && systemctl show {service} -p Environment --value | tr ' ' '\\n' | grep -Fxq {expected} || {{ echo 'Enable write fences on every writer with read fences still enabled first' >&2; exit 1; }}")
        remote(instance, f"systemctl show {service} -p Environment --value | tr ' ' '\\n' | grep -Fxq ISUREAL_WRITE_FENCE_MODE={write_fence_mode} || {{ echo 'Deploy the requested fence mode with synchronous replication enabled first' >&2; exit 1; }}")


if runtime["replicaReads"] and not runtime["replicasSynchronous"] and not read_fence:
    require_write_fences()


image_store = runtime.get("imageStore", {})
if image_store.get("writesEnabled", False):
    assert image_store.get("enabled", False)
    services = [(app["instanceId"], "webapp-optmized-astra") for app in applications]
    if write_proxy:
        services.append((write_host, "webapp-optmized-astra-writer"))
    for instance, service in services:
        for key, value in (("HOST", image_store["host"]), ("PORT", image_store["port"])):
            expected = shlex.quote(f"ISUREAL_IMAGE_STORE_{key}={value}")
            remote(instance, f"systemctl is-active --quiet {service} && systemctl show {service} -p Environment --value | tr ' ' '\\n' | grep -Fxq {expected} || {{ echo 'Deploy image store readers with writes disabled on every application first' >&2; exit 1; }}")
elif not image_store.get("enabled", False):
    check = """DO $$ DECLARE used boolean; BEGIN
      IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='image_objects'::regclass AND attname='asset_key' AND NOT attisdropped) THEN
        EXECUTE 'SELECT EXISTS(SELECT 1 FROM image_objects WHERE asset_key IS NOT NULL)' INTO used;
        IF used THEN RAISE EXCEPTION 'Retain image store readers while published external images exist'; END IF;
      END IF;
    END $$"""
    remote(database, "sudo -u postgres psql -X -v ON_ERROR_STOP=1 -d isureal -c " + shlex.quote(check))


regional_stores = runtime.get("regionalImageStoresEnabled", False)
regional_writes = runtime.get("regionalImageWritesEnabled", False)
assert not regional_writes or regional_stores and image_store.get("writesEnabled", False) and runtime.get("regionalUploadCacheEnabled", False)
if not regional_stores:
    check = "SELECT 1 / (NOT EXISTS(SELECT 1 FROM image_objects WHERE asset_key LIKE 'europe:%' OR asset_key LIKE 'america:%'))::int"
    remote(database, "sudo -u postgres psql -X -v ON_ERROR_STOP=1 -d isureal -c " + shlex.quote(check))
if regional_writes:
    for instance, service in services:
        remote(instance, "systemctl show " + service + " -p Environment --value | grep -q 'ISUREAL_REGIONAL_IMAGE_HOSTS_JSON=.*europe.*america' || { echo 'Deploy regional image readers everywhere before enabling writes' >&2; exit 1; }")


files = runtime.get("fileImageStore", {})
if not files.get("enabled", False):
    check = "SELECT 1 / (NOT EXISTS(SELECT 1 FROM image_objects WHERE asset_key LIKE 'fs:%' OR asset_key LIKE 'pack:%'))::int"
    remote(database, "sudo -u postgres psql -X -v ON_ERROR_STOP=1 -d isureal -c " + shlex.quote(check))
if not files.get("archiveReadsEnabled",False):
    check = "SELECT 1 / (NOT EXISTS(SELECT 1 FROM image_objects WHERE asset_key LIKE 'pack:%'))::int"
    remote(database, "sudo -u postgres psql -X -v ON_ERROR_STOP=1 -d isureal -c " + shlex.quote(check))
if files.get("writesEnabled", False):
    assert files.get("enabled", False) and regional_writes
    for instance, service in services:
        remote(instance, "systemctl is-active --quiet " + service + " && systemctl show " + service + " -p Environment --value | grep -q 'ISUREAL_FILE_IMAGE_ROOT=/var/lib/astra-images' || { echo 'Deploy file image readers everywhere before enabling writes' >&2; exit 1; }")


bundle = ROOT / "var/astra/configuration.tar.gz"
bundle.parent.mkdir(parents=True, exist_ok=True)
with tarfile.open(bundle, "w:gz") as tar:
    tar.add(DEPLOY / "runtime.json", arcname="runtime.json")
    for path in sorted(DEPLOY.glob("*.sh")):
        tar.add(path, arcname=path.name)
    for path in sorted((ROOT / "webapp-optmized-astra/sql").glob("0??-*.sql")):
        if int(path.name[:3]) >= 4:
            tar.add(path, arcname=path.name)


def stage(instance):
    copy(instance, bundle, "/tmp/astra-configuration.tar.gz")
    remote(instance, "mkdir -p /home/isucon/astra-deploy && tar -xzf /tmp/astra-configuration.tar.gz -C /home/isucon/astra-deploy")


instances = [database] + [app["instanceId"] for app in applications]
parallel(stage, instances)

# Provision the independent cluster only on an existing application VM. Readers
# must be deployed with writes disabled before enabling new image publication.
image_store = runtime.get("imageStore", {})
if mode in ("all", "image-store") and image_store.get("enabled", False):
    assert image_store["instanceId"] == write_host
    stores = applications if regional_stores else [app for app in applications if app['region']=='asia']
    def provision_store(app):
        remote(app['instanceId'], "sudo bash /home/isucon/astra-deploy/image-store.sh " + shlex.quote(app['region']))
    parallel(provision_store, stores)


if mode in ("all", "application", "upgrade") and regional_writes:
    secret_file = ROOT / runtime["regionalUploadEnvPath"]
    secret_file.parent.mkdir(parents=True, exist_ok=True)
    if not secret_file.exists():
        fd = os.open(secret_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as file:
            file.write('ISUREAL_UPLOAD_HANDOFF_KEY='+secrets.token_hex(32)+'\n')
    secret_file.chmod(0o600)
    def upload_credentials(app):
        copy(app['instanceId'], secret_file, '/home/isucon/.astra-upload.env')
        remote(app['instanceId'], 'chmod 600 /home/isucon/.astra-upload.env')
    parallel(upload_credentials, applications)


def application(app):
    instance = app["instanceId"]
    copy(instance, ROOT / runtime["archivePath"], "/home/isucon/astra-release.tar.gz")
    read_host, fenced = read_settings(runtime, app)
    fenced = str(int(fenced))
    remote(instance, "sudo bash /home/isucon/astra-deploy/application.sh /home/isucon/astra-release.tar.gz " + shlex.quote(read_host) + " " + fenced + " " + shlex.quote(write_fence_hosts) + " " + shlex.quote(write_fence_mode) + " " + shlex.quote(app["region"]) + " " + shlex.quote(read_scope(runtime, app)))


def write_frontend(app):
    remote(app["instanceId"], "sudo bash /home/isucon/astra-deploy/write-routing.sh frontend " + shlex.quote(app["region"]))


# Coordinated schema/application upgrades keep the existing compatibility
# checks above while all writers are running, then stop requests for migration.
if mode == "upgrade":
    import importlib.util
    spec = importlib.util.spec_from_file_location("asset_gc", DEPLOY / "asset-gc.py")
    gc = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(gc)
    gc.ensure_idle()
    def stop_for_upgrade(app):
        units = "webapp-optmized-astra webapp-optmized-astra-writer" if app["region"] == "asia" else "webapp-optmized-astra"
        remote(app["instanceId"], "sudo systemctl stop " + units)
    parallel(stop_for_upgrade, applications)

# Restore local writes before stopping or replacing the private worker.
if mode in ("all", "application", "upgrade") and not write_proxy:
    parallel(write_frontend, remote_apps)


# Disable local reads before turning off synchronous replication on rollback.
if mode in ("all", "application", "upgrade") and not runtime["replicaReads"]:
    parallel(application, applications)

# In a combined deployment, readers must gain fences before sync commits stop.
early_fenced_apps = mode == "all" and runtime["replicaReads"] and read_fence and not runtime["replicasSynchronous"]
if early_fenced_apps:
    parallel(application, applications)

if mode in ("all", "database", "upgrade"):
    remote(database, "sudo bash /home/isucon/astra-deploy/database.sh")

if mode in ("all", "replication") and runtime["replicasEnabled"]:
    secret_file = ROOT / runtime["replicationEnvPath"]
    if not secret_file.exists():
        raise RuntimeError("Missing private replication env file; restore it from a Team 10 VM before deploying")
    secret_file.chmod(0o600)

    def credentials(instance):
        copy(instance, secret_file, "/home/isucon/.astra-replication.env")
        remote(instance, "chmod 600 /home/isucon/.astra-replication.env")

    parallel(credentials, [database] + [app["instanceId"] for app in replicas])
    synchronous = str(runtime["replicasSynchronous"]).lower()
    if not runtime["replicasSynchronous"] and write_fence and not read_fence:
        require_write_fences()
    elif not runtime["replicasSynchronous"]:
        for app in replicas:
            remote(app["instanceId"], "if systemctl show webapp-optmized-astra -p Environment --value | grep -q ISUREAL_DB_READ_HOST=127.0.0.1 && ! systemctl show webapp-optmized-astra -p Environment --value | grep -q ISUREAL_READ_FENCE=1; then echo 'Enable read fences or disable local reads before disabling synchronous replication' >&2; exit 1; fi")
    remote(database, "sudo bash /home/isucon/astra-deploy/replication-primary.sh " + synchronous + " " + str(native_fence).lower() + " " + str(runtime.get("asiaReplicaEnabled", False)).lower())

    database_bytes = int(subprocess.check_output([ssh, database,
        "sudo -u postgres psql -XAt -d postgres -c 'SELECT sum(pg_database_size(oid))::bigint FROM pg_database'"], text=True).strip())
    assert database_bytes > 0
    def standby(app):
        remote(app["instanceId"], "sudo bash /home/isucon/astra-deploy/replication-standby.sh " + shlex.quote(app["replicaName"]) + " " + str(database_bytes))

    parallel(standby, replicas)

if mode in ("all", "application", "upgrade") and runtime["replicaReads"] and not early_fenced_apps:
    parallel(application, applications)

if mode in ("all", "application", "upgrade"):
    remote(write_host, "sudo bash /home/isucon/astra-deploy/write-routing.sh backend")
    parallel(write_frontend, applications)

if mode == "image-gc":
    subprocess.run([sys.executable, str(DEPLOY / "asset-gc.py")], check=True)

print(f"Applied {mode} configuration to existing Team 10 VMs", flush=True)
