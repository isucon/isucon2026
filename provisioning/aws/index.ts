import { gzipSync } from "node:zlib";
import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";
import * as tls from "@pulumi/tls";
import { config } from "./config";
import {
	eiceSubnetCidr,
	serverEgressRules,
	serverIngressCidrs,
	type Team,
	team,
	vpcCidr,
} from "./lib/topology";
import { routerUserData, serviceUserData } from "./lib/user-data";

// Packer が作った AMI のうち、指定がなければ最新のものを使う
function latestAmi(role: string): pulumi.Output<string> {
	return aws.ec2
		.getAmiOutput({
			owners: ["self"],
			mostRecent: true,
			filters: [
				{ name: "tag:Project", values: ["isucon2026"] },
				{ name: "tag:Role", values: [role] },
			],
		})
		.apply((ami) => ami.id);
}

const serviceAmiId = config.serviceAmiId ?? latestAmi("problem-server");
const routerAmiId = config.routerAmiId ?? latestAmi("router");
const {
	availabilityZone,
	serviceInstanceType,
	routerInstanceType,
	clientCidrs,
	sshCidrs,
} = config;
const teams = Array.from({ length: config.teamCount }, (_, index) =>
	team(index + 1),
);

const tags = { Project: "isucon2026", ManagedBy: "pulumi" };
const allEgress = [
	{ protocol: "-1", fromPort: 0, toPort: 0, cidrBlocks: ["0.0.0.0/0"] },
];

// ---- 全チームで共有するリソース（VPC などはアカウントの上限があるので増やさない） ----

function routerTls(
	tlsTeams: Team[],
	prefix = "",
): {
	certificate: pulumi.Output<string>;
	privateKey: pulumi.Output<string>;
} {
	if (config.tlsCertificate && config.tlsPrivateKey) {
		return {
			certificate: config.tlsCertificate,
			privateKey: config.tlsPrivateKey,
		};
	}
	const key = new tls.PrivateKey(`${prefix}router-tls-key`, {
		algorithm: "ECDSA",
		ecdsaCurve: "P256",
	});
	const certificate = new tls.SelfSignedCert(
		`${prefix}router-tls-certificate`,
		{
			privateKeyPem: key.privateKeyPem,
			subject: { commonName: tlsTeams[0].domain },
			dnsNames: tlsTeams.flatMap((t) => [t.domain, `*.${t.domain}`]),
			validityPeriodHours: 24 * 365,
			allowedUses: ["digital_signature", "key_encipherment", "server_auth"],
		},
	);
	return { certificate: certificate.certPem, privateKey: key.privateKeyPem };
}
const sharedTlsTeams = teams.filter(
	(t) => !config.independentTlsTeams.includes(t.number),
);
const sharedTls =
	sharedTlsTeams.length > 0 ? routerTls(sharedTlsTeams) : undefined;

const vpc = new aws.ec2.Vpc("vpc", {
	cidrBlock: vpcCidr,
	enableDnsHostnames: true,
	enableDnsSupport: true,
	tags: { ...tags, Name: "isucon2026" },
});
const internetGateway = new aws.ec2.InternetGateway("igw", {
	vpcId: vpc.id,
	tags,
});

function subnet(name: string, cidrBlock: string): aws.ec2.Subnet {
	return new aws.ec2.Subnet(`${name}-subnet`, {
		vpcId: vpc.id,
		availabilityZone,
		cidrBlock,
		tags: { ...tags, Name: `isucon2026-${name}` },
	});
}

const publicRouteTable = new aws.ec2.RouteTable("router-public-routes", {
	vpcId: vpc.id,
	routes: [{ cidrBlock: "0.0.0.0/0", gatewayId: internetGateway.id }],
	tags,
});

