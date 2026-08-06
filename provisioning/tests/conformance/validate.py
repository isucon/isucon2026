#!/usr/bin/env python3
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import Any
from urllib.request import Request, urlopen

import yaml


REPO_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_TOPOLOGY = REPO_ROOT / "provisioning" / "config" / "topology.yml"
DEFAULT_STATE = REPO_ROOT / "provisioning" / ".state"
PORTAL_GET_STATE_PATH = "/portal_mock.v1.PortalMockService/GetState"
PORTAL_SERVER_IDS = ("server1", "server2", "server3")


def run(command: list[str], *, timeout: int = 60, cwd: Path | None = None) -> str:
    return subprocess.run(
        command, check=True, text=True, timeout=timeout,
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, cwd=cwd,
    ).stdout.strip()


def request(
    url: str,
    host: str | None = None,
    extra_headers: dict[str, str] | None = None,
) -> tuple[int, str]:
    headers = dict(extra_headers or {})
    if host:
        headers["Host"] = host
    with urlopen(Request(url, headers=headers), timeout=10) as response:
        return response.status, response.read(4096).decode("utf-8", errors="replace")


def connect_json(url: str) -> dict[str, Any]:
    request_body = Request(
        url,
        data=b"{}",
        headers={
            "Connect-Protocol-Version": "1",
            "Content-Type": "application/json",
        },
        method="POST",
    )
    with urlopen(request_body, timeout=10) as response:
        return json.loads(response.read(1 << 20))


def expected_portal_hostnames(topology: dict[str, Any]) -> dict[str, str]:
    portal = topology["portal"]
    base = f"team{portal['teamNumber']}-{portal['teamHash']}.{topology['domain']}"
    return {
        "app": f"app.{base}",
        **{server: f"{server}.{base}" for server in PORTAL_SERVER_IDS},
    }


def portal_reported_hostnames(response: dict[str, Any]) -> dict[str, str]:
    portal = response.get("state", {}).get("portal", {})
    if portal.get("converged") is not True:
        raise ValueError("Portal desired/applied state is not converged")
    hostnames = (
        portal.get("applied", {})
        .get("calculated", {})
        .get("hostnames", {})
    )
    app = hostnames.get("app")
    if not isinstance(app, str) or not app:
        raise ValueError("Portal reported app hostname is missing")
    servers = hostnames.get("servers", {})
    result = {"app": app}
    for server in PORTAL_SERVER_IDS:
        hostname = servers.get(server)
        if not isinstance(hostname, str) or not hostname:
            raise ValueError(f"Portal reported {server} hostname is missing")
        result[server] = hostname
    return result


def vagrant_ssh(vagrant_dir: Path, node: str, command: str, timeout: int = 60) -> str:
    return run(["vagrant", "ssh", node, "-c", command], timeout=timeout, cwd=vagrant_dir)


