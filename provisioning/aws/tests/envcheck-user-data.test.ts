import { expect, test } from "vitest";
import { team } from "../lib/topology";
import {
	environmentCheckConfig,
	routerUserData,
	serviceUserData,
} from "../lib/user-data";

test("provisions declared AMIs and all five instance types for the checker", () => {
	const expected = environmentCheckConfig("ami-service", "ami-router", [
		"c5.large",
		"c7i.large",
		"c7i.large",
		"c5.large",
		"c6in.large",
	]);
	expect(JSON.parse(expected)).toEqual({
		service_ami: "ami-service",
		router_ami: "ami-router",
		types: ["c5.large", "c7i.large", "c7i.large", "c5.large", "c6in.large"],
	});
	const t = team(1);
	expect(serviceUserData(t, t.servers[0], [], [], false, expected)).toContain(
		Buffer.from(expected).toString("base64"),
	);
	const router = routerUserData(
		t,
		"00:11:22:33:44:55",
		"cert",
		"key",
		expected,
		["ssh-ed25519 fixture"],
	);
	expect(router).toContain("/etc/isucon/envcheck.json");
	expect(router).toContain("ec2-instance-connect");
	expect(router).toContain("isuadmin");
	expect(router).not.toContain("/home/isucon/.ssh");
});
