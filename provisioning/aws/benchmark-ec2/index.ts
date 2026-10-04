import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";
import { benchmarkUserData } from "../lib/benchmark-ec2";
const c = new pulumi.Config();
const region = new pulumi.Config("aws").require("region");
const image = c.require("image");
const userData = benchmarkUserData(image, region);
const role = new aws.iam.Role("benchmark", {
  assumeRolePolicy: aws.iam.assumeRolePolicyForPrincipal({
    Service: "ec2.amazonaws.com",
  }),
});
new aws.iam.RolePolicyAttachment("ssm", {
  role: role.name,
  policyArn: "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
});
new aws.iam.RolePolicy("ecr", {
  role: role.name,
  policy: JSON.stringify({
    Version: "2012-10-17",
    Statement: [{
      Effect: "Allow",
      Action: "ecr:GetAuthorizationToken",
      Resource: "*",
    }, {
      Effect: "Allow",
      Action: [
        "ecr:BatchGetImage",
        "ecr:GetDownloadUrlForLayer",
        "ecr:BatchCheckLayerAvailability",
      ],
      Resource: c.require("repositoryArn"),
    }],
  }),
});
const profile = new aws.iam.InstanceProfile("benchmark", { role: role.name });
const sg = new aws.ec2.SecurityGroup("benchmark", {
  vpcId: c.require("vpcId"),
  description: "SSM only, no inbound access",
  ingress: [],
  egress: [{
    protocol: "-1",
    fromPort: 0,
    toPort: 0,
    cidrBlocks: ["0.0.0.0/0"],
  }],
});
new aws.ec2.SecurityGroupRule("router", {
  type: "ingress",
  securityGroupId: c.require("routerSecurityGroupId"),
  sourceSecurityGroupId: sg.id,
  protocol: "tcp",
  fromPort: 8080,
  toPort: 8080,
});
const ami = aws.ssm.getParameterOutput({
  name: "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64",
});
const vm = new aws.ec2.Instance("benchmark", {
  ami: ami.value,
  instanceType: "c7i.xlarge",
  subnetId: c.require("subnetId"),
  associatePublicIpAddress: true,
  vpcSecurityGroupIds: [sg.id],
  iamInstanceProfile: profile.name,
  userData,
  userDataReplaceOnChange: true,
  metadataOptions: { httpTokens: "required", httpEndpoint: "enabled" },
  rootBlockDevice: { volumeSize: 30, volumeType: "gp3", encrypted: true },
  tags: {
    Name: "isucon2026-trial-benchmark",
    Project: "isucon2026",
    ManagedBy: "pulumi",
    Role: "benchmark",
  },
});
export const instanceId = vm.id;
export const publicIp = vm.publicIp;
export const privateIp = vm.privateIp;
