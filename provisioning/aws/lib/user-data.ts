import {
	routerDataIp,
	routerDataSubnetCidr,
	serverNetworks,
	type ServerNetwork,
} from "./topology";

// トポロジーは AMI に焼き込んであるので、起動時は役割に応じてサービスを有効にするだけ
export function serviceUserData(network: ServerNetwork): string {
	const commands =
		network.region === undefined
			? ["systemctl enable --now postgresql"]
			: [
					`sed -i 's/^ISUREAL_REGION=.*/ISUREAL_REGION=${network.region}/' /home/isucon/env.sh`,
					"systemctl enable --now isureal-go nginx",
				];
	return `#!/bin/sh
set -eu
${commands.join("\n")}
`;
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

// 既定経路はパブリック用 ENI に残し、サーバーのサブネット宛てだけをデータ用 ENI から VPC へ送る
export function routerNetplan(publicMac: string, dataMac: string): string {
	const routes = serverNetworks
		.map(
			(network) => `        - to: ${network.subnetCidr}
          via: ${firstUsable(routerDataSubnetCidr)}`,
		)
		.join("\n");
	return `network:
  version: 2
  ethernets:
    public0:
      match:
        macaddress: ${publicMac.toLowerCase()}
      set-name: public0
      dhcp4: true
    data0:
      match:
        macaddress: ${dataMac.toLowerCase()}
      set-name: data0
      dhcp4: false
      addresses:
        - ${routerDataIp}/${routerDataSubnetCidr.split("/")[1]}
      routes:
${routes}
`;
}

// データ用 ENI は起動後に attach されるので、アドレスが載るまで netplan を当て直してから起動する
export function routerUserData(
	publicMac: string,
	dataMac: string,
	tlsCertificate: string,
	tlsPrivateKey: string,
): string {
	return `#cloud-config
write_files:
${file("/etc/netplan/90-isucon.yaml", routerNetplan(publicMac, dataMac), "0600")}
${file("/etc/isucon/envoy/tls/server.crt", tlsCertificate, "0644")}
${file("/etc/isucon/envoy/tls/server.key", tlsPrivateKey, "0640", "root:isureal-router")}
runcmd:
  - rm -f /etc/netplan/50-cloud-init.yaml
  - until ip -4 -o address show dev data0 | grep -Fq ' ${routerDataIp}/'; do netplan apply; sleep 5; done
  - systemctl enable --now isureal-portal isureal-router isureal-envoy
`;
}
