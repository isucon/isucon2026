import importlib.util
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

# Serialization is not the subject of these tests. JSON is also valid YAML;
# using it here lets bootstrap behavior run without host PyYAML installation.
spec = importlib.util.spec_from_file_location(
    "router_bootstrap", Path(__file__).parents[1] / "files/router-bootstrap.py"
)
bootstrap = importlib.util.module_from_spec(spec)
with patch.dict(
    "sys.modules",
    {
        "yaml": types.SimpleNamespace(safe_load=json.loads, safe_dump=json.dumps),
        "register": types.SimpleNamespace(metadata=lambda: None),
    },
):
    spec.loader.exec_module(bootstrap)


class RouterBootstrapTest(unittest.TestCase):
    def test_boot_and_reboot_preserve_public_eni_and_restore_data_routes(self):
        metadata = {
            "network/interfaces/macs/": "aa:aa:aa:aa:aa:aa/\nbb:bb:bb:bb:bb:bb/",
            "network/interfaces/macs/aa:aa:aa:aa:aa:aa/device-number": "0",
            "network/interfaces/macs/bb:bb:bb:bb:bb:bb/device-number": "1",
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = lambda name: root / name.lstrip("/")
            generated = path("/etc/netplan/50-cloud-init.yaml")
            generated.parent.mkdir(parents=True)
            path("/etc/cloud/cloud.cfg.d").mkdir(parents=True)
            public = {"match": {"macaddress": "aa:aa:aa:aa:aa:aa"}, "dhcp4": True}
            generated.write_text(
                json.dumps(
                    {
                        "network": {
                            "version": 2,
                            "ethernets": {
                                "ens5": public,
                                "ens6": {
                                    "match": {"macaddress": "bb:bb:bb:bb:bb:bb"},
                                    "dhcp4": True,
                                },
                            },
                        }
                    }
                )
            )
            ready = json.dumps(
                [{"dst": f"192.168.{n}.0/24"} for n in range(1, 5)]
            ).encode()
            with (
                patch.object(bootstrap, "Path", path),
                patch.object(bootstrap.subprocess, "run") as run,
                patch.object(
                    bootstrap.subprocess,
                    "check_output",
                    side_effect=[b"[]", ready, ready],
                ),
                patch.object(bootstrap.time, "sleep"),
            ):
                bootstrap.configure(metadata.__getitem__)
                bootstrap.configure(metadata.__getitem__)
            actual = json.loads(generated.read_text())["network"]["ethernets"]
            self.assertEqual(actual, {"ens5": public})
            data = json.loads(path("/etc/netplan/90-isucon.yaml").read_text())[
                "network"
            ]["ethernets"]["data0"]
            self.assertEqual(data["addresses"], ["192.168.10.10/24"])
            self.assertEqual(data["mtu"], 9001)
            self.assertFalse(data["dhcp4"])
            self.assertEqual(len(data["routes"]), 4)
            self.assertEqual(run.call_count, 4)
            self.assertEqual(
                path("/etc/netplan/90-isucon.yaml").stat().st_mode & 0o777, 0o600
            )

    def test_missing_data_eni_fails_for_systemd_retry(self):
        with self.assertRaises(StopIteration):
            bootstrap.configure(lambda _: "")
