import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "register", Path(__file__).parents[1] / "files/register.py"
)
register = importlib.util.module_from_spec(spec)
spec.loader.exec_module(register)


class RegistrationTest(unittest.TestCase):
    def test_metadata_uses_imdsv2_and_reads_fresh_values(self):
        requests = []

        def request(req, timeout):
            requests.append(req)
            value = b"imds-token" if req.get_method() == "PUT" else b"203.0.113.2\n"
            return io.BytesIO(value)

        with patch.object(register, "urlopen", request):
            self.assertEqual(register.metadata()("public-ipv4"), "203.0.113.2")
        self.assertEqual(requests[0].get_method(), "PUT")
        self.assertEqual(
            requests[1].get_header("X-aws-ec2-metadata-token"), "imds-token"
        )

    def test_report_is_registration_only_and_carries_real_instance_metadata(self):
        metadata = {
            "instance-id": "i-0123456789abcdef0",
            "local-ipv4": "192.168.11.10",
            "public-ipv4": "203.0.113.5",
        }
        requests = []

        def request(req, timeout):
            requests.append(req)
            response = io.BytesIO(b"")
            response.status = 204
            return response

        with patch.object(register, "urlopen", request):
            register.register(
                {"host": "portal.example", "token": "signed", "number": 5},
                metadata.__getitem__,
            )
        self.assertEqual(
            requests[0].full_url, "https://portal.example/api/instance_registrations"
        )
        self.assertEqual(
            json.loads(requests[0].data),
            {
                "token": "signed",
                "number": 5,
                "instance_id": metadata["instance-id"],
                "private_ipv4_address": metadata["local-ipv4"],
                "public_ipv4_address": metadata["public-ipv4"],
            },
        )

    def test_failed_registration_is_retried_by_systemd(self):
        response = io.BytesIO(b"")
        response.status = 503
        with patch.object(register, "urlopen", return_value=response):
            with self.assertRaises(RuntimeError):
                register.register(
                    {"host": "portal.example", "token": "signed", "number": 1},
                    lambda _: "unused",
                )
