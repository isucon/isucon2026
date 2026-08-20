import * as fs from "node:fs";
import * as path from "node:path";
import { gzipSync } from "node:zlib";
import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";
import {
  controlRouterIp,
  controlSubnetCidr,
  serverNetworks,
} from "./lib/topology";
import { routerUserData, serviceUserData } from "./lib/user-data";

function compressedUserData(content: string): string {
  return gzipSync(Buffer.from(content, "utf8")).toString("base64");
}

const release = JSON.parse(
  fs.readFileSync(path.resolve(__dirname, "../config/release.json"), "utf8"),
) as {
  releaseId: string;
  ubuntu: { cloudImages: { amd64: { region: string } } };
};
const config = new pulumi.Config();
const releaseId = config.require("releaseId");
const serviceAmiId = config.require("serviceAmiId");
const routerAmiId = config.require("routerAmiId");
const availabilityZone = config.get("availabilityZone") ?? "ap-northeast-1a";
const serviceInstanceType = config.get("serviceInstanceType") ?? "t3.small";
const routerInstanceType = config.get("routerInstanceType") ?? "c8in.large";
const keyName = config.get("keyName");
const sshCidr = config.get("sshCidr");
const clientCidr = config.get("clientCidr");
const portalEndpoint = "http://127.0.0.1:18082";
const publicDomain = config.get("publicDomain") ?? "127.0.0.1.nip.io";
const databasePassword = config.requireSecret("databasePassword");
const cursorHmacKey = config.requireSecret("cursorHmacKey");
const tlsCertificate = config.requireSecret("tlsCertificate");
const tlsPrivateKey = config.requireSecret("tlsPrivateKey");
const configuredRegion =
  new pulumi.Config("aws").get("region") ??
  process.env.AWS_REGION ??
  process.env.AWS_DEFAULT_REGION ??
  release.ubuntu.cloudImages.amd64.region;
if (releaseId !== release.releaseId) {
  throw new pulumi.RunError(
    `releaseId must match provisioning/config/release.json: ${release.releaseId}`,
  );
}
if (!availabilityZone.startsWith(`${configuredRegion}`)) {
  throw new pulumi.RunError(
    `availabilityZone ${availabilityZone} is outside configured AWS region ${configuredRegion}`,
  );
}
if ((keyName === undefined) !== (sshCidr === undefined)) {
  throw new pulumi.RunError("keyName and sshCidr must be configured together");
}

const tags = {
  Project: "isucon2026",
  Environment: "aws-validation",
  ReleaseId: releaseId,
  ManagedBy: "pulumi",
};

const serviceVpcs = serverNetworks.map(
  (network) =>
    new aws.ec2.Vpc(`${network.id}-vpc`, {
      cidrBlock: network.vpcCidr,
      enableDnsHostnames: true,
      enableDnsSupport: true,
      tags: { ...tags, Name: `isucon2026-${network.id}` },
    }),
);
const controlSubnet = new aws.ec2.Subnet("control-subnet", {
  availabilityZone,
  cidrBlock: controlSubnetCidr,
  vpcId: serviceVpcs[0].id,
  tags: { ...tags, Name: "isucon2026-control" },
});
const routerControlSecurityGroup = new aws.ec2.SecurityGroup("router-control-sg", {
  vpcId: serviceVpcs[0].id,
  description: "ISUCON Router control interface",
  ingress: sshCidr
    ? [
        { protocol: "tcp", fromPort: 22, toPort: 22, cidrBlocks: [sshCidr] },
        ...(clientCidr
          ? [
              { protocol: "tcp", fromPort: 443, toPort: 443, cidrBlocks: [clientCidr] },
              { protocol: "tcp", fromPort: 8080, toPort: 8080, cidrBlocks: [clientCidr] },
            ]
          : []),
      ]
    : clientCidr
      ? [
          { protocol: "tcp", fromPort: 443, toPort: 443, cidrBlocks: [clientCidr] },
          { protocol: "tcp", fromPort: 8080, toPort: 8080, cidrBlocks: [clientCidr] },
        ]
      : [],
  egress: [
    {
      protocol: "-1",
      fromPort: 0,
      toPort: 0,
      cidrBlocks: ["0.0.0.0/0"],
    },
  ],
  tags,
});
const routerRole = new aws.iam.Role("router-role", {
  assumeRolePolicy: aws.iam.assumeRolePolicyForPrincipal({
    Service: "ec2.amazonaws.com",
  }),
  tags,
});
new aws.iam.RolePolicyAttachment("router-ssm-core", {
  role: routerRole.name,
  policyArn: aws.iam.ManagedPolicy.AmazonSSMManagedInstanceCore,
});
const routerInstanceProfile = new aws.iam.InstanceProfile("router-profile", {
  role: routerRole.name,
  tags,
});
const serviceRole = new aws.iam.Role("service-role", {
  assumeRolePolicy: aws.iam.assumeRolePolicyForPrincipal({
    Service: "ec2.amazonaws.com",
  }),
  tags,
});
new aws.iam.RolePolicyAttachment("service-ssm-core", {
  role: serviceRole.name,
  policyArn: aws.iam.ManagedPolicy.AmazonSSMManagedInstanceCore,
});
const serviceInstanceProfile = new aws.iam.InstanceProfile("service-profile", {
  role: serviceRole.name,
  tags,
});
const routerControlEni = new aws.ec2.NetworkInterface("router-control-eni", {
  subnetId: controlSubnet.id,
  privateIps: [controlRouterIp],
  securityGroups: [routerControlSecurityGroup.id],
  sourceDestCheck: false,
  tags: { ...tags, Name: "isucon2026-router-control" },
});
const internetGateway = new aws.ec2.InternetGateway("control-igw", {
  vpcId: serviceVpcs[0].id,
  tags,
});
const controlRouteTable = new aws.ec2.RouteTable("control-routes", {
  vpcId: serviceVpcs[0].id,
  routes: [
    {
      cidrBlock: "0.0.0.0/0",
      gatewayId: internetGateway.id,
    },
  ],
  tags,
});
new aws.ec2.RouteTableAssociation("control-routes", {
  routeTableId: controlRouteTable.id,
  subnetId: controlSubnet.id,
});
const routerPublicIp = new aws.ec2.Eip("router-public-ip", {
  domain: "vpc",
  networkInterface: routerControlEni.id,
  tags,
});

