import * as pulumi from "@pulumi/pulumi";
import { expect, test } from "vitest";
import { createProductionSupervisor } from "./infrastructure";

const resources: pulumi.runtime.MockResourceArgs[] = [];
pulumi.runtime.setMocks({
  newResource: (args) => {
    resources.push(args);
    return {
      id: `${args.name}-id`,
      state: {
        ...args.inputs,
        arn: `arn:mock:${args.name}`,
        name: args.inputs.name ?? args.name,
      },
    };
  },
  call: (args) => args.inputs,
});

test("isolates production infrastructure and delegates three ARM64 workers to ecspresso", async () => {
  let deployment!: pulumi.Unwrap<
    ReturnType<typeof createProductionSupervisor>["deployments"]
  >[number];
  await pulumi.runtime.runInPulumiStack(async () => {
    const result = createProductionSupervisor({
      region: "ap-northeast-1",
      name: "isucon2026-production-supervisor",
      endpoint: "https://portal.isucon.net",
      image: `example.com/supervisor@sha256:${"a".repeat(64)}`,
      tokenSecretArn:
        "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:portal-production/application-abcdef",
      network: {
        vpcId: "vpc-production",
        internetGatewayId: "igw-production",
        subnets: ["a", "c", "d"].map((az, i) => ({
          availabilityZone: `ap-northeast-1${az}`,
          cidrBlock: `10.27.${32 + i * 4}.0/22`,
        })),
      },
      desiredCount: 3,
    });
    await new Promise<void>((resolve) =>
      result.deployments.apply((value) => {
        deployment = value[0];
        resolve();
      })
    );
  });
  expect(
    resources.filter((r) =>
      r.type === "aws:ecs/service:Service" ||
      r.type === "aws:ecs/taskDefinition:TaskDefinition"
    ),
  ).toHaveLength(0);
  const subnets = resources.filter((r) => r.type === "aws:ec2/subnet:Subnet");
  expect(subnets).toHaveLength(3);
  expect(subnets.every((r) => r.inputs.vpcId === "vpc-production")).toBe(true);
  const tokenPolicy = resources.find((r) =>
    r.name === "production-supervisor-token"
  )!;
  expect(JSON.parse(tokenPolicy.inputs.policy).Statement).toEqual([{
    Effect: "Allow",
    Action: ["secretsmanager:GetSecretValue"],
    Resource:
      "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:portal-production/application-abcdef",
  }]);
  const container = deployment.taskDefinition.containerDefinitions[0];
  expect(deployment.taskDefinition.runtimePlatform.cpuArchitecture).toBe(
    "ARM64",
  );
  expect(deployment.taskDefinition.cpu).toBe("4096");
  expect(deployment.taskDefinition.memory).toBe("8192");
  expect(container.secrets[0].valueFrom).toMatch(
    /portal-production\/application-abcdef:ISUXPORTAL_BENCH_TOKEN::$/,
  );
  expect(
    Object.fromEntries(
      container.environment.map((e) => [e.name, e.value]),
    ),
  ).toMatchObject({
    ISUXPORTAL_SUPERVISOR_ENDPOINT_URL: "https://portal.isucon.net",
    ISUXPORTAL_SUPERVISOR_INSTANCE_NAME: "",
    ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT: "310",
    ISUXBENCH_IMAGES: "/opt/isucon/photos/merged.zip",
  });
  expect(container.command[2]).toContain("Router registration required");
  expect(container.command[2]).toContain("--timeout 300s");
  expect(deployment.serviceDefinition.desiredCount).toBe(3);
  // ECS rejects AZ rebalancing when deployment maximumPercent is 100.
  expect(deployment.serviceDefinition.availabilityZoneRebalancing).toBe(
    "DISABLED",
  );
  expect(
    deployment.serviceDefinition.networkConfiguration.awsvpcConfiguration
      .assignPublicIp,
  ).toBe("ENABLED");
  expect(
    deployment.serviceDefinition.networkConfiguration.awsvpcConfiguration
      .subnets,
  ).toHaveLength(3);
  const logs = resources.find((r) =>
    r.type === "aws:cloudwatch/logGroup:LogGroup"
  )!;
  expect(logs.inputs.retentionInDays).toBe(0);
  expect(logs.inputs.skipDestroy).toBe(true);
});
