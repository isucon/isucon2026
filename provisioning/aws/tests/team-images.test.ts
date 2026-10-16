import * as pulumi from "@pulumi/pulumi";
import { expect, test } from "vitest";

process.env.PULUMI_CONFIG = JSON.stringify({
	"isucon2026:teamCount": "7",
	"isucon2026:independentTlsTeams": "[7]",
	"isucon2026:portalTeamIds": JSON.stringify({ 7: 700 }),
	"isucon2026:teamAdminSshPublicKeys": JSON.stringify({
		7: ["ssh-ed25519 BBBB adminfixture"],
	}),
	"isucon2026:teamSshPublicKeys": JSON.stringify({
		7: ["ssh-ed25519 AAAA fixture"],
	}),
	"isucon2026:disabledInstanceTeams": "[1,2,3]",
	"isucon2026:serviceAmiId": "ami-service",
	"isucon2026:routerAmiId": "ami-router",
	"isucon2026:teamImages": JSON.stringify({
		7: { serviceAmiId: "ami-new-service", routerAmiId: "ami-new-router" },
	}),
});
const resources: pulumi.runtime.MockResourceArgs[] = [];
pulumi.runtime.setMocks(
	{
		newResource: (args) => {
			resources.push(args);
			return {
				id: `${args.name}-id`,
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

test("uses new images only for the selected team and preserves existing instances", async () => {
	await pulumi.runtime.runInPulumiStack(async () => {
		const infra = await import("../index");
		return { teams: infra.teamOutputs };
	});
	const instances = resources.filter(
		(r) => r.type === "aws:ec2/instance:Instance",
	);
	expect(instances).toHaveLength(20);
	const certificates = resources.filter(
		(r) => r.type === "tls:index/selfSignedCert:SelfSignedCert",
	);
	const shared = certificates.find((r) => r.name === "router-tls-certificate");
	expect(shared?.inputs.dnsNames).toHaveLength(12);
	expect(shared?.inputs.dnsNames).not.toContain("team7-aws.127.0.0.1.nip.io");
	expect(
		certificates.find((r) => r.name === "team7-router-tls-certificate")?.inputs
			.dnsNames,
	).toEqual(["team7-aws.127.0.0.1.nip.io", "*.team7-aws.127.0.0.1.nip.io"]);
	for (const instance of instances) {
		const role = instance.name.endsWith("-router") ? "router" : "service";
		const expected = instance.name.startsWith("team7-")
			? `ami-new-${role}`
			: `ami-${role}`;
		expect(instance.inputs.ami, instance.name).toBe(expected);
		if (role === "service") {
			const encoded = Buffer.from("ssh-ed25519 AAAA fixture\n").toString(
				"base64",
			);
			if (instance.name.startsWith("team7-")) {
				expect(instance.inputs.userData).toContain(encoded);
				expect(instance.inputs.tags.IsuconTeamID).toBe("700");
				expect(instance.inputs.tags.Role).toBe("contestant");
				expect(instance.inputs.tags.IsuconInstanceNum).toBe(
					instance.name.includes("db1")
						? "4"
						: instance.name.match(/app([123])/)?.[1],
				);
				expect(instance.inputs.userData).toContain(
					"useradd --create-home --shell /bin/bash isuadmin",
				);
				expect(instance.inputs.userData).toContain(
					Buffer.from("ssh-ed25519 BBBB adminfixture\n").toString("base64"),
				);
			} else expect(instance.inputs.userData).not.toContain("authorized_keys");
		}
	}
});
