import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";

// 他の Pulumi stack の state を置く S3 バケットと、secret の暗号化に使う KMS キー
const bucket = new aws.s3.Bucket(
	"state",
	{ bucket: "isucon2026-pulumi-state" },
	{ protect: true },
);

const key = new aws.kms.Key("secrets", {}, { protect: true });

const alias = new aws.kms.Alias("secrets", {
	name: "alias/isucon2026-pulumi-secrets",
	targetKeyId: key.keyId,
});

export const backendUrl = pulumi.interpolate`s3://${bucket.bucket}`;
export const secretsProvider = pulumi.interpolate`awskms://${alias.name}`;
