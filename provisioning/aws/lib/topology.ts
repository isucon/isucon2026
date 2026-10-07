export type NodeId = "app1" | "app2" | "app3" | "db1";
export type Region = "asia" | "europe" | "america";

export interface ServerNetwork {
	id: NodeId;
	// app だけが持つ。db1 は持たない
	region?: Region;
	subnetCidr: string;
	serviceIp: string;
}

export interface Team {
	number: number;
	domain: string;
	servers: readonly ServerNetwork[];
	routerDataSubnetCidr: string;
	routerDataIp: string;
	routerPublicSubnetCidr: string;
	routerPublicIp: string;
}

// VPC と EC2 Instance Connect Endpoint は全チームで共有する
export const vpcCidr = "192.168.0.0/16";
export const eiceSubnetCidr = "192.168.240.0/24";
// Router の Envoy が転送する app の nginx のポート（provisioning/ansible の app_http_port と一致させる）
export const appHttpPort = 80;
// チームごとに 16 個の /24 を使うので、EIC Endpoint のサブネットと重ならない上限
export const maxTeams = 15;

const nodes: readonly { id: NodeId; region?: Region }[] = [
	{ id: "app1", region: "asia" },
	{ id: "app2", region: "europe" },
	{ id: "app3", region: "america" },
	{ id: "db1" },
];

// チーム n（1 始まり）は 192.168.(16(n-1)+k).0/24 を使う。
// k = 1〜4 がサーバー、10 が Router のデータ用、11 が Router のパブリック用
export function team(number: number): Team {
	const base = 16 * (number - 1);
	const subnet = (k: number) => `192.168.${base + k}.0/24`;
	const host = (k: number) => `192.168.${base + k}.10`;
	return {
		number,
		domain: `team${number}-aws.127.0.0.1.nip.io`,
		servers: nodes.map((node, index) => ({
			...node,
			subnetCidr: subnet(index + 1),
			serviceIp: host(index + 1),
		})),
		routerDataSubnetCidr: subnet(10),
		routerDataIp: host(10),
		routerPublicSubnetCidr: subnet(11),
		routerPublicIp: host(11),
	};
}

export function databaseIp(t: Team): string {
	const db = t.servers.find((server) => server.region === undefined);
	if (db === undefined) {
		throw new Error("team has no database server");
	}
	return db.serviceIp;
}

// 他のサーバーからの通信は Router が NAT せずに折り返すので、送信元は相手のサブネットになる。
// 同じ VPC の他チームのサブネットは許可しない
export function serverIngressCidrs(
	t: Team,
	destination: ServerNetwork,
): string[] {
	return [
		`${t.routerDataIp}/32`,
		...t.servers
			.filter((source) => source.id !== destination.id)
			.map((source) => source.subnetCidr),
	];
}

export interface AclRule {
	protocol: "-1";
	ruleNo: number;
	action: "allow" | "deny";
	cidrBlock: string;
	fromPort: 0;
	toPort: 0;
}

// Router の公開側（パブリック IP とパブリック用サブネット）へサーバーから送らせない。
// 開いていると Envoy 経由で同一リージョン扱いになり、遅延のないサーバー間通信ができてしまう
export function serverEgressRules(
	t: Team,
	routerPublicAddress: string,
): AclRule[] {
	const rule = (
		ruleNo: number,
		action: "allow" | "deny",
		cidrBlock: string,
	): AclRule => ({
		protocol: "-1",
		ruleNo,
		action,
		cidrBlock,
		fromPort: 0,
		toPort: 0,
	});
	return [
		rule(100, "deny", `${routerPublicAddress}/32`),
		rule(110, "deny", t.routerPublicSubnetCidr),
		rule(1000, "allow", "0.0.0.0/0"),
	];
}