// SSH はインターネットに開けず、SSM と EC2 Instance Connect Endpoint から入る
const eiceSecurityGroup = new aws.ec2.SecurityGroup("eice-sg", {
	vpcId: vpc.id,
	description: "EC2 Instance Connect Endpoint",
	egress: [
		{ protocol: "tcp", fromPort: 22, toPort: 22, cidrBlocks: [vpcCidr] },
	],
	tags,
});
const instanceConnectEndpoint =
	new aws.ec2transitgateway.InstanceConnectEndpoint("eice", {
		subnetId: subnet("eice", eiceSubnetCidr).id,
		securityGroupIds: [eiceSecurityGroup.id],
		tags,
	});
const sshFromEice = {
	protocol: "tcp",
	fromPort: 22,
	toPort: 22,
	securityGroups: [eiceSecurityGroup.id],
};

const instanceRole = new aws.iam.Role("instance-role", {
	assumeRolePolicy: aws.iam.assumeRolePolicyForPrincipal({
		Service: "ec2.amazonaws.com",
	}),
	tags,
});
new aws.iam.RolePolicyAttachment("instance-ssm-core", {
	role: instanceRole.name,
	policyArn: aws.iam.ManagedPolicy.AmazonSSMManagedInstanceCore,
});
const instanceProfile = new aws.iam.InstanceProfile("instance-profile", {
	role: instanceRole.name,
	tags,
});

const routerPublicSecurityGroup = new aws.ec2.SecurityGroup(
	"router-public-sg",
	{
		vpcId: vpc.id,
		description: "Router public interface",
		ingress: [
			sshFromEice,
			...(clientCidrs.length > 0
				? [443, 8080].map((port) => ({
						protocol: "tcp",
						fromPort: port,
						toPort: port,
						cidrBlocks: clientCidrs,
					}))
				: []),
		],
		egress: allEgress,
		tags,
	},
);

// EBS はスナップショットから作ったボリュームを遅延ロードするので、起動直後の DB の初回読み込みが
// 10MB/s 台まで落ちる。初期化レートを指定して、起動後すぐに全ブロックを読み込ませる
function serverLaunchTemplate(
	name: string,
	volume: { iops?: number; throughput?: number } = {},
): aws.ec2.LaunchTemplate {
	return new aws.ec2.LaunchTemplate(name, {
		blockDeviceMappings: [
			{
				deviceName: "/dev/sda1",
				ebs: {
					volumeType: "gp3",
					volumeInitializationRate: 300,
					deleteOnTermination: "true",
					...volume,
				},
			},
		],
		tags,
	});
}
const appLaunchTemplate = serverLaunchTemplate("server-launch-template");
const databaseLaunchTemplate = serverLaunchTemplate(
	"database-launch-template",
	{
		iops: config.databaseVolumeIops,
		throughput: config.databaseVolumeThroughput,
	},
);

// ---- チームごとのリソース ----

