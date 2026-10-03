export type NodeId = "app1" | "app2" | "app3" | "db1";
export type Region = "asia" | "europe" | "america";

export interface ServerNetwork {
	id: NodeId;
	// app だけが持つ。db1 は持たない
	region?: Region;
	subnetCidr: string;
	serviceIp: string;
}

// provisioning/ansible/group_vars/all.yml と一致させる
export const teamDomain = "team1-aws.127.0.0.1.nip.io";
export const vpcCidr = "192.168.0.0/16";
export const routerDataSubnetCidr = "192.168.10.0/24";
export const routerDataIp = "192.168.10.10";
export const routerPublicSubnetCidr = "192.168.20.0/24";
export const routerPublicIp = "192.168.20.10";
export const eiceSubnetCidr = "192.168.30.0/24";

export const serverNetworks: readonly ServerNetwork[] = [
	{
		id: "app1",
		region: "asia",
		subnetCidr: "192.168.1.0/24",
		serviceIp: "192.168.1.10",
	},
	{
		id: "app2",
		region: "europe",
		subnetCidr: "192.168.2.0/24",
		serviceIp: "192.168.2.10",
	},
	{
		id: "app3",
		region: "america",
		subnetCidr: "192.168.3.0/24",
		serviceIp: "192.168.3.10",
	},
	{ id: "db1", subnetCidr: "192.168.4.0/24", serviceIp: "192.168.4.10" },
] as const;

// 他のサーバーからの通信は Router が NAT せずに折り返すので、送信元は相手のサブネットになる
export function serverIngressCidrs(destination: ServerNetwork): string[] {
	return [
		`${routerDataIp}/32`,
		...serverNetworks
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

// Router の公開側（EIP とパブリック用サブネット）へサーバーから送らせない。
// 開いていると Envoy 経由で同一リージョン扱いになり、遅延のないサーバー間通信ができてしまう
export function serverEgressRules(routerEip: string): AclRule[] {
	return [
		{
			protocol: "-1",
			ruleNo: 100,
			action: "deny",
			cidrBlock: `${routerEip}/32`,
			fromPort: 0,
			toPort: 0,
		},
		{
			protocol: "-1",
			ruleNo: 110,
			action: "deny",
			cidrBlock: routerPublicSubnetCidr,
			fromPort: 0,
			toPort: 0,
		},
		{
			protocol: "-1",
			ruleNo: 1000,
			action: "allow",
			cidrBlock: "0.0.0.0/0",
			fromPort: 0,
			toPort: 0,
		},
	];
}
