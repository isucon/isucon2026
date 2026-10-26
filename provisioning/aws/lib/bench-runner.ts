import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";

export interface BenchRunnerConfig {
	region: string;
	// Supervisor と同じ private subnet に置き、共有 NAT の固定 IP から Router へ接続する
	vpcId: string;
	subnetId: string;
	// 結果を S3 に残す日数
	resultRetentionDays?: number;
	// ECR に残すイメージの数
	imageRetentionCount?: number;
	// チームのサーバーのインスタンスロール。走行中に SSM で集めた統計を、SSM エージェントが ssm/ の下へ書く
	instanceRoleArns?: string[];
}

// ベンチを Fargate で単発実行するための基盤。タスク定義と実行は ecspresso が管理する
export function createBenchRunner(c: BenchRunnerConfig) {
	if (!/^vpc-[a-zA-Z0-9-]+$/.test(c.vpcId)) {
		throw new Error("vpcId must be a VPC ID");
	}
	if (!/^subnet-[a-zA-Z0-9-]+$/.test(c.subnetId)) {
		throw new Error("subnetId must be a subnet ID");
	}
	for (const arn of c.instanceRoleArns ?? []) {
		if (!/^arn:aws:iam::[0-9]{12}:role\/[a-zA-Z0-9+=,.@_/-]+$/.test(arn)) {
			throw new Error("instanceRoleArns must be IAM role ARNs");
		}
	}
	const name = `isucon2026-bench-runner-${pulumi.getStack()}`;
	const tags = {
		Project: "isucon2026",
		Environment: pulumi.getStack(),
		ManagedBy: "pulumi",
	};

	const repository = new aws.ecr.Repository("bench-images", {
		name,
		imageTagMutability: "IMMUTABLE",
		imageScanningConfiguration: { scanOnPush: true },
		tags,
	});
	new aws.ecr.LifecyclePolicy("bench-images", {
		repository: repository.name,
		policy: JSON.stringify({
			rules: [
				{
					rulePriority: 1,
					description: "Keep recent benchmark images",
					selection: {
						tagStatus: "any",
						countType: "imageCountMoreThan",
						countNumber: c.imageRetentionCount ?? 30,
					},
					action: { type: "expire" },
				},
			],
		}),
	});

	const results = new aws.s3.Bucket("bench-results", {
		bucketPrefix: "isucon2026-bench-results-",
		forceDestroy: true,
		tags,
	});
	const publicAccessBlock = new aws.s3.BucketPublicAccessBlock(
		"bench-results",
		{
			bucket: results.id,
			blockPublicAcls: true,
			blockPublicPolicy: true,
			ignorePublicAcls: true,
			restrictPublicBuckets: true,
		},
	);
	new aws.s3.BucketLifecycleConfiguration("bench-results", {
		bucket: results.id,
		rules: ["runs", "ssm"].map((prefix) => ({
			id: `expire-${prefix}`,
			status: "Enabled",
			filter: { prefix: `${prefix}/` },
			expiration: { days: c.resultRetentionDays ?? 90 },
		})),
	});
	if (c.instanceRoleArns?.length) {
		// インスタンスロールはメインの stack が管理するので、ロールではなくバケットの側で許可する
		new aws.s3.BucketPolicy(
			"bench-results",
			{
				bucket: results.id,
				policy: results.arn.apply((arn) =>
					JSON.stringify({
						Version: "2012-10-17",
						Statement: [
							{
								Sid: "SsmCommandOutput",
								Effect: "Allow",
								Principal: { AWS: c.instanceRoleArns },
								Action: ["s3:PutObject", "s3:PutObjectAcl"],
								Resource: `${arn}/ssm/*`,
							},
							{
								Sid: "SsmCommandOutputBucket",
								Effect: "Allow",
								Principal: { AWS: c.instanceRoleArns },
								Action: [
									"s3:GetBucketLocation",
									"s3:GetEncryptionConfiguration",
								],
								Resource: arn,
							},
						],
					}),
				),
			},
			{ dependsOn: [publicAccessBlock] },
		);
	}

	const logs = new aws.cloudwatch.LogGroup("bench-logs", {
		name: `/isucon2026/bench-runner/${pulumi.getStack()}`,
		retentionInDays: 30,
		tags,
	});

	const execution = new aws.iam.Role("bench-execution", {
		assumeRolePolicy: aws.iam.assumeRolePolicyForPrincipal({
			Service: "ecs-tasks.amazonaws.com",
		}),
		tags,
	});
	new aws.iam.RolePolicyAttachment("bench-execution", {
		role: execution.name,
		policyArn: aws.iam.ManagedPolicy.AmazonECSTaskExecutionRolePolicy,
	});

	// タスクは結果を runs/ の下へ書くだけ
	const task = new aws.iam.Role("bench-task", {
		assumeRolePolicy: aws.iam.assumeRolePolicyForPrincipal({
			Service: "ecs-tasks.amazonaws.com",
		}),
		tags,
	});
	new aws.iam.RolePolicy("bench-results", {
		role: task.id,
		policy: results.arn.apply((arn) =>
			JSON.stringify({
				Version: "2012-10-17",
				Statement: [
					{
						Effect: "Allow",
						Action: ["s3:PutObject"],
						Resource: `${arn}/runs/*`,
					},
				],
			}),
		),
	});

	const sg = new aws.ec2.SecurityGroup("bench-runner", {
		vpcId: c.vpcId,
		description: "Benchmark runner tasks (egress only)",
		ingress: [],
		egress: [
			{
				protocol: "-1",
				fromPort: 0,
				toPort: 0,
				cidrBlocks: ["0.0.0.0/0"],
			},
		],
		tags,
	});

	const cluster = new aws.ecs.Cluster("bench-runner", { name, tags });

	// ecspresso の Jsonnet が読む値
	const infra = pulumi.output({
		region: c.region,
		name,
		cluster: cluster.name,
		repository: repository.repositoryUrl,
		repositoryName: repository.name,
		executionRoleArn: execution.arn,
		taskRoleArn: task.arn,
		logGroup: logs.name,
		bucket: results.bucket,
		subnetId: c.subnetId,
		securityGroupId: sg.id,
	});
	return { infra };
}
