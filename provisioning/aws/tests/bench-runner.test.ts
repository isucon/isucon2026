import * as pulumi from "@pulumi/pulumi";
import { expect, test, vi } from "vitest";
import { createBenchRunner } from "../lib/bench-runner";

const resources: pulumi.runtime.MockResourceArgs[] = [];
pulumi.runtime.setMocks({
	newResource: (args) => {
		resources.push(args);
		return {
			id: `${args.name}-id`,
			state: {
				...args.inputs,
				name: args.inputs.name ?? args.name,
				arn: `arn:mock:${args.name}`,
				bucket: `${args.name}-bucket`,
				repositoryUrl: `123456789012.dkr.ecr.ap-northeast-1.amazonaws.com/${args.inputs.name}`,
			},
		};
	},
	call: (args) => args.inputs,
});

function outputOf<T>(output: pulumi.Output<T>) {
	return new Promise<T>((resolve) => output.apply(resolve));
}

test("runs in the shared private subnet with egress only and write-only results access", async () => {
	const infra = await pulumi.runtime.runInPulumiStack(async () => {
		const runner = createBenchRunner({
			region: "ap-northeast-1",
			vpcId: "vpc-portal",
			subnetId: "subnet-portal-private",
		});
		return outputOf(runner.infra);
	});

	const sg = resources.find(
		(r) => r.type === "aws:ec2/securityGroup:SecurityGroup",
	);
	expect(sg?.inputs.vpcId).toBe("vpc-portal");
	expect(sg?.inputs.ingress).toEqual([]);
	expect(infra).toMatchObject({
		region: "ap-northeast-1",
		subnetId: "subnet-portal-private",
		securityGroupId: "bench-runner-id",
	});

	// バケットの出力に依存するリソースは、スタックの関数が返ったあとに登録される
	const policy = await vi.waitFor(() => {
		const found = resources.find(
			(r) => r.type === "aws:iam/rolePolicy:RolePolicy",
		);
		expect(found).toBeDefined();
		expect(
			resources.some(
				(r) =>
					r.type === "aws:s3/bucketPublicAccessBlock:BucketPublicAccessBlock",
			),
		).toBe(true);
		return found;
	});
	expect(JSON.parse(policy?.inputs.policy).Statement).toEqual([
		{
			Effect: "Allow",
			Action: ["s3:PutObject"],
			Resource: "arn:mock:bench-results/runs/*",
		},
	]);
	expect(
		resources.find((r) => r.type === "aws:ecr/repository:Repository")?.inputs
			.imageTagMutability,
	).toBe("IMMUTABLE");
	expect(
		resources.find(
			(r) =>
				r.type === "aws:s3/bucketPublicAccessBlock:BucketPublicAccessBlock",
		)?.inputs,
	).toMatchObject({ blockPublicAcls: true, restrictPublicBuckets: true });
	// ECS のサービスとタスク定義は ecspresso が管理する
	expect(
		resources.filter(
			(r) =>
				r.type.startsWith("aws:ecs/") && r.type !== "aws:ecs/cluster:Cluster",
		),
	).toHaveLength(0);
});

test("rejects network IDs from other resource types", () => {
	expect(() =>
		createBenchRunner({
			region: "ap-northeast-1",
			vpcId: "subnet-portal-private",
			subnetId: "subnet-portal-private",
		}),
	).toThrow("vpcId");
});

test("lets only the team instance role write SSM command output", async () => {
	await pulumi.runtime.runInPulumiStack(async () => {
		createBenchRunner({
			region: "ap-northeast-1",
			vpcId: "vpc-portal",
			subnetId: "subnet-portal-private",
			instanceRoleArns: ["arn:aws:iam::123456789012:role/instance-role"],
		});
	});
	const policy = await vi.waitFor(() => {
		const found = resources.find(
			(r) => r.type === "aws:s3/bucketPolicy:BucketPolicy",
		);
		expect(found).toBeDefined();
		return found;
	});
	const statements = JSON.parse(policy?.inputs.policy).Statement;
	expect(statements[0]).toMatchObject({
		Principal: { AWS: ["arn:aws:iam::123456789012:role/instance-role"] },
		Action: ["s3:PutObject", "s3:PutObjectAcl"],
		Resource: "arn:mock:bench-results/ssm/*",
	});
	expect(() =>
		createBenchRunner({
			region: "ap-northeast-1",
			vpcId: "vpc-portal",
			subnetId: "subnet-portal-private",
			instanceRoleArns: ["*"],
		}),
	).toThrow("instanceRoleArns");
});
