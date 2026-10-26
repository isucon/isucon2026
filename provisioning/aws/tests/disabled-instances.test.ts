import * as pulumi from "@pulumi/pulumi";
import { expect, test } from "vitest";

process.env.PULUMI_CONFIG = JSON.stringify({
	"isucon2026:teamCount": "6",
	"isucon2026:disabledInstanceTeams": "[1,2,3]",
	"isucon2026:serviceAmiId": "ami-service",
	"isucon2026:routerAmiId": "ami-router",
});
const resources: pulumi.runtime.MockResourceArgs[] = [];
pulumi.runtime.setMocks(
	{
		newResource: (args) => {
			resources.push(args);
			return {
				id: args.name + "-id",
				state: {
					...args.inputs,
					publicIp: "203.0.113.1",
					macAddress: "02:00:00:00:00:01",
					latestVersion: 1,
					certPem: "cert",
					privateKeyPem: "key",
				},
			};
		},
		call: (args) => args.inputs,
	},
	"isucon2026",
	"dev",
);

test("disables only selected teams' instances while preserving their networks", async () => {
	await pulumi.runtime.runInPulumiStack(async () => {
		const infra = await import("../index");
		return { teams: infra.teamOutputs };
	});
	const instances = resources.filter((r) =>
		r.type === "aws:ec2/instance:Instance"
	);
	expect(instances).toHaveLength(15);
	expect(instances.every((r) => /^team[456]-/.test(r.name))).toBe(true);
	const attachments = resources.filter((r) =>
		r.type === "aws:ec2/networkInterfaceAttachment:NetworkInterfaceAttachment"
	);
	expect(attachments).toHaveLength(3);
	for (const team of [1, 2, 3, 4, 5, 6]) {
		expect(resources.some((r) => r.name === `team${team}-router-data-eni`))
			.toBe(true);
	}
});
