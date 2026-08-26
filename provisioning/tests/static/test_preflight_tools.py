from __future__ import annotations

import unittest
from unittest import mock

from provisioning.scripts import preflight



class ToolPreflightTest(unittest.TestCase):
    @staticmethod
    def docker_output(command: list[str]) -> str:
        outputs = {
            ("docker", "compose", "version", "--short"): "5.5.0",
            (
                "docker",
                "version",
                "--format",
                "{{.Client.Version}} {{.Server.Version}}",
            ): "29.8.0 29.8.1",
        }
        return outputs[tuple(command)]

    @mock.patch("provisioning.scripts.preflight.shutil.which", return_value="/usr/bin/tool")
    @mock.patch("provisioning.scripts.preflight._run")
    def test_host_tool_versions_are_not_gated(
        self,
        run: mock.Mock,
        _which: mock.Mock,
    ) -> None:
        run.side_effect = self.docker_output

        self.assertEqual([], preflight.check_tools())
        self.assertEqual(
            {
                ("docker", "compose", "version", "--short"),
                (
                    "docker",
                    "version",
                    "--format",
                    "{{.Client.Version}} {{.Server.Version}}",
                ),
            },
            {tuple(call.args[0]) for call in run.call_args_list},
        )

    @mock.patch(
        "provisioning.scripts.preflight.shutil.which",
        side_effect=lambda command: None if command == "VBoxManage" else "/usr/bin/tool",
    )
    def test_missing_required_command_is_rejected(self, _which: mock.Mock) -> None:
        self.assertEqual(
            ["required command is missing: VBoxManage"],
            preflight.check_tools(),
        )


if __name__ == "__main__":
    unittest.main()
