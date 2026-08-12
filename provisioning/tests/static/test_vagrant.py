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
        self.assertIn('client = topology.fetch("clientForward")', self.vagrantfile)
        self.assertIn('guest: client.fetch("guestPort")', self.vagrantfile)
        self.assertIn('host: client.fetch("hostPort")', self.vagrantfile)
        self.assertIn('host_ip: client.fetch("hostAddress")', self.vagrantfile)
        self.assertIn('auto_correct: client.fetch("autoCorrect")', self.vagrantfile)
        self.assertIn('topology.fetch("sshPorts").fetch("server1")', self.vagrantfile)
        self.assertIn('topology.fetch("sshPorts").fetch("server2")', self.vagrantfile)
        self.assertIn('topology.fetch("sshPorts").fetch("server3")', self.vagrantfile)
        self.assertIn('topology.fetch("sshPorts").fetch("router")', self.vagrantfile)

    def test_data_segments_and_control_adapter_are_explicit(self) -> None:
        for segment in ("server1-data", "server2-data", "server3-data"):
            self.assertIn(segment, self.vagrantfile)
        for network in ("server1Data", "server2Data", "server3Data", "control"):
            self.assertIn(f'network(topology, "{network}")', self.vagrantfile)
        self.assertIn('data.fetch("peerAddress")', self.vagrantfile)
        self.assertIn('data1.fetch("routerAddress")', self.vagrantfile)
        self.assertIn('control.fetch("routerAddress")', self.vagrantfile)
        self.assertIn('adapter_ip: control.fetch("hostAddress")', self.vagrantfile)
        self.assertIn('interface(topology, "server1", "bootstrap0")', self.vagrantfile)
        self.assertIn('interface(topology, "router", "control0")', self.vagrantfile)

    def test_guest_hostnames_come_from_the_topology_contract(self) -> None:
        for node in ("server1", "server2", "server3", "router"):
            self.assertIn(
                f'topology.fetch("nodes").fetch("{node}").fetch("hostname")',
                self.vagrantfile,
            )
        self.assertNotIn('vm.hostname = "server1-#{environment_id}"', self.vagrantfile)
        self.assertNotIn('vm.hostname = "router-#{environment_id}"', self.vagrantfile)

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

    def test_vm_lifecycle_cleans_only_unregistered_environment_folders(self) -> None:
        script = (PROVISIONING_DIR / "scripts" / "vm.sh").read_text()
        self.assertIn("remove_stale_vm_folders()", script)
        self.assertIn('name="${environment_id}-${node}"', script)
        self.assertIn('VBoxManage showvminfo "${name}"', script)
        self.assertIn('folder="${machine_folder%/}/${name}"', script)
        self.assertGreaterEqual(script.count("remove_stale_vm_folders"), 3)


if __name__ == "__main__":
    unittest.main()
