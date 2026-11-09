import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
	appHttpPort,
	databaseIp,
	type ServerNetwork,
	type Team,
} from "./topology";

// アドレスはチームごとに違うので、AMI には焼き込まず起動時に設定する
export function serviceUserData(
	t: Team,
	network: ServerNetwork,
	sshPublicKeys: string[] = [],
	adminSshPublicKeys: string[] = [],
	guardEnabled = false,
	envcheck = "",
): string {
	const commands =
		network.region === undefined
			? ["systemctl enable --now postgresql"]
			: [
					`sed -i -e 's/^ISUREAL_REGION=.*/ISUREAL_REGION=${network.region}/' -e 's/^ISUREAL_DB_HOST=.*/ISUREAL_DB_HOST=${databaseIp(t)}/' /home/isucon/env.sh`,
					"systemctl enable --now isureal-go nginx",
				];
	if (network.region !== undefined && guardEnabled)
		commands.push("systemctl enable --now isureal-guard");
	if (envcheck)
		commands.push(
			`install -d -m 755 /etc/isucon`,
			`printf '%s' '${Buffer.from(envcheck).toString("base64")}' | base64 -d > /etc/isucon/envcheck.json`,
			"chmod 600 /etc/isucon/envcheck.json",
		);
	commands.unshift(...authorizedKeyCommands("isucon", sshPublicKeys));
	if (adminSshPublicKeys.length > 0) {
		commands.unshift(
			"id -u isuadmin >/dev/null 2>&1 || useradd --create-home --shell /bin/bash isuadmin",
			"printf '%s\\n' 'isuadmin ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/90-isuadmin",
			"chmod 440 /etc/sudoers.d/90-isuadmin",
			...authorizedKeyCommands("isuadmin", adminSshPublicKeys),
		);
	}
	return `#!/bin/sh
set -eu
${commands.join("\n")}
`;
}

function authorizedKeyCommands(
	user: "isucon" | "isuadmin",
	keys: string[],
): string[] {
	if (keys.length === 0) return [];
	const encoded = Buffer.from(`${keys.join("\n")}\n`).toString("base64");
	return [
		`install -d -m 700 -o ${user} -g ${user} /home/${user}/.ssh`,
		`printf '%s' '${encoded}' | base64 -d > /home/${user}/.ssh/authorized_keys`,
		`chown ${user}:${user} /home/${user}/.ssh/authorized_keys`,
		`chmod 600 /home/${user}/.ssh/authorized_keys`,
	];
}

function file(
	path: string,
	content: string,
	mode: string,
	owner = "root:root",
): string {
	return `  - path: ${path}
    encoding: b64
    permissions: "${mode}"
    owner: ${owner}
    content: ${Buffer.from(content, "utf8").toString("base64")}`;
}

function firstUsable(cidr: string): string {
	const octets = cidr.split("/")[0].split(".").map(Number);
	octets[3] += 1;
	return octets.join(".");
}

// パブリック用 ENI は cloud-init の既定の設定（DHCP）に任せ、サーバーのサブネット宛てだけを
// データ用 ENI から VPC へ送る。DHCP を使わないので MTU は既定の 1500 になる。サーバーは VPC の
// 9001 を使うので、data0 も 9001 にしてサーバー間の転送と HAProxy の上流をジャンボフレームで通す
// （1500 のままだと経路 MTU が 1500 に落ち、大きな転送ではルーターが処理するパケット数が約 6 倍になる）
export function routerNetplan(t: Team, dataMac: string): string {
	const routes = t.servers
		.map(
			(network) => `        - to: ${network.subnetCidr}
          via: ${firstUsable(t.routerDataSubnetCidr)}`,
		)
		.join("\n");
	return `network:
  version: 2
  ethernets:
    data0:
      match:
        macaddress: ${dataMac.toLowerCase()}
      set-name: data0
      dhcp4: false
      mtu: 9001
      addresses:
        - ${t.routerDataIp}/${t.routerDataSubnetCidr.split("/")[1]}
      routes:
${routes}
`;
}

const tlsCertificatePath = "/etc/isucon/haproxy/tls/server.pem";

// isureal-router と isureal-haproxy が同じファイルを読む
export function routerEnvironment(t: Team): string {
	const lines = [
		"ROUTER_PORTAL_ENDPOINT=http://127.0.0.1:18082",
		`ROUTER_TEAM_NUMBER=${t.number}`,
		`ROUTER_STATUS_LISTEN_ADDRESS=${t.routerPublicIp}:18081`,
		"ROUTER_HTTP_LISTEN_ADDRESS=0.0.0.0:8080",
		"ROUTER_HTTPS_LISTEN_ADDRESS=0.0.0.0:443",
		`ROUTER_TLS_CERTIFICATE=${tlsCertificatePath}`,
		`ROUTER_UPSTREAM_PORT=${appHttpPort}`,
	];
	for (const server of t.servers) {
		const prefix = `ROUTER_${server.id.toUpperCase()}`;
		lines.push(
			`${prefix}_SUBNET=${server.subnetCidr}`,
			`${prefix}_REGION_LOCAL_IP=${t.routerDataIp}`,
			`${prefix}_IP=${server.serviceIp}`,
		);
	}
	return `${lines.join("\n")}\n`;
}