type ServerResources = {
  network: (typeof serverNetworks)[number];
  vpc: aws.ec2.Vpc;
  serviceSubnet: aws.ec2.Subnet;
  routerSubnet: aws.ec2.Subnet;
  serviceEni: aws.ec2.NetworkInterface;
  routerEni: aws.ec2.NetworkInterface;
  instance: aws.ec2.Instance;
};

const servers: ServerResources[] = serverNetworks.map((network, index) => {
  const vpc = serviceVpcs[index];
  const serviceSubnet = new aws.ec2.Subnet(`${network.id}-service-subnet`, {
    availabilityZone,
    cidrBlock: network.serviceSubnetCidr,
    vpcId: vpc.id,
    tags: { ...tags, Name: `isucon2026-${network.id}-service` },
  });
  const routerSubnet = new aws.ec2.Subnet(`${network.id}-router-subnet`, {
    availabilityZone,
    cidrBlock: network.routerSubnetCidr,
    vpcId: vpc.id,
    tags: { ...tags, Name: `isucon2026-${network.id}-router` },
  });
  const serviceRouteTable = new aws.ec2.RouteTable(`${network.id}-service-routes`, {
    vpcId: vpc.id,
    tags: { ...tags, Name: `isucon2026-${network.id}-service` },
  });
  new aws.ec2.RouteTableAssociation(`${network.id}-service-routes`, {
    routeTableId: serviceRouteTable.id,
    subnetId: serviceSubnet.id,
  });
  const serviceSecurityGroup = new aws.ec2.SecurityGroup(`${network.id}-service-sg`, {
    vpcId: vpc.id,
    description: `ISUCON ${network.id} service interface`,
    ingress: [
      {
        protocol: "tcp",
        fromPort: 8080,
        toPort: 8080,
        cidrBlocks: serverNetworks.map((item) => item.vpcCidr),
      },
      ...(sshCidr
        ? [{ protocol: "tcp", fromPort: 22, toPort: 22, cidrBlocks: [sshCidr] }]
        : []),
    ],
    egress: [
      {
        protocol: "-1",
        fromPort: 0,
        toPort: 0,
        cidrBlocks: ["0.0.0.0/0"],
      },
    ],
    tags,
  });
  const routerSecurityGroup = new aws.ec2.SecurityGroup(`${network.id}-router-sg`, {
    vpcId: vpc.id,
    description: `ISUCON Router data interface for ${network.id}`,
    ingress: [
      {
        protocol: "-1",
        fromPort: 0,
        toPort: 0,
        cidrBlocks: serverNetworks.map((item) => item.vpcCidr),
      },
    ],
    egress: [
      {
        protocol: "-1",
        fromPort: 0,
        toPort: 0,
        cidrBlocks: ["0.0.0.0/0"],
      },
    ],
    tags,
  });
  const endpointSecurityGroup = new aws.ec2.SecurityGroup(`${network.id}-endpoint-sg`, {
    vpcId: vpc.id,
    description: `Private AWS API endpoints for ${network.id}`,
    ingress: [
      {
        protocol: "tcp",
        fromPort: 443,
        toPort: 443,
        cidrBlocks: [network.vpcCidr],
      },
    ],
    egress: [
      {
        protocol: "-1",
        fromPort: 0,
        toPort: 0,
        cidrBlocks: ["0.0.0.0/0"],
      },
    ],
    tags,
  });
  for (const service of ["ssm", "ssmmessages", "ec2messages"]) {
    new aws.ec2.VpcEndpoint(`${network.id}-${service}-endpoint`, {
      vpcId: vpc.id,
      serviceName: `com.amazonaws.${configuredRegion}.${service}`,
      vpcEndpointType: "Interface",
      privateDnsEnabled: true,
      subnetIds: [serviceSubnet.id],
      securityGroupIds: [endpointSecurityGroup.id],
      tags: { ...tags, Name: `isucon2026-${network.id}-${service}` },
    });
  }
  const routerEni = new aws.ec2.NetworkInterface(`${network.id}-router-eni`, {
    subnetId: routerSubnet.id,
    privateIps: [network.routerIp],
    securityGroups: [routerSecurityGroup.id],
    sourceDestCheck: false,
    tags: { ...tags, Name: `isucon2026-router-${network.id}` },
  });
  const serviceEni = new aws.ec2.NetworkInterface(`${network.id}-service-eni`, {
    subnetId: serviceSubnet.id,
    privateIps: [network.serviceIp],
    securityGroups: [serviceSecurityGroup.id],
    sourceDestCheck: true,
    tags: { ...tags, Name: `isucon2026-${network.id}` },
  });
  const instance = new aws.ec2.Instance(`${network.id}-instance`, {
    ami: serviceAmiId,
    availabilityZone,
    instanceType: serviceInstanceType,
    iamInstanceProfile: serviceInstanceProfile.name,
    keyName,
    primaryNetworkInterface: {
      networkInterfaceId: serviceEni.id,
    },
    userDataBase64: pulumi
      .all([databasePassword, cursorHmacKey])
      .apply(([password, cursor]) =>
        compressedUserData(
          serviceUserData(releaseId, network, publicDomain, password, cursor),
        ),
      ),
    rootBlockDevice: {
      encrypted: true,
      volumeSize: 40,
      volumeType: "gp3",
    },
    tags: { ...tags, Name: `isucon2026-${network.id}`, Role: "problem-server" },
  });
  for (const peer of serverNetworks) {
    if (peer.id === network.id) continue;
    new aws.ec2.Route(`${network.id}-to-${peer.id}`, {
      routeTableId: serviceRouteTable.id,
      destinationCidrBlock: peer.vpcCidr,
      networkInterfaceId: routerEni.id,
    });
  }
  new aws.ec2.Route(`${network.id}-egress`, {
    routeTableId: serviceRouteTable.id,
    destinationCidrBlock: "0.0.0.0/0",
    networkInterfaceId: routerEni.id,
  });
  return { network, vpc, serviceSubnet, routerSubnet, serviceEni, routerEni, instance };
});

