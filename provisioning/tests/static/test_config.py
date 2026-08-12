from __future__ import annotations

import copy
import tomllib
import unittest

from provisioning.scripts import preflight

from support import FEATURE_DIR, PROVISIONING_DIR, load_json, load_yaml


class ConfigContractTest(unittest.TestCase):
    def setUp(self) -> None:
        self.topology_path = PROVISIONING_DIR / "config" / "topology.yml"
        self.release_path = PROVISIONING_DIR / "config" / "release.json"
        self.topology = load_yaml(self.topology_path)
        self.release = load_json(self.release_path)

    def test_documents_match_json_schemas(self) -> None:
        topology_errors = preflight.validate_instance(
            self.topology,
            FEATURE_DIR / "contracts" / "topology.schema.json",
        )
        release_errors = preflight.validate_instance(
            self.release,
            FEATURE_DIR / "contracts" / "release-manifest.schema.json",
        )
        self.assertEqual([], topology_errors)
        self.assertEqual([], release_errors)

    def test_default_topology_has_exact_nodes_segments_and_forwards(self) -> None:
        self.assertEqual([], preflight.validate_topology(self.topology))
        self.assertEqual(
            {"server1", "server2", "server3", "router"},
            set(self.topology["nodes"]),
        )
        self.assertEqual(2, len(self.topology["nodes"]["server1"]["interfaces"]))
        self.assertEqual(2, len(self.topology["nodes"]["server2"]["interfaces"]))
        self.assertEqual(2, len(self.topology["nodes"]["server3"]["interfaces"]))
        self.assertEqual(5, len(self.topology["nodes"]["router"]["interfaces"]))
        self.assertEqual("127.0.0.1", self.topology["clientForward"]["hostAddress"])
        self.assertEqual(18080, self.topology["clientForward"]["hostPort"])
        self.assertFalse(self.topology["clientForward"]["autoCorrect"])

    def test_duplicate_ports_are_rejected(self) -> None:
        invalid = copy.deepcopy(self.topology)
        invalid["portal"]["uiHostPort"] = invalid["clientForward"]["hostPort"]
        self.assertTrue(any("port" in error.lower() for error in preflight.validate_topology(invalid)))

    def test_overlapping_segments_are_rejected(self) -> None:
        invalid = copy.deepcopy(self.topology)
        invalid["networks"]["server2Data"]["cidr"] = invalid["networks"]["server1Data"]["cidr"]
        self.assertTrue(any("overlap" in error.lower() for error in preflight.validate_topology(invalid)))

    def test_duplicate_macs_are_rejected(self) -> None:
        invalid = copy.deepcopy(self.topology)
        invalid["nodes"]["server2"]["interfaces"][0]["macAddress"] = invalid["nodes"]["server1"]["interfaces"][0]["macAddress"]
        self.assertTrue(any("mac" in error.lower() for error in preflight.validate_topology(invalid)))

    def test_release_is_linux_amd64_and_records_tool_provenance(self) -> None:
        self.assertEqual([], preflight.validate_release(self.release))
        self.assertEqual("29.7.1", self.release["toolchain"]["dockerEngine"])
        self.assertEqual("5.4.0", self.release["toolchain"]["dockerCompose"])
        self.assertEqual({"amd64"}, set(self.release["ubuntu"]["installImages"]))
        self.assertNotIn(
            "arm64",
            {artifact["architecture"] for artifact in self.release["artifacts"]},
        )

    def test_repository_tools_are_pinned_by_mise(self) -> None:
        mise = tomllib.loads((PROVISIONING_DIR.parent / "mise.toml").read_text())
        self.assertEqual("1.15.4", mise["tools"]["packer"])
        self.assertEqual("3.12.13", mise["tools"]["python"])
        self.assertEqual("2.21.2", mise["tools"]["ansible-core"])

    def test_autoinstall_uses_the_global_archive_without_geoip(self) -> None:
        user_data = load_yaml(PROVISIONING_DIR / "packer" / "local-base" / "http" / "user-data")
        apt = user_data["autoinstall"]["apt"]
        self.assertFalse(apt["geoip"])
        self.assertEqual("abort", apt["fallback"])
        self.assertEqual(
            [{"arches": ["amd64"], "uri": "http://archive.ubuntu.com/ubuntu"}],
            apt["mirror-selection"]["primary"],
        )

    def test_unsupported_hosts_are_rejected_without_fallback(self) -> None:
        self.assertEqual([], preflight.check_supported_host("Linux", "x86_64"))
        self.assertTrue(preflight.check_supported_host("Darwin", "x86_64"))
        self.assertTrue(preflight.check_supported_host("Linux", "aarch64"))
        self.assertEqual([], preflight.check_provider("auto"))
        self.assertEqual([], preflight.check_provider("virtualbox"))
        self.assertTrue(preflight.check_provider("libvirt"))

    def test_release_rejects_duplicate_or_incompatible_artifacts(self) -> None:
        duplicate = copy.deepcopy(self.release)
        duplicate["artifacts"].append(copy.deepcopy(duplicate["artifacts"][0]))
        duplicate["artifacts"][-1]["id"] = "backend.duplicate"
        self.assertTrue(
            any("exactly once" in error for error in preflight.validate_release(duplicate))
        )

        incompatible = copy.deepcopy(self.release)
        backend = next(
            artifact for artifact in incompatible["artifacts"]
            if artifact["component"] == "backend"
        )
        backend["compatibilityId"] = "wrong-database-contract"
        self.assertTrue(
            any("incompatible" in error for error in preflight.validate_release(incompatible))
        )

    def test_remote_artifact_requires_checksum_but_local_build_does_not(self) -> None:
        self.assertEqual([], preflight.validate_release(self.release))
        remote = copy.deepcopy(self.release)
        backend = next(
            artifact for artifact in remote["artifacts"]
            if artifact["component"] == "backend"
        )
        backend["uri"] = "https://artifacts.example.invalid/backend.tar"
        self.assertTrue(
            any(
                "remote artifact backend must declare a SHA-256 checksum" in error
                for error in preflight.validate_release(remote)
            )
        )

    def test_local_artifacts_are_deployed_without_checksum_markers(self) -> None:
        build_script = (PROVISIONING_DIR / "scripts" / "build-artifacts.sh").read_text()
        problem_role = (
            PROVISIONING_DIR / "ansible" / "roles" / "problem_server" / "tasks" / "main.yml"
        ).read_text()
        router_role = (
            PROVISIONING_DIR / "ansible" / "roles" / "router" / "tasks" / "main.yml"
        ).read_text()
        self.assertNotIn('artifact["sha256"]', build_script)
        self.assertNotIn("item.value.sha256", problem_role)
        self.assertNotIn("item.value.sha256", router_role)

    def test_portal_status_does_not_require_tagged_image_metadata(self) -> None:
        script = (PROVISIONING_DIR / "scripts" / "portal-compose.sh").read_text()
        self.assertIn("compose ps --all --no-trunc", script)
        self.assertNotIn("    compose images", script)

    def test_portal_noop_build_disables_nondeterministic_attestations(self) -> None:
        script = (PROVISIONING_DIR / "scripts" / "portal-compose.sh").read_text()
        self.assertIn("compose build --provenance=false --sbom=false", script)
        self.assertIn("compose up -d --wait", script)
        self.assertNotIn("compose up -d --build --wait", script)

    def test_runtime_inventory_uses_the_selected_release_artifact_directory(self) -> None:
        script = (PROVISIONING_DIR / "scripts" / "inventory.py").read_text()
        self.assertIn('/ release["releaseId"]', script)
        self.assertNotIn('/ "current" / "bundles"', script)

    def test_portal_and_router_share_topology_hostname_contract(self) -> None:
        portal = self.topology["portal"]
        self.assertEqual(1, portal["teamNumber"])
        self.assertEqual("local", portal["teamHash"])
        self.assertEqual(
            {
                "app": "app.team1-local.127.0.0.1.nip.io",
                "servers": {
                    "server1": "server1.team1-local.127.0.0.1.nip.io",
                    "server2": "server2.team1-local.127.0.0.1.nip.io",
                    "server3": "server3.team1-local.127.0.0.1.nip.io",
                },
            },
            preflight.portal_hostnames(self.topology),
        )

        portal_script = (PROVISIONING_DIR / "scripts" / "portal-compose.sh").read_text()
        self.assertIn('topology["portal"]["teamNumber"]', portal_script)
        self.assertIn('topology["portal"]["teamHash"]', portal_script)
        self.assertNotIn("printf 'PORTAL_TEAM_NUMBER=1", portal_script)
        self.assertNotIn("printf 'PORTAL_TEAM_HASH=local", portal_script)

        router_env = (
            PROVISIONING_DIR
            / "ansible"
            / "roles"
            / "router"
            / "templates"
            / "router.env.j2"
        ).read_text()
        self.assertIn("isucon_topology.portal.teamNumber", router_env)
        self.assertIn("isucon_topology.portal.teamHash", router_env)
        for prefix in ("APP", "SERVER1", "SERVER2", "SERVER3"):
            hostname_line = next(
                line for line in router_env.splitlines()
                if line.startswith(f"ROUTER_{prefix}_HOST=")
            )
            self.assertNotIn("isucon_environment_id", hostname_line)

    def test_invalid_portal_team_hash_is_rejected(self) -> None:
        invalid = copy.deepcopy(self.topology)
        invalid["portal"]["teamHash"] = "Not_A_Host_Label"
        errors = preflight.validate_instance(
            invalid,
            FEATURE_DIR / "contracts" / "topology.schema.json",
        )
        self.assertTrue(any("teamHash" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
