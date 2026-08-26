#!/usr/bin/env python3
from __future__ import annotations

import argparse
from collections import Counter
import ipaddress
import json
import os
from pathlib import Path
import platform
import re
import shutil
import socket
import subprocess
import sys
from typing import Any, Iterable
from urllib.parse import unquote, urlparse

import yaml
from jsonschema import Draft202012Validator, FormatChecker


REPO_ROOT = Path(__file__).resolve().parents[2]
FEATURE_DIR = REPO_ROOT / "specs" / "004-local-vm-provisioning"
DEFAULT_TOPOLOGY = REPO_ROOT / "provisioning" / "config" / "topology.yml"
DEFAULT_RELEASE = REPO_ROOT / "provisioning" / "config" / "release.json"

def _read_document(path: Path) -> Any:
    text = path.read_text()
    if path.suffix == ".json":
        return json.loads(text)
    return yaml.safe_load(text)


def validate_instance(instance: Any, schema_path: Path) -> list[str]:
    schema = json.loads(schema_path.read_text())
    Draft202012Validator.check_schema(schema)
    validator = Draft202012Validator(schema, format_checker=FormatChecker())
    return [
        f"{'.'.join(str(part) for part in error.absolute_path) or '<root>'}: {error.message}"
        for error in sorted(validator.iter_errors(instance), key=lambda item: list(item.absolute_path))
    ]


def _all_interfaces(topology: dict[str, Any]) -> Iterable[tuple[str, dict[str, Any]]]:
    for node_id, node in topology.get("nodes", {}).items():
        for interface in node.get("interfaces", []):
            yield node_id, interface


def portal_hostnames(topology: dict[str, Any]) -> dict[str, Any]:
    portal = topology.get("portal", {})
    team_number = portal.get("teamNumber")
    team_hash = portal.get("teamHash")
    domain = topology.get("domain")
    if not isinstance(team_number, int) or team_number < 1:
        raise ValueError("Portal teamNumber must be a positive integer")
    if not isinstance(team_hash, str) or not team_hash:
        raise ValueError("Portal teamHash must be a non-empty string")
    if not isinstance(domain, str) or not domain:
        raise ValueError("topology domain must be a non-empty string")
    base = f"team{team_number}-{team_hash}.{domain}"
    return {
        "app": f"app.{base}",
        "servers": {
            server: f"{server}.{base}"
            for server in ("server1", "server2", "server3")
        },
    }


