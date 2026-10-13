import configparser
import pathlib
import unittest


class BootOrderingTest(unittest.TestCase):
    def test_checker_can_start_after_cloud_final(self):
        unit = configparser.ConfigParser(interpolation=None)
        unit.read(pathlib.Path(__file__).with_name("isucon-env-checker.service"))

        # Ubuntu 24.04 cloud-init 26.1: cloud-final.service and cloud-init.target
        # both run after multi-user.target. Targets also run after units they Want.
        ordering = {
            "cloud-final.service": {"multi-user.target"},
            "cloud-init.target": {"multi-user.target", "cloud-final.service"},
            "isucon-env-checker.service": set(unit["Unit"]["After"].split()),
        }
        for target in unit["Install"]["WantedBy"].split():
            ordering.setdefault(target, set()).add("isucon-env-checker.service")

        def visit(name, ancestors):
            self.assertNotIn(name, ancestors, f"ordering cycle: {ancestors} -> {name}")
            for dependency in ordering.get(name, set()):
                visit(dependency, ancestors + [name])

        for name in ordering:
            visit(name, [])
        self.assertIn("cloud-final.service", ordering["isucon-env-checker.service"])


if __name__ == "__main__":
    unittest.main()
