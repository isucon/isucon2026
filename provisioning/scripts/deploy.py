#!/usr/bin/env python3
"""Update application artifacts on an already provisioned local VM environment."""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
from datetime import datetime, timezone

import yaml

ROOT = Path(__file__).resolve().parents[2]
IGNORED = {"node_modules", "dist", ".git", "coverage", "test-results", "playwright-report", "__pycache__"}


def files_digest(root: Path) -> dict[str, str]:
    return {
        str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
        for p in sorted(root.rglob("*"))
        if p.is_file() and not (set(p.relative_to(root).parts) & IGNORED)
        and not p.name.endswith(".tsbuildinfo")
    }


def fingerprint(component: str) -> str:
    inputs = files_digest(ROOT / ("backend" if component == "backend" else "frontend"))
    for relative in ("provisioning/scripts/build-artifacts.sh", "mise.toml", "mise.lock",
                     "specs/001-isureal-social-app/contracts/openapi.yaml"):
        inputs[relative] = hashlib.sha256((ROOT / relative).read_bytes()).hexdigest()
    # Include compiler versions and build-affecting environment in cache identity.
    commands = [["go", "version"], ["go", "env", "GOFLAGS", "GOEXPERIMENT", "GOAMD64", "GOWORK"]] if component == "backend" else [["node", "--version"], ["npm", "--version"]]
    inputs["toolchain"] = [subprocess.check_output(c, text=True).strip() for c in commands]
    inputs["environment"] = {k: v for k, v in os.environ.items() if k.startswith("VITE_") or k in ("NODE_ENV", "NODE_OPTIONS")}
    return hashlib.sha256(json.dumps(inputs, sort_keys=True).encode()).hexdigest()


def build(component: str, cache: Path, env: dict[str, str]) -> tuple[Path, bool]:
    destination = cache / component / fingerprint(component)
    stamp = destination / "complete.json"
    if stamp.exists():
        try:
            recorded = json.loads(stamp.read_text())
            if recorded == files_digest(destination / "payload"):
                return destination / "payload", True
        except (OSError, ValueError):
            pass  # An incomplete/corrupt local cache is a cache miss.
    destination.mkdir(parents=True, exist_ok=True)
    stamp.unlink(missing_ok=True)
    payload = destination / "payload"
    subprocess.run([str(ROOT / "provisioning/scripts/build-artifacts.sh"), component],
                   env={**env, "ISUREAL_BUILD_ROOT": str(payload)}, check=True)
    stamp.write_text(json.dumps(files_digest(payload), sort_keys=True))
    # Bound disk use on the development host; retain this and one previous build.
    previous = sorted(
        (p for p in destination.parent.iterdir() if p != destination and p.is_dir()
         and re.fullmatch(r"[0-9a-f]{64}", p.name)),
        key=lambda p: p.stat().st_mtime, reverse=True,
    )
    for obsolete in previous[1:]:
        shutil.rmtree(obsolete)
    return payload, False


def run(component: str) -> int:
    topology_path = Path(os.environ.get("VM_TOPOLOGY", ROOT / "provisioning/config/topology.yml")).resolve()
    release_path = Path(os.environ.get("VM_RELEASE", ROOT / "provisioning/config/release.json")).resolve()
    topology = yaml.safe_load(topology_path.read_text())
    environment = os.environ.get("VM_ENV_ID", topology["environmentId"])
    if not re.fullmatch(r"[a-z][a-z0-9-]{2,31}", environment):
        raise ValueError("invalid VM_ENV_ID")
    state = ROOT / "provisioning/.state" / environment
    if not (state / "runtime-secrets.yml").exists() or not (state / "vagrant").is_dir():
        raise ValueError("Existing VM state is missing; run make vm-up first.")
    env = {**os.environ, "VM_ENV_ID": environment, "VM_TOPOLOGY": str(topology_path),
           "VM_RELEASE": str(release_path), "VAGRANT_DOTFILE_PATH": str(state / "vagrant"),
           "ANSIBLE_CONFIG": str(ROOT / "provisioning/ansible/ansible.cfg"),
           "ANSIBLE_CALLBACK_PLUGINS": str(ROOT / "provisioning/ansible/callback_plugins"),
           "ANSIBLE_CALLBACKS_ENABLED": "deploy_report"}
    with (state / "deploy.lock").open("w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError("Another vm-deploy is running for this environment.")
        return deploy_locked(component, state, env)


def deploy_locked(component: str, state: Path, env: dict[str, str]) -> int:
    started = time.monotonic()
    report_dir = state / "deploy" / datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
    report_dir.mkdir(parents=True)
    report = {"environment": state.name, "component": component, "outcome": "failed", "seconds": {}, "artifacts": {}}
    variables = {"deploy_schema_sha256": hashlib.sha256((ROOT / "backend/schema/schema.sql").read_bytes()).hexdigest()}
    variables_file = report_dir / "vars.json"
    inventory = state / "inventory.yml"

    def play(tags: str) -> None:
        variables_file.write_text(json.dumps(variables))
        subprocess.run(["ansible-playbook", "-i", str(inventory),
                        str(ROOT / "provisioning/ansible/playbooks/deploy.yml"),
                        "--tags", tags, "--extra-vars", "@" + str(variables_file)],
                       env={**env, "ISUREAL_DEPLOY_REPORT": str(report_dir / (tags.split(",")[0] + ".json"))}, check=True)

    try:
        subprocess.run([str(ROOT / "provisioning/scripts/inventory.sh")], env=env, check=True)
        play("precheck")  # All nodes must pass before any application files are changed.
        build_start = time.monotonic()
        try:
            for part in (["backend", "frontend"] if component == "app" else [component]):
                payload, cached = build(part, state / "deploy-cache", env)
                variables["deploy_" + part] = str(payload)
                if part == "frontend":
                    variables["deploy_frontend_files"] = list(files_digest(payload / "frontend"))
                report["artifacts"][part] = {"cached": cached, "sha256": files_digest(payload)}
        finally:
            report["seconds"]["build"] = round(time.monotonic() - build_start, 3)
        play("deploy,restart,verify")
        report["outcome"] = "passed"
        return 0
    finally:
        report["seconds"]["total"] = round(time.monotonic() - started, 3)
        for phase in ("precheck", "deploy"):
            path = report_dir / (phase + ".json")
            if path.exists():
                report[phase] = json.loads(path.read_text())
        (report_dir / "report.json").write_text(json.dumps(report, indent=2))
        print(f"vm-deploy: {report['outcome']}; report: {report_dir / 'report.json'}", flush=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--component", choices=("app", "backend", "frontend"), default="app")
    args = parser.parse_args()
    try:
        return run(args.component)
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        print(f"vm-deploy: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