def validate_topology(topology: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    try:
        base = ipaddress.ip_network(topology["baseCidr"], strict=True)
    except (KeyError, ValueError) as exc:
        return [f"base CIDR is invalid: {exc}"]

    networks: dict[str, ipaddress.IPv4Network] = {}
    for name, definition in topology.get("networks", {}).items():
        try:
            network = ipaddress.ip_network(definition["cidr"], strict=True)
        except (KeyError, ValueError) as exc:
            errors.append(f"network {name} CIDR is invalid: {exc}")
            continue
        if network.version != 4 or network.prefixlen != 28:
            errors.append(f"network {name} must be an IPv4 /28")
        if not network.subnet_of(base):
            errors.append(f"network {name} is outside base CIDR")
        for other_name, other in networks.items():
            if network.overlaps(other):
                errors.append(f"network {name} overlaps {other_name}")
        networks[name] = network
        for address_key in ("routerAddress", "peerAddress", "hostAddress"):
            if address_key not in definition:
                continue
            try:
                address = ipaddress.ip_address(definition[address_key])
            except ValueError:
                errors.append(f"network {name} {address_key} is invalid")
                continue
            if address not in network or address in {network.network_address, network.broadcast_address}:
                errors.append(f"network {name} {address_key} is not a usable member")

    expected_segments = {
        "server1": [("bootstrap0", "nat", None), ("data0", "intnet", "server1Data")],
        "server2": [("bootstrap0", "nat", None), ("data0", "intnet", "server2Data")],
        "server3": [("bootstrap0", "nat", None), ("data0", "intnet", "server3Data")],
        "router": [
            ("bootstrap0", "nat", None),
            ("data1", "intnet", "server1Data"),
            ("data2", "intnet", "server2Data"),
            ("data3", "intnet", "server3Data"),
            ("control0", "hostonly", "control"),
        ],
    }
    if set(topology.get("nodes", {})) != set(expected_segments):
        errors.append("nodes must be exactly server1, server2, server3, and router")
    for node_id, expected in expected_segments.items():
        actual = topology.get("nodes", {}).get(node_id, {}).get("interfaces", [])
        normalized = [(item.get("role"), item.get("mode"), item.get("segment")) for item in actual]
        if normalized != expected:
            errors.append(f"node {node_id} interface bindings do not match the canonical topology")

    macs: dict[str, str] = {}
    for node_id, interface in _all_interfaces(topology):
        mac = interface.get("macAddress", "")
        if mac in macs:
            errors.append(f"MAC {mac} is duplicated by {macs[mac]} and {node_id}")
        macs[mac] = node_id

    ports: dict[int, str] = {}
    port_inputs = {
        "client": topology.get("clientForward", {}).get("hostPort"),
        "client-tls": topology.get("clientTlsForward", {}).get("hostPort"),
        "portal-api": topology.get("portal", {}).get("apiHostPort"),
        "portal-ui": topology.get("portal", {}).get("uiHostPort"),
        **{f"ssh-{name}": value for name, value in topology.get("sshPorts", {}).items()},
    }
    for name, port in port_inputs.items():
        if not isinstance(port, int):
            continue
        if port in ports:
            errors.append(f"port {port} is duplicated by {ports[port]} and {name}")
        ports[port] = name

    if topology.get("clientForward", {}).get("hostAddress") != "127.0.0.1":
        errors.append("Client forward must bind 127.0.0.1")
    if topology.get("clientForward", {}).get("autoCorrect") is not False:
        errors.append("Client forward auto-correction must be disabled")
    if topology.get("clientTlsForward", {}).get("hostAddress") != "127.0.0.1":
        errors.append("TLS Client forward must bind 127.0.0.1")
    if topology.get("clientTlsForward", {}).get("autoCorrect") is not False:
        errors.append("TLS Client forward auto-correction must be disabled")
    if topology.get("portal", {}).get("uiHostAddress") != "127.0.0.1":
        errors.append("Portal UI must bind 127.0.0.1")
    try:
        hostnames = portal_hostnames(topology)
        for hostname in (hostnames["app"], *hostnames["servers"].values()):
            if any(len(label) > 63 for label in hostname.split(".")):
                errors.append(f"Portal hostname {hostname} contains an overlong label")
    except ValueError as exc:
        errors.append(str(exc))
    profile = topology.get("defaultResourceProfile")
    if profile not in topology.get("resourceProfiles", {}):
        errors.append("default resource profile is not defined")
    return errors


def validate_release(release: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    install_images = release.get("ubuntu", {}).get("installImages", {})
    if set(install_images) != {"amd64"}:
        errors.append("Ubuntu install image must contain only amd64")
    artifacts = release.get("artifacts", [])
    required_components = {
        "backend",
        "frontend",
        "router",
        "envoy",
        "database-migrations",
    }
    component_counts = Counter(artifact.get("component") for artifact in artifacts)
    if set(component_counts) != required_components or any(
        component_counts[component] != 1 for component in required_components
    ):
        errors.append("release artifacts must contain each required component exactly once")
    for artifact in artifacts:
        component = artifact.get("component")
        architecture = artifact.get("architecture")
        if component in {"backend", "router", "envoy"} and architecture != "amd64":
            errors.append(f"artifact {component} must be amd64")
        if architecture not in {"amd64", "any"}:
            errors.append(f"artifact {component} has unsupported architecture")
        if "?" in artifact.get("uri", ""):
            errors.append(f"artifact {component} URI must not be temporary or signed")
        if urlparse(artifact.get("uri", "")).scheme in {"https", "s3"} and not artifact.get("sha256"):
            errors.append(f"remote artifact {component} must declare a SHA-256 checksum")
    database = release.get("databaseContract", {})
    if database.get("engine") != "postgresql" or database.get("majorVersion") != 16:
        errors.append("database contract must be PostgreSQL 16")
    database_compatibility = database.get("compatibilityId")
    for component in ("backend", "database-migrations"):
        matches = [artifact for artifact in artifacts if artifact.get("component") == component]
        if len(matches) == 1 and matches[0].get("compatibilityId") != database_compatibility:
            errors.append(f"artifact {component} is incompatible with the database contract")
    return errors


def check_supported_host(system: str, machine: str) -> list[str]:
    errors: list[str] = []
    if system != "Linux":
        errors.append(f"unsupported host OS {system}; Linux is required")
    if machine not in {"x86_64", "amd64"}:
        errors.append(f"unsupported host architecture {machine}; x86_64 is required")
    return errors


def check_provider(provider: str) -> list[str]:
    if provider not in {"auto", "virtualbox"}:
        return [f"unsupported VM_PROVIDER {provider}; use auto or virtualbox"]
    return []


def _run(command: list[str]) -> str:
    return subprocess.run(command, check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT).stdout.strip()


def check_tools() -> list[str]:
    errors: list[str] = []
    required = ["packer", "vagrant", "VBoxManage", "docker", "ansible-playbook", "openssl"]
    for command in required:
        if shutil.which(command) is None:
            errors.append(f"required command is missing: {command}")
    if errors:
        return errors
    try:
        docker_versions = _run(
            ["docker", "version", "--format", "{{.Client.Version}} {{.Server.Version}}"]
        ).split()
        if len(docker_versions) != 2:
            errors.append("Docker Engine did not report both client and server versions")
    except (subprocess.CalledProcessError, ValueError) as exc:
        errors.append(f"Docker daemon/version check failed: {exc}")
    try:
        _run(["docker", "compose", "version", "--short"])
    except subprocess.CalledProcessError as exc:
        errors.append(f"Docker Compose check failed: {exc.stdout.strip()}")
    return errors


def check_virtualization_and_capacity(topology: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    drivers = (Path("/dev/vboxdrvu"), Path("/dev/vboxdrv"))
    if not any(driver.exists() and os.access(driver, os.R_OK | os.W_OK) for driver in drivers):
        errors.append("VirtualBox kernel driver is unavailable to this user")
    try:
        flags = Path("/proc/cpuinfo").read_text()
        if re.search(r"\b(vmx|svm)\b", flags) is None:
            errors.append("hardware virtualization flags (vmx/svm) are unavailable")
    except OSError as exc:
        errors.append(f"CPU virtualization inspection failed: {exc}")
    profile_name = os.environ.get("VM_PROFILE", topology["defaultResourceProfile"])
    profiles = topology["resourceProfiles"]
    if profile_name not in profiles:
        errors.append(f"resource profile is undefined: {profile_name}")
        return errors
    profile = profiles[profile_name]
    required_cpus = profile["problemServer"]["cpus"] * 3 + profile["router"]["cpus"]
    available_cpus = os.cpu_count() or 0
    if available_cpus < required_cpus:
        errors.append(f"host has {available_cpus} CPUs; profile requires {required_cpus}")
    page_size = os.sysconf("SC_PAGE_SIZE")
    pages = os.sysconf("SC_PHYS_PAGES")
    available_memory_mib = page_size * pages // (1024 * 1024)
    required_memory_mib = profile["problemServer"]["memoryMiB"] * 3 + profile["router"]["memoryMiB"] + 2048
    if available_memory_mib < required_memory_mib:
        errors.append(
            f"host has {available_memory_mib} MiB RAM; VM profile plus Portal requires {required_memory_mib} MiB"
        )
    free_disk_gib = shutil.disk_usage(REPO_ROOT).free // (1024**3)
    if free_disk_gib < 20:
        errors.append(f"host has {free_disk_gib} GiB free; image and VM workflow requires at least 20 GiB")
    return errors


def check_compose_contract(topology: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    portal = topology["portal"]
    control_address = topology["networks"]["control"]["hostAddress"]
    environment = os.environ.copy()
    environment.update({
        "PORTAL_TEAM_NUMBER": str(portal["teamNumber"]),
        "PORTAL_TEAM_HASH": portal["teamHash"],
        "PORTAL_DOMAIN_SUFFIX": topology["domain"],
        "PORTAL_ROUTER_MODE": "external-router",
        "PORTAL_SERVER_BIND_IP": control_address,
        "PORTAL_SERVER_PORT": str(portal["apiHostPort"]),
        "PORTAL_WEB_BIND_IP": portal["uiHostAddress"],
        "PORTAL_WEB_PORT": str(portal["uiHostPort"]),
    })
    compose_file = REPO_ROOT / portal["composeFile"]
    try:
        result = subprocess.run(
            ["docker", "compose", "-f", str(compose_file), "config", "--format", "json"],
            check=True, text=True, env=environment,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        )
        rendered = json.loads(result.stdout)
    except (subprocess.CalledProcessError, json.JSONDecodeError) as exc:
        output = exc.stdout.strip() if isinstance(exc, subprocess.CalledProcessError) else str(exc)
        return [f"Portal Compose validation failed: {output}"]
    expected = {
        "server": {(control_address, str(portal["apiHostPort"]), portal["apiContainerPort"])},
        "web": {(portal["uiHostAddress"], str(portal["uiHostPort"]), portal["uiContainerPort"])},
    }
    for service, wanted in expected.items():
        actual = {
            (binding.get("host_ip"), str(binding.get("published")), binding.get("target"))
            for binding in rendered.get("services", {}).get(service, {}).get("ports", [])
        }
        if actual != wanted:
            errors.append(f"Portal {service} publishes {sorted(actual)!r}; expected {sorted(wanted)!r}")
        if any(binding[0] in {None, "", "0.0.0.0", "::"} for binding in actual):
            errors.append(f"Portal {service} has a wildcard publish")
    server_environment = rendered.get("services", {}).get("server", {}).get("environment", {})
    expected_environment = {
        "PORTAL_TEAM_NUMBER": str(portal["teamNumber"]),
        "PORTAL_TEAM_HASH": portal["teamHash"],
        "PORTAL_DOMAIN_SUFFIX": topology["domain"],
        "PORTAL_ROUTER_MODE": "external-router",
    }
    for key, wanted in expected_environment.items():
        if str(server_environment.get(key)) != wanted:
            errors.append(f"Portal {key} resolves to {server_environment.get(key)!r}; expected {wanted!r}")
    mode = server_environment.get("PORTAL_ROUTER_MODE")
    if mode != "external-router":
        errors.append("Portal must resolve in external-router mode")
    return errors


def _resolve_file_uri(uri: str) -> Path | None:
    parsed = urlparse(uri)
    if parsed.scheme != "file":
        return None
    # The release contract uses file://<repository-relative-path> for artifacts
    # that are produced in the working tree.  A non-local authority therefore
    # denotes the first relative path component, not a UNC host.
    if parsed.netloc and parsed.netloc != "localhost":
        raw = unquote(f"{parsed.netloc}{parsed.path}")
    else:
        raw = unquote(parsed.path)
    path = Path(raw)
    if not path.is_absolute():
        path = REPO_ROOT / raw
    return path


def check_artifact_files(release: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    for artifact in release.get("artifacts", []):
        path = _resolve_file_uri(artifact["uri"])
        if path is None:
            continue
        if not path.is_file():
            errors.append(f"artifact {artifact['component']} is missing: {path}")
    return errors


def check_ports(topology: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    ports = [
        topology["clientForward"]["hostPort"],
        topology["clientTlsForward"]["hostPort"],
        topology["portal"]["apiHostPort"],
        topology["portal"]["uiHostPort"],
        *topology["sshPorts"].values(),
    ]
    for port in ports:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        try:
            sock.bind(("0.0.0.0", port))
        except OSError as exc:
            errors.append(f"host port {port} is unavailable: {exc}")
        finally:
            sock.close()
    return errors


def check_host_route_conflict(topology: dict[str, Any]) -> list[str]:
    try:
        routes = _run(["ip", "-4", "route", "show"])
    except subprocess.CalledProcessError as exc:
        return [f"host route inspection failed: {exc.stdout.strip()}"]
    return host_route_conflicts(routes, topology)


def host_route_conflicts(routes: str, topology: dict[str, Any]) -> list[str]:
    base = ipaddress.ip_network(topology["baseCidr"])
    # Routes that fall inside this environment's own declared segments (for
    # example the control host-only network) are expected and often linger on
    # the host after `vm-destroy`.  They must not be treated as foreign
    # conflicts, otherwise a destroy/up cycle fails preflight on our own route.
    own_networks: list[ipaddress.IPv4Network] = []
    for definition in topology.get("networks", {}).values():
        if definition.get("kind") != "host-only":
            continue
        try:
            own_networks.append(ipaddress.ip_network(definition["cidr"]))
        except (KeyError, ValueError):
            continue
    errors: list[str] = []
    for line in routes.splitlines():
        token = line.split(maxsplit=1)[0]
        if token == "default":
            continue
        try:
            route = ipaddress.ip_network(token, strict=False)
        except ValueError:
            continue
        if not route.overlaps(base):
            continue
        if any(route.subnet_of(network) for network in own_networks):
            continue
        errors.append(f"host route {route} overlaps topology {base}")
    return errors


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Validate local VM provisioning inputs without creating resources")
    parser.add_argument("--topology", type=Path, default=Path(os.environ.get("VM_TOPOLOGY", DEFAULT_TOPOLOGY)))
    parser.add_argument("--release", type=Path, default=Path(os.environ.get("VM_RELEASE", DEFAULT_RELEASE)))
    parser.add_argument("--skip-artifacts", action="store_true")
    parser.add_argument("--skip-ports", action="store_true")
    parser.add_argument("--skip-routes", action="store_true")
    parser.add_argument("--skip-tools", action="store_true")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        topology = _read_document(args.topology.resolve())
        release = _read_document(args.release.resolve())
    except (OSError, ValueError, json.JSONDecodeError, yaml.YAMLError) as exc:
        print(f"preflight input failure: {exc}", file=sys.stderr)
        return 2
    errors = []
    errors += [f"topology schema: {item}" for item in validate_instance(topology, FEATURE_DIR / "contracts" / "topology.schema.json")]
    errors += [f"release schema: {item}" for item in validate_instance(release, FEATURE_DIR / "contracts" / "release-manifest.schema.json")]
    errors += validate_topology(topology)
    errors += validate_release(release)
    errors += check_supported_host(platform.system(), platform.machine())
    errors += check_provider(os.environ.get("VM_PROVIDER", "auto"))
    errors += check_virtualization_and_capacity(topology)
    if not args.skip_tools:
        errors += check_tools()
        errors += check_compose_contract(topology)
    if not args.skip_artifacts:
        errors += check_artifact_files(release)
    if not args.skip_ports:
        errors += check_ports(topology)
    if not args.skip_routes:
        errors += check_host_route_conflict(topology)

    output = {
        "schemaVersion": 1,
        "command": "vm-preflight",
        "environmentId": topology.get("environmentId", "unknown"),
        "releaseId": release.get("releaseId", "unknown"),
        "hostProfile": "linux-amd64-virtualbox",
        "outcome": "fail" if errors else "pass",
        "phase": "preflight",
        "errors": errors,
    }
    if os.environ.get("VM_OUTPUT") == "json":
        print(json.dumps(output, sort_keys=True))
    elif errors:
        print("local-vm preflight: FAIL", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
    else:
        profile = topology["resourceProfiles"][os.environ.get("VM_PROFILE", topology["defaultResourceProfile"])]
        total_memory = profile["problemServer"]["memoryMiB"] * 3 + profile["router"]["memoryMiB"]
        total_cpus = profile["problemServer"]["cpus"] * 3 + profile["router"]["cpus"]
        print(
            f"local-vm preflight: PASS\n"
            f"environment={topology['environmentId']} release={release['releaseId']} "
            f"profile=linux-amd64-virtualbox cpus={total_cpus} memoryMiB={total_memory}\n"
            f"client-http={topology['clientForward']['hostAddress']}:{topology['clientForward']['hostPort']} "
            f"client-https={topology['clientTlsForward']['hostAddress']}:{topology['clientTlsForward']['hostPort']} "
            f"portal-ui={topology['portal']['uiHostAddress']}:{topology['portal']['uiHostPort']} "
            f"portal-api={topology['networks']['control']['hostAddress']}:{topology['portal']['apiHostPort']}"
        )
    return 2 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
