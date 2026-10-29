import * as pulumi from "@pulumi/pulumi";
import { expect, test } from "vitest";
import { createPoolNetwork } from "../supervisor/pool/network";

const resources: pulumi.runtime.MockResourceArgs[] = [];
pulumi.runtime.setMocks({
	newResource: (args) => {
		resources.push(args);
		return { id: `${args.name}-id`, state: args.inputs };
	},
	call: (args) => args.inputs,
});

test("provides public task subnets across three AZs through the Internet Gateway", async () => {
	await pulumi.runtime.runInPulumiStack(async () =>
		createPoolNetwork({
			vpcId: "vpc-portal",
			internetGatewayId: "igw-portal",
			subnets: [
				{ availabilityZone: "ap-northeast-1a", cidrBlock: "10.26.32.0/22" },
				{ availabilityZone: "ap-northeast-1c", cidrBlock: "10.26.36.0/22" },
				{ availabilityZone: "ap-northeast-1d", cidrBlock: "10.26.40.0/22" },
			],
		}),
	);
	const subnets = resources.filter((r) => r.type === "aws:ec2/subnet:Subnet");
	expect(subnets).toHaveLength(3);
	expect(new Set(subnets.map((r) => r.inputs.availabilityZone)).size).toBe(3);
	for (const subnet of subnets) {
		expect(subnet.inputs.vpcId).toBe("vpc-portal");
		expect(subnet.inputs.mapPublicIpOnLaunch).toBe(false);
		expect(subnet.inputs.cidrBlock).toMatch(/\/22$/);
	}
	expect(
		resources.filter(
			(r) => r.type === "aws:ec2/routeTableAssociation:RouteTableAssociation",
		),
	).toHaveLength(3);
	const routes = resources.filter(
		(r) => r.type === "aws:ec2/routeTable:RouteTable",
	);
	expect(routes).toHaveLength(1);
	expect(routes[0].inputs.routes).toEqual([
		{ cidrBlock: "0.0.0.0/0", gatewayId: "igw-portal" },
	]);
	expect(resources.filter((r) => r.type.startsWith("aws:ecs/"))).toHaveLength(
		0,
	);
});
