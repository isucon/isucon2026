import * as pulumi from "@pulumi/pulumi";
import { expect, test } from "vitest";
import { createSupervisor } from "../lib/supervisor";
const resources: pulumi.runtime.MockResourceArgs[] = [];
pulumi.runtime.setMocks({
  newResource: (args) => {
    resources.push(args);
    return {
      id: args.name + "-id",
      state: { ...args.inputs, name: args.name, arn: "arn:mock:" + args.name },
    };
  },
  call: (args) => args.inputs,
});
test("shares the portal private subnet and fixed NAT egress without a dedicated VPC", async () => {
  await pulumi.runtime.runInPulumiStack(async () =>
    createSupervisor({
      availabilityZone: "ap-northeast-1a",
      region: "ap-northeast-1",
      image: `registry.example/bench@sha256:${"a".repeat(64)}`,
      endpoint: "https://portal.example",
      tokenParameterArn:
        "arn:aws:ssm:ap-northeast-1:123456789012:parameter/token",
      instanceName: "apne1-az4",
      teamIds: ["1", "2", "3"],
      routerNetworkInterfaces: {
        "app.team3.example": "eni-333",
        "app.team5.example": "eni-555",
      },
      detachedRouterHosts: ["app.team3.example"],
      routerSecurityGroupIds: ["sg-router"],
      sharedNetwork: {
        vpcId: "vpc-portal",
        subnetId: "subnet-portal-private",
        egressPublicIp: "203.0.113.5",
      },
    })
  );
  expect(resources.filter((r) => r.type === "aws:ec2/natGateway:NatGateway"))
    .toHaveLength(0);
  expect(resources.filter((r) => r.type === "aws:ec2/vpc:Vpc")).toHaveLength(0);
  expect(
    resources.find((r) => r.type === "aws:ec2/securityGroup:SecurityGroup")
      ?.inputs.vpcId,
  ).toBe("vpc-portal");
  expect(resources.filter((r) => r.type === "aws:ec2/eip:Eip")).toHaveLength(2);
  const associations = resources.filter((r) =>
    r.type === "aws:ec2/eipAssociation:EipAssociation"
  );
  expect(associations.map((r) => r.inputs.networkInterfaceId)).toEqual([
    "eni-555",
  ]);
  const workers = resources.filter((r) => r.type === "aws:ecs/service:Service");
  expect(workers).toHaveLength(3);
  for (const worker of workers) {
    expect(worker.inputs.networkConfiguration.subnets).toEqual([
      "subnet-portal-private",
    ]);
    expect(worker.inputs.networkConfiguration.assignPublicIp).toBe(false);
  }
  expect(
    resources.find((r) =>
      r.type === "aws:ec2/securityGroupRule:SecurityGroupRule"
    )?.inputs.cidrBlocks,
  ).toEqual(["203.0.113.5/32"]);
});
