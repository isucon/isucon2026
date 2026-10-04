import unittest
from provisioning.scripts.router_network import remove_conflicting_interface


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
