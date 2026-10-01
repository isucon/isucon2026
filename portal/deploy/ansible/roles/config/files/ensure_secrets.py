#!/usr/bin/env python3
"""Persist generated credentials, adopting an existing deployment's env file."""

import base64
import json
import os
from pathlib import Path
import secrets
import sys
from urllib.parse import unquote, urlsplit


def main():
    root = Path(sys.argv[1])
    dest = root / "secrets.json"
    if dest.exists():
        json.loads(dest.read_text())  # Fail closed on corruption.
        dest.chmod(0o600)
        print("PRESENT")
        return
    env = {}
    if (root / "env").exists():
        for line in (root / "env").read_text().splitlines():
            if line and not line.startswith("#") and "=" in line:
                key, value = line.split("=", 1)
                env[key] = value
    portal = {}
    for key in (
        "session_secret",
        "bypass_secret",
        "checker_secret",
        "bench_token",
        "ssh_key_api_secret",
        "admin_password",
    ):
        portal[key] = (
            env.get("ISUXPORTAL_" + key.upper())
            or base64.b64encode(secrets.token_bytes(32)).decode()
        )
    password = unquote(
        urlsplit(env.get("ISUXPORTAL_DATABASE_URL", "")).password or ""
    ) or secrets.token_hex(32)
    data = {"portal": portal, "postgres": {"password": password}}
    # Exclusive creation prevents concurrent executions from replacing credentials.
    fd = os.open(dest, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as out:
        json.dump(data, out)
        out.write("\n")
    print("CREATED")


if __name__ == "__main__":
    main()
