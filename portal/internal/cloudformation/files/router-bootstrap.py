#!/usr/bin/env python3
"""Configure the second ENI before starting the contest Router."""

import json
from pathlib import Path
import subprocess
import time
import yaml
from register import metadata


def configure(get):
    data_mac = next(
        mac.rstrip("/")
        for mac in get("network/interfaces/macs/").splitlines()
        if get("network/interfaces/macs/" + mac + "device-number") == "1"
    )
    data = {
        "match": {"macaddress": data_mac},
        "set-name": "data0",
        "dhcp4": False,
        "mtu": 9001,
        "addresses": ["192.168.10.10/24"],
        "routes": [
            {"to": f"192.168.{n}.0/24", "via": "192.168.10.1"} for n in range(1, 5)
        ],
    }
    generated = Path("/etc/netplan/50-cloud-init.yaml")
    network = yaml.safe_load(generated.read_text())
    interfaces = network["network"]["ethernets"]
    for name, value in list(interfaces.items()):
        if (
            name == "data0"
            or value.get("match", {}).get("macaddress", "").lower() == data_mac.lower()
        ):
            del interfaces[name]
    generated.write_text(yaml.safe_dump(network))
    generated.chmod(0o600)
    target = Path("/etc/netplan/90-isucon.yaml")
    target.write_text(
        yaml.safe_dump({"network": {"version": 2, "ethernets": {"data0": data}}})
    )
    target.chmod(0o600)
    Path("/etc/cloud/cloud.cfg.d/99-isucon-router-network.cfg").write_text(
        "network: {config: disabled}\nupdates: {network: {when: [boot-new-instance]}}\n"
    )
    subprocess.run(["netplan", "apply"], check=True)
    subprocess.run(["networkctl", "reconfigure", "data0"], check=True)
    for _ in range(30):
        routes = json.loads(
            subprocess.check_output(["ip", "-j", "-4", "route", "show", "dev", "data0"])
        )
        if {f"192.168.{n}.0/24" for n in range(1, 5)} <= {r.get("dst") for r in routes}:
            return
        time.sleep(1)
    raise RuntimeError("Router data routes are not ready")


if __name__ == "__main__":
    configure(metadata())
