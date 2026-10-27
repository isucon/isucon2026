import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";

const config = new pulumi.Config();
const instances = config.requireObject<string[]>("applicationInstances");
const instanceRole = config.require("instanceRoleArn");
const archivePath = config.require("archivePath");
const digest = createHash("sha256")
	.update(readFileSync(archivePath))
	.digest("hex");
const bucket = new aws.s3.Bucket("astra-artifacts", {
	tags: { Project: "isucon2026", Team: "10", ManagedBy: "pulumi" },
});
new aws.s3.BucketPublicAccessBlock("astra-private", {
	bucket: bucket.id,
	blockPublicAcls: true,
	blockPublicPolicy: true,
	ignorePublicAcls: true,
	restrictPublicBuckets: true,
});
const policy = new aws.s3.BucketPolicy("astra-download", {
	bucket: bucket.id,
	policy: bucket.arn.apply((arn) =>
		JSON.stringify({
			Version: "2012-10-17",
			Statement: [
				{
					Effect: "Allow",
					Principal: { AWS: instanceRole },
					Action: "s3:GetObject",
					Resource: `${arn}/releases/*`,
				},
			],
		}),
	),
});
const archive = new aws.s3.BucketObjectv2("astra-release", {
	bucket: bucket.id,
	key: `releases/${digest}.tar.gz`,
	source: new pulumi.asset.FileAsset(archivePath),
});
const script = readFileSync(
	"../../../webapp-optmized-astra/deploy/application.sh",
	"utf8",
);
const document = new aws.ssm.Document("astra-application", {
	documentType: "Command",
	content: pulumi.all([bucket.id, archive.key]).apply(([bucketName, key]) =>
		JSON.stringify({
			schemaVersion: "2.2",
			description: "Deploy the Team 10 Rust application",
			mainSteps: [
				{
					action: "aws:runShellScript",
					name: "deploy",
					inputs: {
						timeoutSeconds: "600",
						runCommand: [
							"set -eu",
							"command -v aws >/dev/null || (curl -fsSL https://awscli.amazonaws.com/awscli-exe-linux-x86_64-2.31.21.zip -o /tmp/astra-aws.zip && python3 -m zipfile -e /tmp/astra-aws.zip /tmp/astra-aws && chmod -R u+x /tmp/astra-aws/aws && /tmp/astra-aws/aws/install)",
							`aws s3 cp s3://${bucketName}/${key} /tmp/astra-release.tar.gz --only-show-errors`,
							"cat >/tmp/astra-application.sh <<'ASTRA_SCRIPT'",
							script,
							"ASTRA_SCRIPT",
							"bash /tmp/astra-application.sh /tmp/astra-release.tar.gz",
						],
					},
				},
			],
		}),
	),
});
const association = new aws.ssm.Association(
	"astra-application",
	{
		name: document.name,
		documentVersion: document.latestVersion.apply(String),
		targets: [{ key: "InstanceIds", values: instances }],
		waitForSuccessTimeoutSeconds: 600,
	},
	{ dependsOn: [policy, archive] },
);
export const deployment = {
	bucket: bucket.id,
	digest,
	associationId: association.associationId,
};
