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
        self.assertRegex(
            tasks,
            r"- name: Extract backend\.tar[\s\S]+?notify: Restart problem-server application",
        )
        self.assertNotIn("mysql", tasks.lower())
        self.assertNotIn("docker", tasks.lower())

    def test_problem_server_public_hostname_comes_from_portal_contract(self) -> None:
        for relative in (
            "roles/problem_server/tasks/main.yml",
            "roles/problem_server/templates/backend.env.j2",
            "roles/problem_server/templates/node-check.sh.j2",
        ):
            content = self.read(relative)
            self.assertIn("isucon_topology.portal.teamNumber", content)
            self.assertIn("isucon_topology.portal.teamHash", content)
            self.assertNotIn("app.{{ isucon_environment_id }}", content)

    def test_reset_restores_services_even_when_seed_fails(self) -> None:
        reset = self.read("playbooks/reset-data.yml")
        self.assertIn("      always:", reset)
        self.assertIn("Restore native application services after reset attempt", reset)

    def test_router_uses_native_router_and_envoy_units(self) -> None:
        tasks = self.read("roles/router/tasks/main.yml")
        self.assertIn("router.tar", tasks)
        self.assertIn("envoy.tar", tasks)
        self.assertIn("isureal-router.service", tasks)
        self.assertIn("isureal-envoy.service", tasks)
        self.assertRegex(
            tasks,
            r"- name: Extract router\.tar[\s\S]+?notify: Restart Router stack",
        )
        self.assertRegex(
            tasks,
            r"- name: Extract envoy\.tar[\s\S]+?notify: Restart Router stack",
        )
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
        self.assertIn('chain output', firewall)
        self.assertIn('ct state established,related accept', firewall)
        self.assertIn('tcp sport 22 accept', firewall)
        self.assertLess(
            firewall.index('tcp sport 22 accept'),
            firewall.index('ip daddr 10.0.0.0/8 drop'),
        )
        self.assertIn('ip daddr 10.0.2.3 udp dport 53 accept', firewall)
        self.assertIn('oifname "${bootstrap_iface}" ip daddr 10.0.0.0/8 drop', firewall)
        self.assertIn('oifname "${bootstrap_iface}" ip daddr 192.168.0.0/16 drop', firewall)
        self.assertIn('oifname "${bootstrap_iface}" meta nfproto ipv6 drop', firewall)
        self.assertIn('tcp dport 443 accept', firewall)

        router_tasks = self.read("roles/router/tasks/main.yml")
        envoy_bootstrap = self.read("roles/router/templates/envoy-bootstrap.yaml.j2")
        envoy_service = self.read("roles/router/templates/isureal-envoy.service.j2")
        self.assertIn("Install local TLS certificates", router_tasks)
        self.assertIn("port_value: 443", envoy_bootstrap)
        self.assertIn("envoy.transport_sockets.tls", envoy_bootstrap)
        self.assertIn("/etc/isucon/envoy/tls/server.crt", envoy_bootstrap)
        self.assertIn("CAP_NET_BIND_SERVICE", envoy_service)

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