export function routerControlEnvironment(t: Team): string {
	return `ROUTER_CONTROL_TEAM_NUMBER=${t.number}
ROUTER_CONTROL_LISTEN_ADDRESS=127.0.0.1:18082
ROUTER_CONTROL_DEFAULT_COUNTRY=JP
`;
}

// HAProxy は証明書のチェーンと鍵を 1 つの PEM から読む
function tlsPem(certificate: string, privateKey: string): string {
	return `${certificate.trimEnd()}\n${privateKey.trimEnd()}\n`;
}

// data0 は 90-isucon.yaml の静的な設定だけで動かす（provisioning/scripts/router_network.py も同じ内容を書く）。
// EC2 では cloud-init の hotplug が既定で有効で、AMI にも udev ルールが残っている。起動後に付いたデータ用 ENI を
// 見つけると DHCP とポリシールーティングの定義を 50-cloud-init.yaml に書き足し、data0 の定義が二重になって
// 経路を張れなくなる。network の config: disabled は hotplug には効かないので、hotplug も止める
export const routerCloudInitConfig = `network: {config: disabled}
updates: {network: {when: [boot-new-instance]}}
`;

// 再起動のたびに cloud-init は名前順で ens5 より先に来る data0 で DHCP を取り、1 時間で切れるアドレスを残す。
// 切れると data0 のアドレスと経路が消えるので、cloud-init の DHCP が済んだ後の final ステージで設定し直す。
// bootcmd の時点では D-Bus が動いておらず、networkctl を使えない
export const routerDataReconfigurePath =
	"/var/lib/cloud/scripts/per-boot/isucon-router-data0";
export const routerDataReconfigureScript = `#!/bin/sh
if [ -e /sys/class/net/data0 ]; then networkctl reconfigure data0; fi
`;

export const routerNetworkScriptPath = "/usr/local/sbin/isucon-router-network";

// データ用 ENI は起動後に attach されるので、data0 に静的なアドレスと経路が載るまで netplan を当て直してから起動する。
// ENI が init-local より先に付くと cloud-init が 50-cloud-init.yaml にも定義を書くので、スクリプトがそれを消す
export function routerUserData(
	t: Team,
	dataMac: string,
	tlsCertificate: string,
	tlsPrivateKey: string,
	envcheck = "",
	adminSshPublicKeys: string[] = [],
): string {
	const networkScript = readFileSync(
		join(__dirname, "../../scripts/router_network.py"),
		"utf8",
	);
	const adminCommands = adminSshPublicKeys.length
		? [
				"id -u isuadmin >/dev/null 2>&1 || useradd --create-home --shell /bin/bash isuadmin",
				"printf '%s\\n' 'isuadmin ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/90-isuadmin",
				"chmod 440 /etc/sudoers.d/90-isuadmin",
				...authorizedKeyCommands("isuadmin", adminSshPublicKeys),
			]
		: [];
	return `#cloud-config
${envcheck ? "bootcmd:\n  - [dpkg, --purge, ec2-instance-connect]\n  - [systemctl, daemon-reload]\n  - [systemctl, try-restart, ssh.service]\n" : ""}write_files:
${envcheck ? file("/etc/isucon/envcheck.json", envcheck, "0600") : ""}
${file("/etc/cloud/cloud.cfg.d/99-isucon-router-network.cfg", routerCloudInitConfig, "0644")}
${file(routerDataReconfigurePath, routerDataReconfigureScript, "0755")}
${file(routerNetworkScriptPath, networkScript, "0755")}
${file("/etc/netplan/90-isucon.yaml", routerNetplan(t, dataMac), "0600")}
${file("/etc/isucon/router.env", routerEnvironment(t), "0640", "root:isureal-router")}
${file("/etc/isucon/router-control.env", routerControlEnvironment(t), "0640", "root:isureal-router")}
${file(tlsCertificatePath, tlsPem(tlsCertificate, tlsPrivateKey), "0640", "root:isureal-router")}
runcmd:
${adminCommands.map((cmd) => `  - ${JSON.stringify(cmd)}`).join("\n")}${adminCommands.length ? "\n" : ""}  - until ${routerNetworkScriptPath}; do sleep 5; done
  - systemctl enable --now isureal-router-control isureal-router isureal-haproxy
`;
}

export function environmentCheckConfig(
	serviceAMI: string,
	routerAMI: string,
	types: string[],
): string {
	if (!serviceAMI || !routerAMI || types.length !== 5)
		throw new Error("incomplete envcheck configuration");
	return JSON.stringify({
		service_ami: serviceAMI,
		router_ami: routerAMI,
		types,
	});
}