const router = new aws.ec2.Instance(
  "router-instance",
  {
    ami: routerAmiId,
    availabilityZone,
    instanceType: routerInstanceType,
    iamInstanceProfile: routerInstanceProfile.name,
    keyName,
    primaryNetworkInterface: {
      networkInterfaceId: routerControlEni.id,
    },
    userDataBase64: pulumi
      .all([
        tlsCertificate,
        tlsPrivateKey,
        routerControlEni.macAddress,
        ...servers.map((server) => server.routerEni.macAddress),
      ])
      .apply(([certificate, privateKey, controlMac, ...dataMacs]) =>
        compressedUserData(
          routerUserData(
            releaseId,
            portalEndpoint,
            publicDomain,
            controlRouterIp,
            controlMac,
            dataMacs,
            certificate,
            privateKey,
          ),
        ),
      ),
    rootBlockDevice: {
      encrypted: true,
      volumeSize: 20,
      volumeType: "gp3",
    },
    tags: { ...tags, Name: "isucon2026-router", Role: "router" },
  },
  { dependsOn: servers.map((server) => server.routerEni) },
);

servers.forEach((server, index) => {
  new aws.ec2.NetworkInterfaceAttachment(`${server.network.id}-router-attachment`, {
    instanceId: router.id,
    networkInterfaceId: server.routerEni.id,
    deviceIndex: index + 1,
  });
});

export const routerInstanceId = router.id;
export const routerControlPrivateIp = controlRouterIp;
export const routerPublicAddress = routerPublicIp.publicIp;
export const appHostname = `app.team1-aws.${publicDomain}`;
export const serviceInstanceIds = servers.map((server) => server.instance.id);
export const servicePrivateIps = servers.map((server) => server.network.serviceIp);
export const vpcIds = servers.map((server) => server.vpc.id);
