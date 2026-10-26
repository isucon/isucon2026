#!/usr/bin/env python3
"""Keep the AWS Router's data0 on its static netplan configuration.

Runs at the Router's first boot from user data, and repairs existing Routers.
"""

import copy
import json
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path

# provisioning/aws/lib/user-data.ts の routerCloudInitConfig、routerDataReconfigurePath、
# routerDataReconfigureScript と同じ内容
CLOUD_INIT_CONFIG = (
    "network: {config: disabled}\n"
    "updates: {network: {when: [boot-new-instance]}}\n"
)
DATA_RECONFIGURE_PATH = "/var/lib/cloud/scripts/per-boot/isucon-router-data0"
DATA_RECONFIGURE_SCRIPT = (
    "#!/bin/sh\n"
    "if [ -e /sys/class/net/data0 ]; then networkctl reconfigure data0; fi\n"
)
# ip -j が寿命のないアドレスに返す値
FOREVER = 0xFFFFFFFF


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


def data_link_ready(addresses, routes, setup_state, address, destinations):
    local, prefix = address.split("/")
    static_address = any(
        info.get("local") == local
        and info.get("prefixlen") == int(prefix)
        and info.get("valid_life_time") == FOREVER
        for link in addresses
        for info in link.get("addr_info", [])
    )
    installed = {route.get("dst") for route in routes}
    return (
        setup_state == "configured"
        and static_address
        and set(destinations) <= installed
    )


def read_data_link():
    def run(*command):
        return subprocess.run(
            command, check=True, capture_output=True, text=True
        ).stdout

    addresses = json.loads(run("ip", "-j", "-4", "address", "show", "dev", "data0"))
    routes = json.loads(run("ip", "-j", "-4", "route", "show", "dev", "data0"))
    status = json.loads(run("networkctl", "--json=short", "status", "data0"))
    return addresses, routes, status.get("AdministrativeState")


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
        CLOUD_INIT_CONFIG
    )
    reconfigure = Path(DATA_RECONFIGURE_PATH)
    reconfigure.parent.mkdir(parents=True, exist_ok=True)
    reconfigure.write_text(DATA_RECONFIGURE_SCRIPT)
    reconfigure.chmod(0o755)
    subprocess.run(["netplan", "generate"], check=True)
    subprocess.run(["netplan", "apply"], check=True)
    if not Path("/sys/class/net/data0").exists():
        sys.exit("data0 is not attached yet")
    # cloud-init の DHCP が残した期限付きのアドレスからは、netplan apply だけでは経路が戻らないことがある
    subprocess.run(["networkctl", "reconfigure", "data0"], check=True)
    destinations = [route["to"] for route in data["routes"]]
    for _ in range(30):
        if data_link_ready(*read_data_link(), data["addresses"][0], destinations):
            break
        time.sleep(1)
    else:
        raise RuntimeError("data0 did not get its static address and routes")
    print(
        "Router data0 configuration restored; cloud-init hotplug and network "
        "regeneration disabled; data0 is reconfigured after every boot"
    )


if __name__ == "__main__":
    main()
