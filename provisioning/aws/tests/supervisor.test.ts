import * as pulumi from "@pulumi/pulumi";
import { expect, test } from "vitest";
import { createSupervisor, supervisorContainer } from "../lib/supervisor";

const resources: pulumi.runtime.MockResourceArgs[] = [];
pulumi.runtime.setMocks({
  newResource: (args) => {
    resources.push(args);
    return {
      id: `${args.name}-id`,
      state: {
        ...args.inputs,
        arn: `arn:mock:${args.name}`,
        name: args.name,
        publicIp: "203.0.113.10",
      },
    };
  },
  call: (args) => args.inputs,
});
const config = {
  availabilityZone: "ap-northeast-1a",
  image: `registry.example/supervisor@sha256:${"a".repeat(64)}`,
  endpoint: "https://portal.example",
  tokenParameterArn:
    "arn:aws:ssm:ap-northeast-1:123456789012:parameter/trial/token",
  teamId: "42",
  instanceName: "apne1-az4",
  region: "ap-northeast-1",
  routerSecurityGroupIds: ["sg-router"],
};

test("preserves team-specific benchmark images during supervisor deployments", () => {
  const override = `registry.example/team42@sha256:${"b".repeat(64)}`;
  const teamImages = { "42": override };
  expect(supervisorContainer({ ...config, teamImages }, "logs").image)
    .toBe(override);
  expect(
    supervisorContainer({ ...config, teamImages, teamId: "43" }, "logs").image,
  )
    .toBe(config.image);
  expect(() =>
    supervisorContainer({
      ...config,
      teamImages: { "42": "registry.example/team42:latest" },
    }, "logs")
  ).toThrow("image must be pinned by digest");
});

test("runs a team-scoped worker with ISUCON14 process and log settings", () => {
  const container = supervisorContainer(config, "trial-logs");
  expect(container.environment).toContainEqual({
    name: "ISUXPORTAL_SUPERVISOR_TEAM_ID",
    value: "42",
  });
  expect(container.environment).toContainEqual({
    name: "ISUXPORTAL_SUPERVISOR_INSTANCE_NAME",
    value: "apne1-az4",
  });
  expect(container.secrets).toEqual([{
    name: "ISUXPORTAL_SUPERVISOR_TOKEN",
    valueFrom: config.tokenParameterArn,
  }]);
  expect(container.linuxParameters.initProcessEnabled).toBe(true);
  expect(container.stopTimeout).toBe(120);
  expect(container.ulimits).toContainEqual({
    name: "nofile",
    softLimit: 100000,
    hardLimit: 100000,
  });
  expect(container.command).toEqual([
    "/usr/local/bin/isureal-bench",
    "--target-port",
    "8080",
  ]);
  expect(container.logConfiguration.logDriver).toBe("awslogs");
});

test("rejects mutable images and invalid team or endpoint", () => {
  expect(() =>
    supervisorContainer({ ...config, image: "registry.example:latest" }, "logs")
  ).toThrow();
  expect(() =>
    supervisorContainer({ ...config, teamId: "42\nOTHER=value" }, "logs")
  ).toThrow();
  expect(() =>
    supervisorContainer(
      { ...config, endpoint: "http://portal.example" },
      "logs",
    )
  ).toThrow();
});

test("creates Fargate service with fixed egress and scoped execution-role secrets", async () => {
  await pulumi.runtime.runInPulumiStack(async () => {
    const worker = createSupervisor(config);
    await new Promise((resolve) =>
      pulumi.all([worker.serviceName, worker.egressPublicAddress]).apply(
        resolve,
      )
    );
  });
  const task = resources.find((r) =>
    r.type === "aws:ecs/taskDefinition:TaskDefinition"
  )!;
  expect(task.inputs.cpu).toBe("4096");
  expect(task.inputs.memory).toBe("8192");
  expect(task.inputs.requiresCompatibilities).toEqual(["FARGATE"]);
  expect(task.inputs.runtimePlatform.cpuArchitecture).toBe("X86_64");
  const service = resources.find((r) => r.type === "aws:ecs/service:Service")!;
  expect(service.inputs.desiredCount).toBe(1);
  expect(service.inputs.launchType).toBe("FARGATE");
  expect(service.inputs.networkConfiguration.assignPublicIp).toBe(false);
  const sg = resources.find((r) =>
    r.type === "aws:ec2/securityGroup:SecurityGroup"
  )!;
  expect(sg.inputs.ingress).toEqual([]);
  const policy = resources.find((r) =>
    r.type === "aws:iam/rolePolicy:RolePolicy"
  )!;
  const document = JSON.parse(policy.inputs.policy);
  expect(document.Statement[0].Resource).toBe(config.tokenParameterArn);
  expect(document.Statement[0].Action).toEqual(["ssm:GetParameters"]);
  const ingress = resources.find((r) =>
    r.type === "aws:ec2/securityGroupRule:SecurityGroupRule"
  )!;
  expect(ingress.inputs.securityGroupId).toBe("sg-router");
  expect(ingress.inputs.fromPort).toBe(8080);
  expect(ingress.inputs.cidrBlocks).toEqual(["203.0.113.10/32"]);
  expect(resources.some((r) => r.type === "aws:ec2/natGateway:NatGateway"))
    .toBe(true);
  expect(resources.some((r) => r.type === "aws:ec2/instance:Instance")).toBe(
    false,
  );
});

