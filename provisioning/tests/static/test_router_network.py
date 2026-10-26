import re
import unittest
from pathlib import Path

from provisioning.scripts.router_network import (
    CLOUD_INIT_CONFIG,
    DATA_RECONFIGURE_PATH,
    DATA_RECONFIGURE_SCRIPT,
    FOREVER,
    data_link_ready,
    remove_conflicting_interface,
)


class RouterNetworkTest(unittest.TestCase):
    def test_removes_duplicate_data_nic_without_changing_control_nic(self):
        original = {
            "network": {
                "version": 2,
                "ethernets": {
                    "ens5": {
                        "match": {"macaddress": "06:00:00:00:00:01"},
                        "dhcp4": True,
                    },
                    "ens6": {
                        "match": {"macaddress": "06:00:00:00:00:02"},
                        "dhcp4": True,
                    },
                },
            }
        }
        fixed = remove_conflicting_interface(original, "06:00:00:00:00:02")
        self.assertEqual(list(fixed["network"]["ethernets"]), ["ens5"])
        self.assertEqual(
            fixed["network"]["ethernets"]["ens5"],
            original["network"]["ethernets"]["ens5"],
        )
        self.assertIn("ens6", original["network"]["ethernets"])
        self.assertEqual(
            remove_conflicting_interface(fixed, "06:00:00:00:00:02"), fixed
        )

    def test_refuses_to_remove_the_only_interface(self):
        network = {
            "network": {
                "ethernets": {"ens5": {"match": {"macaddress": "06:00:00:00:00:02"}}}
            }
        }
        with self.assertRaises(ValueError):
            remove_conflicting_interface(network, "06:00:00:00:00:02")

    def test_rejects_invalid_mac(self):
        with self.assertRaises(ValueError):
            remove_conflicting_interface({"network": {"ethernets": {}}}, "invalid")


class DataLinkReadyTest(unittest.TestCase):
    ADDRESS = "192.168.170.10/24"
    DESTINATIONS = ["192.168.161.0/24", "192.168.162.0/24"]

    def ready(self, lifetime=FOREVER, destinations=DESTINATIONS, state="configured"):
        addresses = [
            {
                "ifname": "data0",
                "addr_info": [
                    {
                        "local": "192.168.170.10",
                        "prefixlen": 24,
                        "valid_life_time": lifetime,
                    }
                ],
            }
        ]
        routes = [{"dst": "192.168.170.0/24", "protocol": "kernel"}] + [
            {"dst": dst, "gateway": "192.168.170.1", "protocol": "static"}
            for dst in destinations
        ]
        return data_link_ready(
            addresses, routes, state, self.ADDRESS, self.DESTINATIONS
        )

    def test_accepts_static_address_with_all_routes(self):
        self.assertTrue(self.ready())

    def test_rejects_address_left_by_dhcp(self):
        self.assertFalse(self.ready(lifetime=3553))

    def test_rejects_missing_route(self):
        self.assertFalse(self.ready(destinations=self.DESTINATIONS[:1]))

    def test_rejects_link_still_configuring(self):
        self.assertFalse(self.ready(state="configuring"))


class RouterUserDataTest(unittest.TestCase):
    def test_writes_the_same_files_as_router_user_data(self):
        source = (
            Path(__file__).resolve().parents[2] / "aws" / "lib" / "user-data.ts"
        ).read_text()
        for name, expected in [
            ("routerCloudInitConfig", CLOUD_INIT_CONFIG),
            ("routerDataReconfigureScript", DATA_RECONFIGURE_SCRIPT),
        ]:
            match = re.search(
                rf"export const {name} = `(.*?)`;", source, re.DOTALL
            )
            self.assertIsNotNone(match, name)
            self.assertEqual(match.group(1), expected, name)
        self.assertIn(f'"{DATA_RECONFIGURE_PATH}"', source)
