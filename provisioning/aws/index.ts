import { gzipSync } from "node:zlib";
import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";
import * as tls from "@pulumi/tls";
import { config } from "./config";
import {
	eiceSubnetCidr,
	routerDataIp,
	routerDataSubnetCidr,
	routerPublicIp,
	routerPublicSubnetCidr,
	serverEgressRules,
	serverIngressCidrs,
	serverNetworks,
	teamDomain,
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
} = config;

function routerTls(): {
	certificate: pulumi.Output<string>;
	privateKey: pulumi.Output<string>;
} {
	if (config.tlsCertificate && config.tlsPrivateKey) {
		return {
			certificate: config.tlsCertificate,
			privateKey: config.tlsPrivateKey,
		};
	}
	const key = new tls.PrivateKey("router-tls-key", {
		algorithm: "ECDSA",
		ecdsaCurve: "P256",
	});
	const certificate = new tls.SelfSignedCert("router-tls-certificate", {
		privateKeyPem: key.privateKeyPem,
		subject: { commonName: teamDomain },
		dnsNames: [teamDomain, `*.${teamDomain}`],
		validityPeriodHours: 24 * 365,
		allowedUses: ["digital_signature", "key_encipherment", "server_auth"],
	});
	return { certificate: certificate.certPem, privateKey: key.privateKeyPem };
}
const { certificate: tlsCertificate, privateKey: tlsPrivateKey } = routerTls();

const tags = { Project: "isucon2026", ManagedBy: "pulumi" };
const allEgress = [
	{ protocol: "-1", fromPort: 0, toPort: 0, cidrBlocks: ["0.0.0.0/0"] },
];

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

const routerDataSubnet = subnet("router-data", routerDataSubnetCidr);
const routerPublicSubnet = subnet("router-public", routerPublicSubnetCidr);
const eiceSubnet = subnet("eice", eiceSubnetCidr);

