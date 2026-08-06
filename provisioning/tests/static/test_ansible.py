from __future__ import annotations

import unittest

from support import PROVISIONING_DIR


class AnsibleContractTest(unittest.TestCase):
    def read(self, relative: str) -> str:
        return (PROVISIONING_DIR / "ansible" / relative).read_text()

    def test_runtime_playbook_composes_network_and_native_roles(self) -> None:
        runtime = self.read("playbooks/runtime.yml")
        self.assertIn("runtime_network", runtime)
        self.assertIn("problem_server", runtime)
        self.assertIn("router", runtime)
        self.assertNotIn("community.docker", runtime)

    def test_problem_server_uses_postgresql_16_and_systemd(self) -> None:
        tasks = self.read("roles/problem_server/tasks/main.yml")
        self.assertIn("postgresql-16", tasks)
        self.assertIn("community.postgresql", tasks)
        self.assertIn("backend.tar", tasks)
        self.assertIn("frontend.tar", tasks)
        self.assertIn("database-migrations.tar", tasks)
        for unit in ("isureal-api.service", "isureal-worker.service", "nginx.service"):
            self.assertIn(unit, tasks)
        self.assertNotIn("mysql", tasks.lower())
        self.assertNotIn("docker", tasks.lower())

    def test_router_uses_native_router_and_envoy_units(self) -> None:
        tasks = self.read("roles/router/tasks/main.yml")
        self.assertIn("router.tar", tasks)
        self.assertIn("envoy.tar", tasks)
        self.assertIn("isureal-router.service", tasks)
        self.assertIn("isureal-envoy.service", tasks)
        self.assertNotIn("docker", tasks.lower())

    def test_network_role_keys_interfaces_by_mac(self) -> None:
        tasks = self.read("roles/runtime_network/tasks/main.yml")
        template = self.read("roles/runtime_network/templates/50-isureal.network.j2")
        live_apply = self.read("roles/runtime_network/templates/network-apply.sh.j2")
        firewall = self.read("roles/runtime_network/templates/firewall-apply.sh.j2")
        self.assertIn("macAddress", tasks)
        self.assertIn("MACAddress", template)
        self.assertIn("IPv4Forwarding", template)
        for rendered_template in (template, live_apply, firewall):
            self.assertNotIn("regex_replace", rendered_template)
            self.assertIn("[10:12]", rendered_template)

    def test_image_cleanup_removes_clone_identity_and_restores_ssh_bootability(self) -> None:
        tasks = self.read("roles/image_cleanup/tasks/main.yml")
        override = self.read(
            "roles/image_cleanup/templates/10-isureal-hostkeys.conf.j2"
        )
        self.assertIn("cloud-init clean --logs --machine-id --seed", tasks)
        self.assertIn("ssh_host_*", tasks)
        self.assertIn("isucon_ssh_hostkey_override.stat.size", tasks)
        self.assertIn("ssh-keygen -A", override)
        self.assertIn("ExecStartPre=", override)
        self.assertIn(
            "test -s /etc/systemd/system/ssh.service.d/10-isureal-hostkeys.conf",
            tasks,
        )
        self.assertIn("    sync\n", tasks)
        self.assertIn("/var/lib/systemd/random-seed", tasks)
        self.assertIn("ansible.builtin.raw", tasks)
        self.assertNotIn("ansible.builtin.shell", tasks)


if __name__ == "__main__":
    unittest.main()
