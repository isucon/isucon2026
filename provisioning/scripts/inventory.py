#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
from typing import Any

import yaml


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_TOPOLOGY = REPO_ROOT / "provisioning" / "config" / "topology.yml"
DEFAULT_RELEASE = REPO_ROOT / "provisioning" / "config" / "release.json"
VAGRANT_DIR = REPO_ROOT / "provisioning" / "vagrant"


def ssh_config(node: str) -> dict[str, str]:
    output = subprocess.run(
        ["vagrant", "ssh-config", node],
        check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        cwd=VAGRANT_DIR,
    ).stdout
    result: dict[str, str] = {}
    for line in output.splitlines():
        match = re.match(r"\s*(HostName|User|Port|IdentityFile)\s+(.+?)\s*$", line)
        if match:
            result[match.group(1)] = match.group(2).strip('"')
    missing = {"HostName", "User", "Port", "IdentityFile"} - result.keys()
    if missing:
        raise ValueError(f"{node} ssh-config is missing {sorted(missing)}")
    return result


def load_secret(path: Path) -> dict[str, Any]:
    if path.exists():
        return yaml.safe_load(path.read_text())
    value = {
        "schemaVersion": 1,
        "databasePasswords": {node: secrets.token_hex(24) for node in ("server1", "server2", "server3")},
        "cursorHMACKey": secrets.token_hex(48),
    }
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(yaml.safe_dump(value, sort_keys=True))
    path.chmod(0o600)
    return value


def main() -> int:
    parser = argparse.ArgumentParser(description="Render environment-scoped Ansible inventory from Vagrant state")
    parser.add_argument("--topology", type=Path, default=Path(os.environ.get("VM_TOPOLOGY", DEFAULT_TOPOLOGY)))
    parser.add_argument("--release", type=Path, default=Path(os.environ.get("VM_RELEASE", DEFAULT_RELEASE)))
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    topology: dict[str, Any] = yaml.safe_load(args.topology.resolve().read_text())
    release = json.loads(args.release.resolve().read_text())
    environment_id = os.environ.get("VM_ENV_ID", topology["environmentId"])
    if not re.fullmatch(r"[a-z][a-z0-9-]{2,31}", environment_id):
        raise ValueError(f"invalid VM_ENV_ID: {environment_id}")
    state_dir = REPO_ROOT / "provisioning" / ".state" / environment_id
    output = args.output or state_dir / "inventory.yml"
    secret = load_secret(state_dir / "runtime-secrets.yml")
    artifact_root = (
        REPO_ROOT
        / "provisioning"
        / ".state"
        / "artifacts"
        / release["releaseId"]
        / "bundles"
    )
    host_vars: dict[str, Any] = {}
    for node_id, node in topology["nodes"].items():
        ssh = ssh_config(node_id)
        variables: dict[str, Any] = {
            "ansible_host": ssh["HostName"],
            "ansible_port": int(ssh["Port"]),
            "ansible_user": ssh["User"],
            "ansible_ssh_private_key_file": ssh["IdentityFile"],
            "isucon_node_id": node_id,
            "isucon_node": node,
        }
        if node["role"] == "problem-server":
            segment = node["interfaces"][1]["segment"]
            variables["isucon_data_address"] = topology["networks"][segment]["peerAddress"]
            variables["isucon_router_address"] = topology["networks"][segment]["routerAddress"]
            variables["isucon_database_password"] = secret["databasePasswords"][node_id]
        host_vars[node_id] = variables

    inventory = {
        "all": {
            "vars": {
                "ansible_python_interpreter": "/usr/bin/python3",
                "isucon_environment_id": environment_id,
                "isucon_topology": topology,
                "isucon_release_id": release["releaseId"],
                "isucon_release_manifest_path": str(args.release.resolve()),
                "isucon_artifact_root": str(artifact_root),
                "isucon_cursor_hmac_key": secret["cursorHMACKey"],
                "isucon_image_build": False,
            },
            "children": {
                "problem_servers": {"hosts": {name: host_vars[name] for name in ("server1", "server2", "server3")}},
                "routers": {"hosts": {"router": host_vars["router"]}},
            },
        }
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_suffix(".tmp")
    temporary.write_text(yaml.safe_dump(inventory, sort_keys=False))
    temporary.chmod(0o600)
    os.replace(temporary, output)
    print(output)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as exc:
        print(f"inventory: {exc}", file=sys.stderr)
        raise SystemExit(4)
