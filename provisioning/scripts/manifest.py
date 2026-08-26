#!/usr/bin/env python3
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import sys
from typing import Any

from jsonschema import Draft202012Validator, FormatChecker


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_RELEASE = REPO_ROOT / "provisioning" / "config" / "release.json"
DEFAULT_INDEX = REPO_ROOT / "provisioning" / ".state" / "images" / "image-artifact-index.json"
SCHEMA = REPO_ROOT / "specs" / "004-local-vm-provisioning" / "contracts" / "image-artifact-index.schema.json"
ROLES = ("base", "problem-server", "router")


def now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def read_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text())


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_index(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {"schemaVersion": 2, "generatedAt": now(), "artifacts": []}
    return read_json(path)


def validate(index: dict[str, Any]) -> list[str]:
    schema = read_json(SCHEMA)
    validator = Draft202012Validator(schema, format_checker=FormatChecker())
    errors = [
        f"{'.'.join(str(part) for part in error.absolute_path) or '<root>'}: {error.message}"
        for error in sorted(validator.iter_errors(index), key=lambda item: list(item.absolute_path))
    ]
    keys: set[tuple[str, str, str, str, str]] = set()
    for artifact in index.get("artifacts", []):
        output = artifact.get("output", {})
        key = (
            artifact.get("releaseId", ""), artifact.get("role", ""),
            artifact.get("provider", ""), artifact.get("architecture", ""),
            output.get("region", "") if output.get("kind") == "ami" else "",
        )
        if key in keys:
            errors.append(f"duplicate artifact variant: {'/'.join(key)}")
        keys.add(key)
        state = artifact.get("validationState")
        reports = artifact.get("validationReports", [])
        if state in {"validated", "promoted"} and not reports:
            errors.append(f"{artifact.get('artifactId')}: {state} requires a validation report")
    return errors


def write_index(path: Path, index: dict[str, Any]) -> None:
    errors = validate(index)
    if errors:
        raise ValueError("invalid image artifact index:\n- " + "\n- ".join(errors))
    index["generatedAt"] = now()
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(index, indent=2, sort_keys=True) + "\n")
    os.replace(temporary, path)


def file_uri(path: Path) -> str:
    absolute = path.resolve()
    try:
        relative = absolute.relative_to(REPO_ROOT)
    except ValueError as exc:
        raise ValueError("box output must be inside the repository") from exc
    return "file://" + relative.as_posix()


def record_box(args: argparse.Namespace) -> None:
    release = read_json(args.release.resolve())
    box = args.box.resolve()
    if not box.is_file():
        raise ValueError(f"box does not exist: {box}")
    digest = sha256_file(box)
    release_id = release["releaseId"]
    artifact_id = f"{release_id}/{args.role}/virtualbox/amd64"
    artifact = {
        "artifactId": artifact_id,
        "releaseId": release_id,
        "role": args.role,
        "provider": "virtualbox",
        "architecture": "amd64",
        "sourceImageId": args.source_image_id,
        "buildName": args.build_name,
        "buildTime": now(),
        "sourceRevision": args.source_revision,
        "output": {"kind": "vagrant-box", "uri": file_uri(box), "sha256": digest},
        "validationState": "unverified",
        "validationReports": [],
    }
    index = load_index(args.index)
    matches = [item for item in index["artifacts"] if item["artifactId"] == artifact_id]
    if matches:
        old = matches[0]
        if old["output"]["sha256"] == digest:
            print(artifact_id)
            return
        if not getattr(args, "replace", False):
            raise ValueError(f"immutable artifact already exists with a different checksum: {artifact_id}")
        if old.get("validationState") == "promoted":
            raise ValueError(f"promoted artifact cannot be replaced: {artifact_id}")
        index["artifacts"] = [item for item in index["artifacts"] if item["artifactId"] != artifact_id]
    index["artifacts"].append(artifact)
    index["artifacts"].sort(key=lambda item: item["artifactId"])
    write_index(args.index, index)
    print(artifact_id)


