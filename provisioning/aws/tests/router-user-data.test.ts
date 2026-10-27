import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";
import { appHttpPort, team } from "../lib/topology";
import {
	routerEnvironment,
	routerNetplan,
	routerCloudInitConfig,
	routerDataReconfigurePath,
	routerDataReconfigureScript,
	routerNetworkScriptPath,
	routerUserData,
} from "../lib/user-data";

const userData = routerUserData(team(6), "06:ff:f7:f4:e5:2b", "cert", "key");

function writtenFile(path: string): { permissions: string; content: string } {
	const entry = userData.match(
		new RegExp(
			`  - path: ${path}\\n    encoding: b64\\n    permissions: "(\\d+)"\\n    owner: \\S+\\n    content: (\\S+)`,
		),
	);
	if (!entry) throw new Error(`${path} is not written`);
	return {
		permissions: entry[1],
		content: Buffer.from(entry[2], "base64").toString("utf8"),
	};
}

test("keeps cloud-init from regenerating the router network", () => {
	expect(
		writtenFile("/etc/cloud/cloud.cfg.d/99-isucon-router-network.cfg").content,
	).toBe(routerCloudInitConfig);
	expect(routerCloudInitConfig).toContain("network: {config: disabled}\n");
});

// hotplug が後から付いたデータ用 ENI の定義を 50-cloud-init.yaml に書き足すと data0 が failed になる
test("disables cloud-init hotplug for the data interface", () => {
	expect(routerCloudInitConfig).toContain(
		"updates: {network: {when: [boot-new-instance]}}\n",
	);
	expect(routerCloudInitConfig).not.toContain("hotplug");
});

// 再起動のたびに cloud-init が data0 に残す 1 時間のリースを、静的な設定に戻す
test("reconfigures data0 after cloud-init's DHCP on every boot", () => {
	expect(routerDataReconfigurePath).toMatch(
		/^\/var\/lib\/cloud\/scripts\/per-boot\//,
	);
	expect(writtenFile(routerDataReconfigurePath)).toEqual({
		permissions: "0755",
		content: routerDataReconfigureScript,
	});
	expect(routerDataReconfigureScript).toContain("networkctl reconfigure data0");
});

test("starts the router services only after data0 has its static routes", () => {
	expect(writtenFile(routerNetworkScriptPath)).toEqual({
		permissions: "0755",
		content: readFileSync(
			join(__dirname, "../../scripts/router_network.py"),
			"utf8",
		),
	});
	const runcmd = userData.slice(userData.indexOf("runcmd:\n"));
	expect(runcmd).toBe(`runcmd:
  - until ${routerNetworkScriptPath}; do sleep 5; done
  - systemctl enable --now isureal-portal isureal-router isureal-haproxy
`);
});

test("uses jumbo frames on the data interface like the servers", () => {
	expect(routerNetplan(team(6), "06:ff:f7:f4:e5:2b")).toMatch(
		/set-name: data0\n\s+dhcp4: false\n\s+mtu: 9001\n/,
	);
});

// HAProxy は証明書のチェーンと鍵を 1 つの PEM から読む
test("writes the certificate and key in one PEM for HAProxy", () => {
	expect(writtenFile("/etc/isucon/haproxy/tls/server.pem")).toEqual({
		permissions: "0640",
		content: "cert\nkey\n",
	});
	expect(userData).not.toContain("envoy");
});

test("points the Router and HAProxy at the team's servers", () => {
	const env = routerEnvironment(team(6));
	expect(env).toContain("ROUTER_HTTPS_LISTEN_ADDRESS=0.0.0.0:443\n");
	expect(env).toContain(
		"ROUTER_TLS_CERTIFICATE=/etc/isucon/haproxy/tls/server.pem\n",
	);
	expect(env).toContain(`ROUTER_UPSTREAM_PORT=${appHttpPort}\n`);
	expect(env).not.toMatch(/XDS|ENVOY/);
});
