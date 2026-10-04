#!/usr/bin/env python3
"""Repair cloud-init's duplicate ENI definition on an existing AWS Router."""

import copy
import re
import shutil
import subprocess
from pathlib import Path


def remove_conflicting_interface(document, data_mac):
    if not re.fullmatch(r"(?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}", data_mac):
        raise ValueError("invalid data interface MAC")
    fixed = copy.deepcopy(document)
    interfaces = fixed["network"]["ethernets"]
    for name, interface in list(interfaces.items()):
        if interface.get("match", {}).get("macaddress", "").lower() == data_mac.lower():
            del interfaces[name]
    if not interfaces:
        raise ValueError("refusing to remove all cloud-init interfaces")
    return fixed


def main():
    import yaml

    custom = Path("/etc/netplan/90-isucon.yaml")
    generated = Path("/etc/netplan/50-cloud-init.yaml")
    own = yaml.safe_load(custom.read_text())
    data = own["network"]["ethernets"]["data0"]
    if not data.get("routes") or not data.get("addresses"):
        raise ValueError("Router data0 configuration requires addresses and routes")
    original = yaml.safe_load(generated.read_text())
    fixed = remove_conflicting_interface(original, data["match"]["macaddress"])
    if fixed != original:
        backup = Path("/etc/netplan/50-cloud-init.yaml.before-router-repair")
        if not backup.exists():
            shutil.copy2(generated, backup)
        temporary = generated.with_suffix(".yaml.tmp")
        temporary.write_text(yaml.safe_dump(fixed, sort_keys=False))
        temporary.chmod(0o600)
        temporary.replace(generated)
    Path("/etc/cloud/cloud.cfg.d/99-isucon-router-network.cfg").write_text(
        "network: {config: disabled}\n"
    )
    subprocess.run(["netplan", "generate"], check=True)
    subprocess.run(["netplan", "apply"], check=True)
    print(
        "Router data0 configuration restored; cloud-init network regeneration disabled"
    )


if __name__ == "__main__":
    main()
