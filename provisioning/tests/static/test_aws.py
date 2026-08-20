from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest

from provisioning.scripts import manifest
from support import PROVISIONING_DIR, load_json


class AWSProvisioningContractTest(unittest.TestCase):
    def test_release_pins_reviewed_canonical_ami_and_amazon_plugin(self) -> None:
        release = load_json(PROVISIONING_DIR / "config" / "release.json")
        image = release["ubuntu"]["cloudImages"]["amd64"]
        self.assertEqual("ap-northeast-1", image["region"])
        self.assertRegex(image["amiId"], r"^ami-[0-9a-f]+$")
        self.assertEqual("099720109477", image["ownerId"])
        self.assertEqual("1.8.1", release["toolchain"]["packerPlugins"]["amazon"])

    def test_packer_builds_two_amis_from_shared_ansible_playbook(self) -> None:
        root = PROVISIONING_DIR / "packer" / "aws-images"
        builds = (root / "builds.pkr.hcl").read_text()
        sources = (root / "sources.pkr.hcl").read_text()
        self.assertEqual(2, builds.count("../../ansible/playbooks/image.yml"))
        self.assertIn("image_role=problem-server", builds)
        self.assertIn("image_role=router", builds)
        self.assertIn('source "amazon-ebs" "problem_server"', sources)
        self.assertIn('source "amazon-ebs" "router"', sources)
        self.assertNotIn("private_key", sources.lower())
        self.assertNotIn("access_key", sources.lower())

    def test_router_bundle_contains_the_validation_portal(self) -> None:
        script = (PROVISIONING_DIR / "scripts" / "build-artifacts.sh").read_text()
        self.assertIn('go -C "${repo_root}/portal-mock/server" build', script)
        self.assertIn("bin/isureal-portal", script)

    def test_packer_wrapper_bridges_aws_cli_login_without_persisting_credentials(self) -> None:
        script = (PROVISIONING_DIR / "scripts" / "aws-image.sh").read_text()
        self.assertIn(
            "aws configure export-credentials --format process",
            script,
        )
        self.assertIn("credential_process =", script)
        self.assertNotIn("aws configure set aws_access_key_id", script)
        self.assertNotIn("credentials.pkrvars", script)

    def test_packer_wrapper_can_adopt_an_available_ami_after_late_failure(self) -> None:
        script = (PROVISIONING_DIR / "scripts" / "aws-image.sh").read_text()
        self.assertIn("adopt_existing_ami()", script)
        self.assertIn('"Name=state,Values=available"', script)
        build_role = script[script.index("build_role()"):]
        self.assertLess(
            build_role.index("if adopt_existing_ami"),
            build_role.index("configure_aws_sdk_credentials"),
        )

    def test_aws_validation_uses_valid_nft_option_order(self) -> None:
        script = (PROVISIONING_DIR / "scripts" / "aws-validate.sh").read_text()
        self.assertIn("nft -a list chain inet isureal_router forward", script)
        self.assertNotIn("nft list chain inet isureal_router forward -a", script)
        self.assertIn("nft_packet_count", script)
        self.assertIn("tc_packet_count", script)
        self.assertIn("cloneIdentityUniqueness", script)
        self.assertEqual(3, script.count("peer_http_all"))
        self.assertIn("--state validated", script)

    def test_pulumi_uses_three_vpcs_fixed_ips_and_cross_vpc_router_enis(self) -> None:
        source = (PROVISIONING_DIR / "aws" / "index.ts").read_text()
        topology = (PROVISIONING_DIR / "aws" / "lib" / "topology.ts").read_text()
        self.assertIn("serverNetworks.map", source)
        self.assertIn("NetworkInterfaceAttachment", source)
        self.assertIn("deviceIndex: index + 1", source)
        self.assertIn("sourceDestCheck: false", source)
        self.assertIn("networkInterfaceId: routerEni.id", source)
        self.assertIn("userDataBase64", source)
        self.assertIn("gzipSync", source)
        self.assertIn('"t3.small"', source)
        self.assertIn('"c8in.large"', source)
        self.assertIn("cidrBlocks: [network.vpcCidr]", source)
        for cidr in ("192.168.60.0/24", "192.168.61.0/24", "192.168.62.0/24"):
            self.assertIn(cidr, topology)
        self.assertEqual(1, source.count("new aws.ec2.Vpc("))

    def test_record_ami_is_immutable_and_schema_valid(self) -> None:
        release = load_json(PROVISIONING_DIR / "config" / "release.json")
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            release_path = root / "release.json"
            index_path = root / "index.json"
            release_path.write_text(json.dumps(release))

            class Args:
                index = index_path
                release = release_path
                role = "problem-server"
                region = "ap-northeast-1"
                ami_id = "ami-0123456789abcdef0"
                owner_id = "123456789012"
                source_image_id = "ami-0e239f7f4b3226722"
                build_name = "problem-server-amd64"
                build_time = "2026-08-20T00:00:00Z"
                replace = False

            manifest.record_ami(Args)
            manifest.record_ami(Args)
            index = load_json(index_path)
            self.assertEqual([], manifest.validate(index))
            self.assertEqual(1, len(index["artifacts"]))
            self.assertEqual("ami", index["artifacts"][0]["output"]["kind"])

            Args.ami_id = "ami-0fedcba9876543210"
            with self.assertRaisesRegex(ValueError, "immutable artifact"):
                manifest.record_ami(Args)

    def test_revalidation_can_append_a_report_without_downgrading(self) -> None:
        release = load_json(PROVISIONING_DIR / "config" / "release.json")
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            release_path = root / "release.json"
            index_path = root / "index.json"
            release_path.write_text(json.dumps(release))

            class RecordArgs:
                index = index_path
                release = release_path
                role = "router"
                region = "ap-northeast-1"
                ami_id = "ami-0123456789abcdef0"
                owner_id = "123456789012"
                source_image_id = "ami-0e239f7f4b3226722"
                build_name = "router-amd64"
                build_time = "2026-08-20T00:00:00Z"
                replace = False

            class TransitionArgs:
                index = index_path
                artifact_id = (
                    f"{release['releaseId']}/router/amazon-ebs/amd64/ap-northeast-1"
                )
                state = "validated"
                report = "aws-conformance:first"

            manifest.record_ami(RecordArgs)
            manifest.transition(TransitionArgs)
            TransitionArgs.report = "aws-conformance:second"
            manifest.transition(TransitionArgs)
            artifact = load_json(index_path)["artifacts"][0]
            self.assertEqual("validated", artifact["validationState"])
            self.assertEqual(
                ["aws-conformance:first", "aws-conformance:second"],
                artifact["validationReports"],
            )

    def test_ami_uniqueness_is_scoped_by_region(self) -> None:
        release = load_json(PROVISIONING_DIR / "config" / "release.json")
        base = {
            "artifactId": "r2026.08.06.3/router/amazon-ebs/amd64/ap-northeast-1",
            "releaseId": "r2026.08.06.3",
            "role": "router",
            "provider": "amazon-ebs",
            "architecture": "amd64",
            "sourceImageId": release["ubuntu"]["cloudImages"]["amd64"]["amiId"],
            "buildName": "router-amd64",
            "buildTime": "2026-08-20T00:00:00Z",
            "sourceRevision": release["sourceRevision"],
            "output": {
                "kind": "ami",
                "region": "ap-northeast-1",
                "amiId": "ami-0123456789abcdef0",
                "ownerId": "123456789012",
            },
            "validationState": "unverified",
            "validationReports": [],
        }
        other_region = json.loads(json.dumps(base))
        other_region["artifactId"] = (
            "r2026.08.06.3/router/amazon-ebs/amd64/us-east-1"
        )
        other_region["output"]["region"] = "us-east-1"
        other_region["output"]["amiId"] = "ami-0fedcba9876543210"
        index = {
            "schemaVersion": 2,
            "generatedAt": "2026-08-20T00:00:00Z",
            "artifacts": [base, other_region],
        }
        self.assertEqual([], manifest.validate(index))


if __name__ == "__main__":
    unittest.main()