test("can use portal-dev's existing JSON secret without copying its token", () => {
  const tokenSecretArn =
    "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:portal-develop/application-abcdef";
  const { tokenParameterArn: _, teamId: __, ...rest } = config;
  const container = supervisorContainer({ ...rest, tokenSecretArn }, "logs");
  expect(container.secrets).toEqual([{
    name: "ISUXPORTAL_SUPERVISOR_TOKEN",
    valueFrom: `${tokenSecretArn}:ISUXPORTAL_BENCH_TOKEN::`,
  }]);
  expect(
    container.environment.some((item) =>
      item.name === "ISUXPORTAL_SUPERVISOR_TEAM_ID"
    ),
  ).toBe(false);
  expect(() => supervisorContainer({ ...config, tokenSecretArn }, "logs"))
    .toThrow();
});

test("routes the selected app through its Router while preserving benchmark arguments", async () => {
  const { spawnSync } = await import("node:child_process");
  const container = supervisorContainer({
    ...config,
    targetOverrides: {
      "13.231.83.243": {
        target: "54.250.237.4",
        host: "app1.team1-aws.127.0.0.1.nip.io",
      },
    },
  }, "logs");
  expect(container.command[0]).toBe("/bin/sh");
  const script = container.command[2].replace(
    "/usr/local/bin/isureal-bench",
    "/usr/bin/printf '%s\\n'",
  );
  const run = spawnSync("/bin/sh", [
    "-c",
    script,
    "bench-proxy",
    "--only-post-validation",
    "--target",
    "13.231.83.243",
  ], {
    env: { ...process.env, ISUXBENCH_TARGET: "13.231.83.243" },
    encoding: "utf8",
  });
  expect(run.status).toBe(0);
  expect(run.stdout.trim().split("\n")).toEqual([
    "--only-post-validation",
    "--target",
    "13.231.83.243",
    "--target",
    "54.250.237.4",
    "--host",
    "app1.team1-aws.127.0.0.1.nip.io",
    "--target-port",
    "8080",
  ]);
  const unmapped = spawnSync("/bin/sh", ["-c", script], {
    env: { ...process.env, ISUXBENCH_TARGET: "203.0.113.99" },
  });
  expect(unmapped.status).toBe(2);
});

test("passes the configured large dataset scale to direct and routed benchmarks", () => {
  const c = { ...config, benchmarkScale: "large" as const };
  expect(supervisorContainer(c, "logs").command).toEqual([
    "/usr/local/bin/isureal-bench",
    "--target-port",
    "8080",
    "--scale",
    "large",
  ]);
  const routed = supervisorContainer({
    ...c,
    targetOverrides: {
      "13.231.83.243": {
        target: "54.250.237.4",
        host: "app.team1-aws.127.0.0.1.nip.io",
      },
    },
  }, "logs");
  expect(routed.command[2]).toContain("--scale large");
});

test("rejects unsupported dataset scales", () => {
  expect(() =>
    supervisorContainer({ ...config, benchmarkScale: "large; exit 0" }, "logs")
  ).toThrow("benchmarkScale must be small or large");
});

test("uses Router EIPs by Host while retaining routes without a managed EIP", async () => {
  const { routerTargetOverrides } = await import("../lib/supervisor");
  const routes = {
    "203.0.113.10": { target: "198.51.100.10", host: "app.team2.example" },
    "203.0.113.11": { target: "198.51.100.11", host: "app.team1.example" },
  };
  expect(
    routerTargetOverrides(routes, { "app.team2.example": "198.51.100.20" }),
  ).toEqual({
    "203.0.113.10": { target: "198.51.100.20", host: "app.team2.example" },
    "203.0.113.11": routes["203.0.113.11"],
  });
  expect(routes["203.0.113.10"].target).toBe("198.51.100.10");
});
