from __future__ import annotations

import inspect
import subprocess
import unittest
from unittest import mock

from provisioning.scripts import preflight
from provisioning.tests.conformance import validate
from support import PROVISIONING_DIR, load_yaml


class ConformanceContractTest(unittest.TestCase):
    @mock.patch("provisioning.tests.conformance.validate.time.sleep")
    @mock.patch("provisioning.tests.conformance.validate.subprocess.run")
    def test_command_runner_retries_transient_failures(
        self,
        run: mock.Mock,
        sleep: mock.Mock,
    ) -> None:
        run.side_effect = [
            subprocess.CalledProcessError(1, ["vagrant", "status"]),
            subprocess.CompletedProcess(["vagrant", "status"], 0, stdout="ready\n"),
        ]

        self.assertEqual(
            "ready",
            validate.run(["vagrant", "status"], retries=1, retry_delay=0.1),
        )
        sleep.assert_called_once_with(0.1)

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
                                "app": "app.team1-local.127.0.0.1.nip.io",
                                "servers": {
                                    "app1": "app1.team1-local.127.0.0.1.nip.io",
                                    "app2": "app2.team1-local.127.0.0.1.nip.io",
                                    "app3": "app3.team1-local.127.0.0.1.nip.io",
                                },
                                "target": "app1.team1-local.127.0.0.1.nip.io",
                            }
                        }
                    },
                }
            }
        }

        self.assertEqual(
            {
                "app": "app.team1-local.127.0.0.1.nip.io",
                "app1": "app1.team1-local.127.0.0.1.nip.io",
                "app2": "app2.team1-local.127.0.0.1.nip.io",
                "app3": "app3.team1-local.127.0.0.1.nip.io",
            },
            validate.portal_reported_hostnames(response),
        )

    def test_rejects_incomplete_or_unconverged_portal_state(self) -> None:
        with self.assertRaisesRegex(ValueError, "converged"):
            validate.portal_reported_hostnames(
                {"state": {"portal": {"converged": False}}}
            )

    def test_report_uses_selected_release_and_atomic_replace(self) -> None:
        source = inspect.getsource(validate.main)
        self.assertIn('"releaseId": release["releaseId"]', source)
        self.assertIn("os.replace(temporary_report, report_path)", source)
        with self.assertRaisesRegex(ValueError, "app3"):
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
                                            "app1": "app1.example.test",
                                            "app2": "app2.example.test",
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