def record_ami(args: argparse.Namespace) -> None:
    release = read_json(args.release.resolve())
    release_id = release["releaseId"]
    artifact_id = f"{release_id}/{args.role}/amazon-ebs/amd64/{args.region}"
    artifact = {
        "artifactId": artifact_id,
        "releaseId": release_id,
        "role": args.role,
        "provider": "amazon-ebs",
        "architecture": "amd64",
        "sourceImageId": args.source_image_id,
        "buildName": args.build_name,
        "buildTime": args.build_time or now(),
        "sourceRevision": args.source_revision,
        "output": {
            "kind": "ami",
            "region": args.region,
            "amiId": args.ami_id,
            "ownerId": args.owner_id,
        },
        "validationState": "unverified",
        "validationReports": [],
    }
    index = load_index(args.index)
    matches = [item for item in index["artifacts"] if item["artifactId"] == artifact_id]
    if matches:
        old = matches[0]
        if old["output"] == artifact["output"]:
            print(artifact_id)
            return
        if not getattr(args, "replace", False):
            raise ValueError(f"immutable artifact already exists with a different output: {artifact_id}")
        if old.get("validationState") == "promoted":
            raise ValueError(f"promoted artifact cannot be replaced: {artifact_id}")
        index["artifacts"] = [item for item in index["artifacts"] if item["artifactId"] != artifact_id]
    index["artifacts"].append(artifact)
    index["artifacts"].sort(key=lambda item: item["artifactId"])
    write_index(args.index, index)
    print(artifact_id)


def transition(args: argparse.Namespace) -> None:
    index = load_index(args.index)
    matches = [item for item in index["artifacts"] if item["artifactId"] == args.artifact_id]
    if len(matches) != 1:
        raise ValueError(f"artifact must exist exactly once: {args.artifact_id}")
    artifact = matches[0]
    old = artifact["validationState"]
    terminal = {"promoted", "rejected"}
    if old in terminal and old != args.state:
        raise ValueError(f"terminal artifact state cannot transition: {old} -> {args.state}")
    allowed = {
        "unverified": {"validated", "rejected"},
        "validated": {"validated", "promoted", "rejected"},
        "promoted": {"promoted"},
        "rejected": {"rejected"},
    }
    if args.state not in allowed[old]:
        raise ValueError(f"invalid artifact state transition: {old} -> {args.state}")
    if args.report not in artifact["validationReports"]:
        artifact["validationReports"].append(args.report)
    artifact["validationState"] = args.state
    write_index(args.index, index)
    print(args.artifact_id)


def validate_command(args: argparse.Namespace) -> None:
    errors = validate(load_index(args.index))
    if errors:
        raise ValueError("invalid image artifact index:\n- " + "\n- ".join(errors))
    print(f"image artifact index: PASS ({args.index})")


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description="Manage immutable local/AMI image artifact metadata")
    result.add_argument("--index", type=Path, default=DEFAULT_INDEX)
    subparsers = result.add_subparsers(dest="command", required=True)

    record = subparsers.add_parser("record-box")
    record.add_argument("--release", type=Path, default=DEFAULT_RELEASE)
    record.add_argument("--role", choices=ROLES, required=True)
    record.add_argument("--box", type=Path, required=True)
    record.add_argument("--source-image-id", required=True)
    record.add_argument("--build-name", required=True)
    record.add_argument("--source-revision", required=True)
    record.add_argument("--replace", action="store_true")
    record.set_defaults(function=record_box)

    record_cloud = subparsers.add_parser("record-ami")
    record_cloud.add_argument("--release", type=Path, default=DEFAULT_RELEASE)
    record_cloud.add_argument("--role", choices=("problem-server", "router"), required=True)
    record_cloud.add_argument("--region", required=True)
    record_cloud.add_argument("--ami-id", required=True)
    record_cloud.add_argument("--owner-id", required=True)
    record_cloud.add_argument("--source-image-id", required=True)
    record_cloud.add_argument("--build-name", required=True)
    record_cloud.add_argument("--build-time")
    record_cloud.add_argument("--source-revision", required=True)
    record_cloud.add_argument("--replace", action="store_true")
    record_cloud.set_defaults(function=record_ami)

    state = subparsers.add_parser("set-state")
    state.add_argument("--artifact-id", required=True)
    state.add_argument("--state", choices=("validated", "rejected", "promoted"), required=True)
    state.add_argument("--report", required=True)
    state.set_defaults(function=transition)

    check = subparsers.add_parser("validate")
    check.set_defaults(function=validate_command)
    return result


def main() -> int:
    args = parser().parse_args()
    try:
        args.function(args)
    except (OSError, KeyError, ValueError, json.JSONDecodeError) as exc:
        print(f"manifest: {exc}", file=sys.stderr)
        return 3
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
