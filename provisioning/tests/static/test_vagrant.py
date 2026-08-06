from __future__ import annotations

import re
import unittest

from support import PROVISIONING_DIR, load_yaml


class VagrantContractTest(unittest.TestCase):
    def setUp(self) -> None:
        self.topology = load_yaml(PROVISIONING_DIR / "config" / "topology.yml")
        self.vagrantfile = (PROVISIONING_DIR / "vagrant" / "Vagrantfile").read_text()

    def test_exactly_four_nodes_are_declared(self) -> None:
        declarations = re.findall(r'config\.vm\.define\s+"([^"]+)"', self.vagrantfile)
        self.assertEqual(["server1", "server2", "server3", "router"], declarations)

    def test_fixed_forward_contract_is_literal_and_not_auto_corrected(self) -> None:
        self.assertIn('host: 18080', self.vagrantfile)
        self.assertIn('guest: 8080', self.vagrantfile)
        self.assertIn('host_ip: "127.0.0.1"', self.vagrantfile)
        self.assertIn('auto_correct: false', self.vagrantfile)
        for node, port in self.topology["sshPorts"].items():
            self.assertRegex(self.vagrantfile, rf'host:\s*{port}', msg=node)

    def test_data_segments_and_control_adapter_are_explicit(self) -> None:
        for segment in ("server1-data", "server2-data", "server3-data"):
            self.assertIn(segment, self.vagrantfile)
        self.assertIn("192.168.60.69", self.vagrantfile)
        self.assertIn("192.168.60.65", self.vagrantfile)
        for node in self.topology["nodes"].values():
            for interface in node["interfaces"]:
                self.assertIn(interface["macAddress"], self.vagrantfile)

    def test_shared_folders_and_automatic_provision_are_disabled(self) -> None:
        self.assertIn('"/vagrant", disabled: true', self.vagrantfile)
        self.assertNotIn("docker", self.vagrantfile.lower())

    def test_role_builds_preserve_the_known_box_ssh_key(self) -> None:
        sources = (
            PROVISIONING_DIR / "packer" / "role-images" / "vagrant-sources.pkr.hcl"
        ).read_text()
        self.assertEqual(2, sources.count("insert_key   = false"))

    def test_role_builds_have_explicit_temporary_vm_resources(self) -> None:
        sources = (
            PROVISIONING_DIR / "packer" / "role-images" / "vagrant-sources.pkr.hcl"
        ).read_text()
        template = (
            PROVISIONING_DIR / "packer" / "role-images" / "Vagrantfile.tpl"
        ).read_text()
        self.assertEqual(
            2,
            sources.count('template     = "${path.root}/Vagrantfile.tpl"'),
        )
        self.assertIn("{{ .DefaultTemplate }}", template)
        self.assertIn("vb.cpus = 4", template)
        self.assertIn("vb.memory = 4096", template)


if __name__ == "__main__":
    unittest.main()
