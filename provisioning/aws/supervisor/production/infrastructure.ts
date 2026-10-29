import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";
import { supervisorContainer } from "../../lib/supervisor";
import { createPoolNetwork, type PoolNetworkConfig } from "../pool/network";

export interface ProductionSupervisorConfig {
  name: string;
  region: string;
  endpoint: string;
  image: string;
  tokenSecretArn: string;
  network: PoolNetworkConfig;
  desiredCount: number;
}

export function createProductionSupervisor(c: ProductionSupervisorConfig) {
  if (!/^[a-z][a-z0-9-]+$/.test(c.name)) throw new Error("invalid name");
  if (!Number.isInteger(c.desiredCount) || c.desiredCount < 1) {
    throw new Error("desiredCount must be positive");
  }
  if (
    c.network.subnets.length !== 3 ||
    new Set(c.network.subnets.map((s) => s.availabilityZone)).size !== 3
  ) {
    throw new Error("three distinct availability zones are required");
  }
  const containerConfig = {
    ...c,
    availabilityZone: c.network.subnets[0].availabilityZone,
    instanceName: "production",
    benchmarkScale: "large",
    benchmarkImages: "/opt/isucon/photos/merged.zip",
  };
  supervisorContainer(containerConfig, "validation");
  const tags = {
    Project: "isucon2026",
    Environment: "production",
    ManagedBy: "pulumi",
  };
  const subnetIds = createPoolNetwork(c.network);
  const sg = new aws.ec2.SecurityGroup("production-supervisor", {
    vpcId: c.network.vpcId,
    description: "Production benchmark workers; outbound connections only",
    ingress: [],
    egress: [{
      protocol: "-1",
      fromPort: 0,
      toPort: 0,
      cidrBlocks: ["0.0.0.0/0"],
    }],
    tags,
  });
  const execution = new aws.iam.Role("production-supervisor-execution", {
    assumeRolePolicy: aws.iam.assumeRolePolicyForPrincipal({
      Service: "ecs-tasks.amazonaws.com",
    }),
    tags,
  });
  new aws.iam.RolePolicyAttachment("production-supervisor-execution", {
    role: execution.name,
    policyArn: aws.iam.ManagedPolicy.AmazonECSTaskExecutionRolePolicy,
  });
  new aws.iam.RolePolicy("production-supervisor-token", {
    role: execution.id,
    policy: JSON.stringify({
      Version: "2012-10-17",
      Statement: [{
        Effect: "Allow",
        Action: ["secretsmanager:GetSecretValue"],
        Resource: c.tokenSecretArn,
      }],
    }),
  });
  const logs = new aws.cloudwatch.LogGroup("production-supervisor", {
    name: `/ecs/${c.name}`,
    retentionInDays: 0,
    skipDestroy: true,
    tags,
  });
  const cluster = new aws.ecs.Cluster("production-supervisor", {
    name: c.name,
    settings: [{ name: "containerInsights", value: "enhanced" }],
    tags,
  });
  const deployments = pulumi.all([
    execution.arn,
    logs.name,
    cluster.arn,
    sg.id,
    pulumi.all(subnetIds),
  ]).apply(
    ([executionRoleArn, logGroup, clusterArn, securityGroup, subnets]) => {
      const container = supervisorContainer(containerConfig, logGroup);
      container.environment = container.environment.map((entry) => {
        if (entry.name === "ISUXPORTAL_SUPERVISOR_INSTANCE_NAME") {
          return { ...entry, value: "" };
        }
        if (entry.name === "ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT") {
          return { ...entry, value: "310" };
        }
        return entry;
      });
      container.command = [
        "/bin/sh",
        "-c",
        'set -eu\nif [ "${1:-}" != "--host" ] || [ -z "${2:-}" ]; then echo "Router registration required" >&2; exit 2; fi\nexec /usr/local/bin/isureal-bench "$@" --target-port 8080 --scale large --timeout 300s\n',
        "bench-proxy",
      ];
      return [{
        taskDefinition: {
          family: c.name,
          cpu: "4096",
          memory: "8192",
          networkMode: "awsvpc",
          requiresCompatibilities: ["FARGATE"],
          runtimePlatform: {
            cpuArchitecture: "ARM64",
            operatingSystemFamily: "LINUX",
          },
          executionRoleArn,
          containerDefinitions: [container],
        },
        serviceDefinition: {
          desiredCount: c.desiredCount,
          propagateTags: "NONE",
          healthCheckGracePeriodSeconds: 0,
          launchType: "FARGATE",
          platformVersion: "1.4.0",
          // Stop-before-start deployments cannot use ECS AZ rebalancing.
          availabilityZoneRebalancing: "DISABLED",
          deploymentConfiguration: {
            minimumHealthyPercent: 0,
            maximumPercent: 100,
            deploymentCircuitBreaker: { enable: true, rollback: true },
          },
          networkConfiguration: {
            awsvpcConfiguration: {
              subnets,
              securityGroups: [securityGroup],
              assignPublicIp: "ENABLED",
            },
          },
        },
        ecspressoConfig: {
          region: c.region,
          cluster: clusterArn,
          service: c.name,
          task_definition: "task-definition.json",
          service_definition: "service-definition.json",
          timeout: "10m",
        },
      }];
    },
  );
  return {
    deployments,
    subnetIds,
    clusterName: cluster.name,
    logGroupName: logs.name,
  };
}