function createTeam(t: Team) {
	const prefix = `team${t.number}`;
	const teamTls = config.independentTlsTeams.includes(t.number)
		? routerTls([t], `${prefix}-`)
		: sharedTls;
	if (!teamTls) throw new Error("Missing Router TLS configuration");
	const { certificate: tlsCertificate, privateKey: tlsPrivateKey } = teamTls;
	const instancesEnabled = !config.disabledInstanceTeams.includes(t.number);
	const teamTags = { ...tags, Team: String(t.number) };

	// Router: パブリック IP（bench の入口）とデータ用 ENI（サーバー間の折り返し）。
	// EIP はアカウントの上限が厳しいので使わず、起動時に割り当てられるパブリック IPv4 を使う
	const routerPublicSubnet = subnet(
		`${prefix}-router-public`,
		t.routerPublicSubnetCidr,
	);
	new aws.ec2.RouteTableAssociation(`${prefix}-router-public-routes`, {
		routeTableId: publicRouteTable.id,
		subnetId: routerPublicSubnet.id,
	});
	const routerDataSecurityGroup = new aws.ec2.SecurityGroup(
		`${prefix}-router-data-sg`,
		{
			vpcId: vpc.id,
			description: `Router data interface for team ${t.number}`,
			ingress: [
				{
					protocol: "-1",
					fromPort: 0,
					toPort: 0,
					cidrBlocks: t.servers.map((server) => server.subnetCidr),
				},
			],
			egress: allEgress,
			tags: teamTags,
		},
	);
	const routerDataEni = new aws.ec2.NetworkInterface(
		`${prefix}-router-data-eni`,
		{
			subnetId: subnet(`${prefix}-router-data`, t.routerDataSubnetCidr).id,
			privateIps: [t.routerDataIp],
			securityGroups: [routerDataSecurityGroup.id],
			sourceDestCheck: false,
			tags: { ...teamTags, Name: `isucon2026-${prefix}-router-data` },
		},
	);
	const router = instancesEnabled
		? new aws.ec2.Instance(
				`${prefix}-router`,
				{
					ami: config.teamImages[t.number]?.routerAmiId ?? routerAmiId,
					availabilityZone,
					instanceType: routerInstanceType,
					iamInstanceProfile: instanceProfile.name,
					subnetId: routerPublicSubnet.id,
					privateIp: t.routerPublicIp,
					associatePublicIpAddress: true,
					vpcSecurityGroupIds: [routerPublicSecurityGroup.id],
					userDataBase64: pulumi
						.all([tlsCertificate, tlsPrivateKey, routerDataEni.macAddress])
						.apply(([certificate, privateKey, dataMac]) =>
							gzipSync(
								Buffer.from(
									routerUserData(t, dataMac, certificate, privateKey),
								),
							).toString("base64"),
						),
					rootBlockDevice: { volumeSize: 20, volumeType: "gp3" },
					tags: {
						...teamTags,
						Name: `isucon2026-${prefix}-router`,
						Role: "router",
					},
				},
				// プライベート IP を固定しているので、置き換え時は古いインスタンスを先に消す。
				// Router のユーザーデータ（write_files と runcmd）は初回起動でしか動かないので、既存の Router に差分を
				// 流しても止めて起動し直すだけになる。データ用 ENI が付いたまま起動し直すとパブリック IPv4 も付かない。
				// 新しいユーザーデータは作り直したときに使う
				{
					dependsOn: [internetGateway],
					deleteBeforeReplace: true,
					ignoreChanges: ["userDataBase64"],
				},
			)
		: undefined;
	if (router) {
		new aws.ec2.NetworkInterfaceAttachment(`${prefix}-router-data-attachment`, {
			instanceId: router.id,
			networkInterfaceId: routerDataEni.id,
			deviceIndex: 1,
		});
	}

	// サーバー: 他のサーバーのサブネット宛ては Router のデータ用 ENI へ送る（VPC 内のミドルボックスルーティング）。
	// 同一サブネット内はルートテーブルを通らないので、サーバーごとにサブネットを分ける。
	// サブネット宛てのルートは宛先のサブネットが存在しないと作れないので、ルートは 1 本ずつ宛先に依存させる
	const serverEgressAcl = router
		? router.publicIp.apply((address) => serverEgressRules(t, address))
		: pulumi.output(
				serverEgressRules(t, t.routerPublicIp).filter(
					(rule) => rule.ruleNo !== 100,
				),
			);
	const serverSubnets = t.servers.map((server) =>
		subnet(`${prefix}-${server.id}`, server.subnetCidr),
	);
	const servers = t.servers.map((network, index) => {
		const name = `${prefix}-${network.id}`;
		const serverSubnet = serverSubnets[index];
		const routeTable = new aws.ec2.RouteTable(`${name}-routes`, {
			vpcId: vpc.id,
			tags: { ...teamTags, Name: `isucon2026-${name}` },
		});
		const routes = [
			new aws.ec2.Route(`${name}-egress`, {
				routeTableId: routeTable.id,
				destinationCidrBlock: "0.0.0.0/0",
				gatewayId: internetGateway.id,
			}),
			...t.servers.flatMap((peer, peerIndex) =>
				peer.id === network.id
					? []
					: [
							new aws.ec2.Route(
								`${name}-to-${peer.id}`,
								{
									routeTableId: routeTable.id,
									destinationCidrBlock: peer.subnetCidr,
									networkInterfaceId: routerDataEni.id,
								},
								{ dependsOn: [serverSubnets[peerIndex]] },
							),
						],
			),
		];
		new aws.ec2.RouteTableAssociation(`${name}-routes`, {
			routeTableId: routeTable.id,
			subnetId: serverSubnet.id,
		});
		new aws.ec2.NetworkAcl(`${name}-acl`, {
			vpcId: vpc.id,
			subnetIds: [serverSubnet.id],
			ingress: [
				{
					protocol: "-1",
					ruleNo: 1000,
					action: "allow",
					cidrBlock: "0.0.0.0/0",
					fromPort: 0,
					toPort: 0,
				},
			],
			egress: serverEgressAcl,
			tags: { ...teamTags, Name: `isucon2026-${name}` },
		});
		const securityGroup = new aws.ec2.SecurityGroup(`${name}-sg`, {
			vpcId: vpc.id,
			description: `ISUCON team ${t.number} ${network.id}`,
			ingress: [
				{
					protocol: "-1",
					fromPort: 0,
					toPort: 0,
					cidrBlocks: serverIngressCidrs(t, network),
				},
				sshFromEice,
				...(sshCidrs.length > 0
					? [
							{
								protocol: "tcp",
								fromPort: 22,
								toPort: 22,
								cidrBlocks: sshCidrs,
							},
						]
					: []),
			],
			egress: allEgress,
			tags: teamTags,
		});
		const isDatabase = network.region === undefined;
		const launchTemplate = isDatabase
			? databaseLaunchTemplate
			: appLaunchTemplate;
		// パブリック IPv4 は外向き通信（パッケージ取得、SSM）専用。インターネットからの着信は SG で受けない
		if (!instancesEnabled) return undefined;
		return new aws.ec2.Instance(
			`${name}-instance`,
			{
				ami: config.teamImages[t.number]?.serviceAmiId ?? serviceAmiId,
				availabilityZone,
				instanceType: isDatabase
					? config.databaseInstanceType
					: serviceInstanceType,
				iamInstanceProfile: instanceProfile.name,
				subnetId: serverSubnet.id,
				privateIp: network.serviceIp,
				associatePublicIpAddress: true,
				vpcSecurityGroupIds: [securityGroup.id],
				userData: serviceUserData(
					t,
					network,
					config.teamSshPublicKeys[t.number],
					config.teamAdminSshPublicKeys[t.number],
					config.guardTeams.includes(t.number),
				),
				// ルートボリュームの容量は AMI のスナップショットに合わせる
				launchTemplate: {
					id: launchTemplate.id,
					version: launchTemplate.latestVersion.apply(String),
				},
				tags: {
					...teamTags,
					Name: `isucon2026-${name}`,
					Role:
						config.portalTeamIds[t.number] === undefined
							? "problem-server"
							: "contestant",
					...(config.portalTeamIds[t.number] === undefined
						? {}
						: {
								IsuconTeamID: String(config.portalTeamIds[t.number]),
								IsuconInstanceNum: String(index + 1),
							}),
				},
			},
			// プライベート IP を固定しているので、置き換え時は古いインスタンスを先に消す
			{
				dependsOn: routes,
				deleteBeforeReplace: true,
				protect: isDatabase && config.protectedDatabaseTeams.includes(t.number),
			},
		);
	});

	return {
		number: t.number,
		appHostname: `app.${t.domain}`,
		routerInstanceId: router?.id,
		routerPublicAddress: router?.publicIp,
		serviceInstanceIds: servers.flatMap((server) =>
			server ? [server.id] : [],
		),
		servicePublicAddresses: servers.flatMap((server) =>
			server ? [server.publicIp] : [],
		),
	};
}

export const teamOutputs = teams.map(createTeam);
export const instanceConnectEndpointId = instanceConnectEndpoint.id;
