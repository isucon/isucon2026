#!/usr/bin/env python3
"""Register this instance on boot."""

import json
from pathlib import Path
from urllib.request import Request, urlopen


def metadata():
    base = "http://169.254.169.254/latest/"
    with urlopen(
        Request(
            base + "api/token",
            method="PUT",
            headers={"X-aws-ec2-metadata-token-ttl-seconds": "60"},
        ),
        timeout=5,
    ) as response:
        token = response.read().decode()

    def get(path):
        with urlopen(
            Request(
                base + "meta-data/" + path, headers={"X-aws-ec2-metadata-token": token}
            ),
            timeout=5,
        ) as response:
            return response.read().decode().strip()

    return get


def register(config, get):
    body = {
        "token": config["token"],
        "number": config["number"],
        "instance_id": get("instance-id"),
        "private_ipv4_address": get("local-ipv4"),
        "public_ipv4_address": get("public-ipv4"),
    }
    if config["number"] == 5:
        mac = next(
            mac
            for mac in get("network/interfaces/macs/").splitlines()
            if get("network/interfaces/macs/" + mac + "device-number") == "1"
        )
        body["private_ipv4_address"] = get(
            "network/interfaces/macs/" + mac + "local-ipv4s"
        ).splitlines()[0]
    request = Request(
        "https://" + config["host"] + "/api/instance_registrations",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urlopen(request, timeout=15) as response:
        if response.status != 204:
            raise RuntimeError("instance registration failed")


if __name__ == "__main__":
    config = json.loads(Path("/etc/isucon/registration.json").read_text())
    register(config, metadata())