def colon_mac(value: str) -> str:
    return ":".join(value[index:index + 2] for index in range(0, 12, 2)).lower()


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate the running four-node local VM environment")
    parser.add_argument("--topology", type=Path, default=DEFAULT_TOPOLOGY)
    args = parser.parse_args()
    topology: dict[str, Any] = yaml.safe_load(args.topology.read_text())
    env_id = os.environ.get("VM_ENV_ID", topology["environmentId"])
    if not re.fullmatch(r"[a-z][a-z0-9-]{2,31}", env_id):
        raise SystemExit(f"invalid VM_ENV_ID: {env_id}")
    vagrant_dir = REPO_ROOT / "provisioning" / "vagrant"
    failures: list[str] = []
    checks: dict[str, Any] = {}

    try:
        status = run(["vagrant", "status", "--machine-readable"], timeout=30, cwd=vagrant_dir)
        running = {
            line.split(",")[1]
            for line in status.splitlines()
            if ",state,running" in line
        }
        expected = set(topology["nodes"])
        checks["runningNodes"] = sorted(running)
        if running != expected:
            failures.append(f"running VMs are {sorted(running)}; expected {sorted(expected)}")
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
        failures.append(f"Vagrant status failed: {exc}")

    for node in topology["nodes"]:
        try:
            health = vagrant_ssh(vagrant_dir, node, "sudo /usr/local/sbin/isureal-node-check")
            identity = vagrant_ssh(
                vagrant_dir, node,
                ". /etc/os-release; printf '%s|%s|' \"$VERSION_ID\" \"$(uname -m)\"; jq -r .releaseId /etc/isucon/release.json",
            ).splitlines()[-1]
            expected_identity = f"24.04|x86_64|{json.loads((REPO_ROOT / 'provisioning/config/release.json').read_text())['releaseId']}"
            if identity != expected_identity:
                failures.append(f"{node} identity is {identity!r}; expected {expected_identity!r}")
            nic_output = vagrant_ssh(
                vagrant_dir, node,
                "for path in /sys/class/net/*; do name=$(basename \"$path\"); "
                "test \"$name\" = lo || printf '%s %s\\n' \"$name\" \"$(cat \"$path/address\")\"; done",
            )
            actual_macs = {line.split()[-1].lower() for line in nic_output.splitlines() if len(line.split()) == 2}
            expected_macs = {colon_mac(item["macAddress"]) for item in topology["nodes"][node]["interfaces"]}
            if actual_macs != expected_macs:
                failures.append(f"{node} MAC set is {sorted(actual_macs)}; expected {sorted(expected_macs)}")
            checks.setdefault("nodes", {})[node] = {
                "health": health.splitlines()[-1], "identity": identity, "macs": sorted(actual_macs),
            }
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
            failures.append(f"{node} health failed: {exc}")

    server_segments = {
        "server1": "server1Data", "server2": "server2Data", "server3": "server3Data",
    }
    for source, source_segment in server_segments.items():
        router_address = topology["networks"][source_segment]["routerAddress"]
        for destination, destination_segment in server_segments.items():
            if source == destination:
                continue
            destination_address = topology["networks"][destination_segment]["peerAddress"]
            try:
                route = vagrant_ssh(vagrant_dir, source, f"ip -4 route get {destination_address}")
                checks.setdefault("routes", {})[f"{source}-to-{destination}"] = route.splitlines()[-1]
                if f"via {router_address}" not in route:
                    failures.append(f"{source} to {destination} does not use Router {router_address}")
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
                failures.append(f"{source} to {destination} route failed: {exc}")

    portal = topology["portal"]
    control = topology["networks"]["control"]["hostAddress"]
    portal_api = f"http://{control}:{portal['apiHostPort']}"
    reported_hostnames: dict[str, str] = {}
    try:
        portal_state = connect_json(portal_api + PORTAL_GET_STATE_PATH)
        reported_hostnames = portal_reported_hostnames(portal_state)
        expected_hostnames = expected_portal_hostnames(topology)
        checks["portalHostnames"] = {
            "expected": expected_hostnames,
            "reported": reported_hostnames,
            "match": reported_hostnames == expected_hostnames,
        }
        if reported_hostnames != expected_hostnames:
            failures.append(
                f"Portal reported hostnames {reported_hostnames!r}; expected topology contract {expected_hostnames!r}"
            )
    except Exception as exc:
        failures.append(f"Portal hostname discovery failed: {exc}")

    client = topology["clientForward"]
    client_url = f"http://{client['hostAddress']}:{client['hostPort']}/"
    client_checks: dict[str, Any] = {}
    for name, host in reported_hostnames.items():
        try:
            code, body = request(
                client_url,
                host,
                {"X-Country-Code": "JP"},
            )
            frontend_marker = "<!doctype html" in body.lower()
            client_checks[name] = {
                "status": code,
                "host": host,
                "frontendMarker": frontend_marker,
            }
            if code != 200 or not frontend_marker:
                failures.append(
                    f"Portal-reported hostname {name}={host} did not return frontend HTML through Client ingress"
                )
        except Exception as exc:  # network clients raise several concrete errors
            client_checks[name] = {"host": host, "error": str(exc)}
            failures.append(
                f"Portal-reported hostname {name}={host} failed through Client ingress: {exc}"
            )
    checks["clientHosts"] = client_checks
    if "app" in client_checks:
        checks["client"] = client_checks["app"]

    for name, url in {
        "portalApi": f"http://{control}:{portal['apiHostPort']}/healthz",
        "portalUi": f"http://{portal['uiHostAddress']}:{portal['uiHostPort']}/",
    }.items():
        try:
            code, _ = request(url)
            checks[name] = code
            if code != 200:
                failures.append(f"{name} returned HTTP {code}")
        except Exception as exc:
            failures.append(f"{name} failed: {exc}")

    report = {
        "schemaVersion": 1,
        "environmentId": env_id,
        "generatedAt": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "outcome": "fail" if failures else "pass",
        "checks": checks,
        "errors": failures,
    }
    report_dir = DEFAULT_STATE / env_id / "reports"
    report_dir.mkdir(parents=True, exist_ok=True)
    report_path = report_dir / "latest.json"
    report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    if failures:
        print("local-vm conformance: FAIL", file=sys.stderr)
        for failure in failures:
            print(f"- {failure}", file=sys.stderr)
        return 6
    print("local-vm conformance: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
