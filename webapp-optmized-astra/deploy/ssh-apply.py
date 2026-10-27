"""Apply the Pulumi-referenced runtime configuration to existing Team 10 VMs.

No AWS infrastructure resources are created. Transfer files with scp and execute
configuration scripts over SSH through the existing Instance Connect Endpoint.
"""
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import shlex
import subprocess
import sys
import tarfile

ROOT = Path(__file__).resolve().parents[2]
DEPLOY = ROOT / "webapp-optmized-astra/deploy"
runtime = json.loads((DEPLOY / "runtime.json").read_text())
mode = sys.argv[1] if len(sys.argv) > 1 else "all"
assert mode in ("all", "application", "database", "replication")
read_fence = runtime.get("readFenceEnabled", False)
assert not runtime["replicaReads"] or runtime["replicasSynchronous"] or read_fence
ssh = str(DEPLOY / "ssh.sh")
database = runtime["database"]["instanceId"]
applications = runtime["applications"]
replicas = [app for app in applications if "replicaName" in app]
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


def application(app):
    instance = app["instanceId"]
    copy(instance, ROOT / runtime["archivePath"], "/home/isucon/astra-release.tar.gz")
    read_host = "127.0.0.1" if runtime["replicaReads"] and "replicaName" in app else ""
    fenced = "1" if read_host and read_fence else "0"
    remote(instance, "sudo bash /home/isucon/astra-deploy/application.sh /home/isucon/astra-release.tar.gz " + shlex.quote(read_host) + " " + fenced)


def write_frontend(app):
    remote(app["instanceId"], "sudo bash /home/isucon/astra-deploy/write-routing.sh frontend")


# Restore local writes before stopping or replacing the private worker.
if mode in ("all", "application") and not write_proxy:
    parallel(write_frontend, remote_apps)


# Disable local reads before turning off synchronous replication on rollback.
if mode in ("all", "application") and not runtime["replicaReads"]:
    parallel(application, applications)

# In a combined deployment, readers must gain fences before sync commits stop.
early_fenced_apps = mode == "all" and runtime["replicaReads"] and read_fence and not runtime["replicasSynchronous"]
if early_fenced_apps:
    parallel(application, applications)

if mode in ("all", "database"):
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
    if not runtime["replicasSynchronous"]:
        for app in replicas:
            remote(app["instanceId"], "if systemctl show webapp-optmized-astra -p Environment --value | grep -q ISUREAL_DB_READ_HOST=127.0.0.1 && ! systemctl show webapp-optmized-astra -p Environment --value | grep -q ISUREAL_READ_FENCE=1; then echo 'Enable read fences or disable local reads before disabling synchronous replication' >&2; exit 1; fi")
    remote(database, "sudo bash /home/isucon/astra-deploy/replication-primary.sh " + synchronous)

    def standby(app):
        remote(app["instanceId"], "sudo bash /home/isucon/astra-deploy/replication-standby.sh " + shlex.quote(app["replicaName"]))

    parallel(standby, replicas)

if mode in ("all", "application") and runtime["replicaReads"] and not early_fenced_apps:
    parallel(application, applications)

if mode in ("all", "application"):
    remote(write_host, "sudo bash /home/isucon/astra-deploy/write-routing.sh backend")
    if write_proxy:
        parallel(write_frontend, remote_apps)

print(f"Applied {mode} configuration to existing Team 10 VMs", flush=True)