const publicRouteTable = new aws.ec2.RouteTable("router-public-routes", {
	vpcId: vpc.id,
	routes: [{ cidrBlock: "0.0.0.0/0", gatewayId: internetGateway.id }],
	tags,
});
new aws.ec2.RouteTableAssociation("router-public-routes", {
	routeTableId: publicRouteTable.id,
	subnetId: routerPublicSubnet.id,
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
		subnetId: eiceSubnet.id,
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

// Router: パブリック用 ENI（EIP、bench の入口）とデータ用 ENI（サーバー間の折り返し）の 2 本
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
const routerDataSecurityGroup = new aws.ec2.SecurityGroup("router-data-sg", {
	vpcId: vpc.id,
	description: "Router data interface",
	ingress: [
		{
			protocol: "-1",
			fromPort: 0,
			toPort: 0,
			cidrBlocks: serverNetworks.map((network) => network.subnetCidr),
		},
	],
	egress: allEgress,
	tags,
});
const routerPublicEni = new aws.ec2.NetworkInterface("router-public-eni", {
	subnetId: routerPublicSubnet.id,
	privateIps: [routerPublicIp],
	securityGroups: [routerPublicSecurityGroup.id],
	tags: { ...tags, Name: "isucon2026-router-public" },
});
const routerDataEni = new aws.ec2.NetworkInterface("router-data-eni", {
	subnetId: routerDataSubnet.id,
	privateIps: [routerDataIp],
	securityGroups: [routerDataSecurityGroup.id],
	sourceDestCheck: false,
	tags: { ...tags, Name: "isucon2026-router-data" },
});
const routerEip = new aws.ec2.Eip(
	"router-eip",
	{
		domain: "vpc",
		networkInterface: routerPublicEni.id,
		tags,
	},
	{ dependsOn: [internetGateway] },
);

// EBS はスナップショットから作ったボリュームを遅延ロードするので、起動直後の DB の初回読み込みが
// 10MB/s 台まで落ちる。初期化レートを指定して、起動後すぐに全ブロックを読み込ませる
const serverLaunchTemplate = new aws.ec2.LaunchTemplate(
	"server-launch-template",
	{
		blockDeviceMappings: [
			{
				deviceName: "/dev/sda1",
				ebs: {
					volumeType: "gp3",
					volumeInitializationRate: 300,
					deleteOnTermination: "true",
				},
			},
		],
		tags,
	},
);

// サーバー: 他のサーバーのサブネット宛ては Router のデータ用 ENI へ送る（VPC 内のミドルボックスルーティング）。
// 同一サブネット内はルートテーブルを通らないので、サーバーごとにサブネットを分ける。
// サブネット宛てのルートは宛先のサブネットが存在しないと作れないので、ルートは 1 本ずつ宛先に依存させる
const serverEgressAcl = routerEip.publicIp.apply(serverEgressRules);
const serverSubnets = serverNetworks.map((network) =>
	subnet(network.id, network.subnetCidr),
);
const servers = serverNetworks.map((network, index) => {
	const serverSubnet = serverSubnets[index];
	const routeTable = new aws.ec2.RouteTable(`${network.id}-routes`, {
		vpcId: vpc.id,
		tags: { ...tags, Name: `isucon2026-${network.id}` },
	});
	const routes = [
		new aws.ec2.Route(`${network.id}-egress`, {
			routeTableId: routeTable.id,
			destinationCidrBlock: "0.0.0.0/0",
			gatewayId: internetGateway.id,
		}),
		...serverNetworks.flatMap((peer, peerIndex) =>
			peer.id === network.id
				? []
				: [
						new aws.ec2.Route(
							`${network.id}-to-${peer.id}`,
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
	new aws.ec2.RouteTableAssociation(`${network.id}-routes`, {
		routeTableId: routeTable.id,
		subnetId: serverSubnet.id,
	});
	new aws.ec2.NetworkAcl(`${network.id}-acl`, {
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
		tags: { ...tags, Name: `isucon2026-${network.id}` },
	});
	const securityGroup = new aws.ec2.SecurityGroup(`${network.id}-sg`, {
		vpcId: vpc.id,
		description: `ISUCON ${network.id}`,
		ingress: [
			{
				protocol: "-1",
				fromPort: 0,
				toPort: 0,
				cidrBlocks: serverIngressCidrs(network),
			},
			sshFromEice,
		],
		egress: allEgress,
		tags,
	});
	// パブリック IPv4 は外向き通信（パッケージ取得、SSM）専用。インターネットからの着信は SG で受けない
	return new aws.ec2.Instance(
		`${network.id}-instance`,
		{
			ami: serviceAmiId,
			availabilityZone,
			instanceType: serviceInstanceType,
			iamInstanceProfile: instanceProfile.name,
			subnetId: serverSubnet.id,
			privateIp: network.serviceIp,
			associatePublicIpAddress: true,
			vpcSecurityGroupIds: [securityGroup.id],
			userData: serviceUserData(network),
			// ルートボリュームの容量は AMI のスナップショットに合わせる
			launchTemplate: {
				id: serverLaunchTemplate.id,
				version: serverLaunchTemplate.latestVersion.apply(String),
			},
			tags: {
				...tags,
				Name: `isucon2026-${network.id}`,
				Role: "problem-server",
			},
			// プライベート IP を固定しているので、置き換え時は古いインスタンスを先に消す
		},
		{ dependsOn: routes, deleteBeforeReplace: true },
	);
});

const router = new aws.ec2.Instance(
	"router-instance",
	{
		ami: routerAmiId,
		availabilityZone,
		instanceType: routerInstanceType,
		iamInstanceProfile: instanceProfile.name,
		primaryNetworkInterface: { networkInterfaceId: routerPublicEni.id },
		userDataBase64: pulumi
			.all([
				tlsCertificate,
				tlsPrivateKey,
				routerPublicEni.macAddress,
				routerDataEni.macAddress,
			])
			.apply(([certificate, privateKey, publicMac, dataMac]) =>
				gzipSync(
					Buffer.from(
						routerUserData(publicMac, dataMac, certificate, privateKey),
					),
				).toString("base64"),
			),
		rootBlockDevice: { volumeSize: 20, volumeType: "gp3" },
		tags: { ...tags, Name: "isucon2026-router", Role: "router" },
	},
	{ dependsOn: [routerEip] },
);
new aws.ec2.NetworkInterfaceAttachment("router-data-attachment", {
	instanceId: router.id,
	networkInterfaceId: routerDataEni.id,
	deviceIndex: 1,
});

export const routerInstanceId = router.id;
export const routerPublicAddress = routerEip.publicIp;
export const appHostname = `app.${teamDomain}`;
export const instanceConnectEndpointId = instanceConnectEndpoint.id;
export const serviceInstanceIds = servers.map((server) => server.id);
export const servicePrivateIps = serverNetworks.map(
	(network) => network.serviceIp,
);
