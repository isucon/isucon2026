from __future__ import annotations

import unittest

from provisioning.scripts import preflight
from provisioning.tests.conformance import validate
from support import PROVISIONING_DIR, load_yaml


class ConformanceContractTest(unittest.TestCase):
    def test_expected_hostnames_match_the_topology_contract(self) -> None:
        topology = load_yaml(PROVISIONING_DIR / "config" / "topology.yml")
        contract = preflight.portal_hostnames(topology)
        self.assertEqual(
            {
                "app": contract["app"],
                **contract["servers"],
            },
            validate.expected_portal_hostnames(topology),
        )

    def test_extracts_every_portal_reported_hostname(self) -> None:
        response = {
            "state": {
                "portal": {
                    "converged": True,
                    "applied": {
                        "calculated": {
                            "hostnames": {
                                "app": "app.team1-local.isureal.test",
                                "servers": {
                                    "server1": "server1.team1-local.isureal.test",
                                    "server2": "server2.team1-local.isureal.test",
                                    "server3": "server3.team1-local.isureal.test",
                                },
                                "target": "server1.team1-local.isureal.test",
                            }
                        }
                    },
                }
            }
        }

        self.assertEqual(
            {
                "app": "app.team1-local.isureal.test",
                "server1": "server1.team1-local.isureal.test",
                "server2": "server2.team1-local.isureal.test",
                "server3": "server3.team1-local.isureal.test",
            },
            validate.portal_reported_hostnames(response),
        )

    def test_rejects_incomplete_or_unconverged_portal_state(self) -> None:
        with self.assertRaisesRegex(ValueError, "converged"):
            validate.portal_reported_hostnames(
                {"state": {"portal": {"converged": False}}}
            )
        with self.assertRaisesRegex(ValueError, "server3"):
            validate.portal_reported_hostnames(
                {
                    "state": {
                        "portal": {
                            "converged": True,
                            "applied": {
                                "calculated": {
                                    "hostnames": {
                                        "app": "app.example.test",
                                        "servers": {
                                            "server1": "server1.example.test",
                                            "server2": "server2.example.test",
                                        },
                                    }
                                }
                            },
                        }
                    }
                }
            )


if __name__ == "__main__":
    unittest.main()
