import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";
import { isIPv4 } from "node:net";

export interface SupervisorConfig {
  detachedRouterHosts?: string[];
  routerNetworkInterfaces?: Record<string, string>;
  retainDedicatedNetwork?: boolean;
  sharedNetwork?: { vpcId: string; subnetId: string; egressPublicIp: string };
  benchmarkScale?: string;
  benchmarkImages?: string;
  availabilityZone: string;
  image: string;
  teamImages?: Record<string, string>;
  endpoint: string;
  teamEndpoints?: Record<string, string>;
  tokenParameterArn?: string;
  tokenSecretArn?: string;
  teamId?: string;
  teamIds?: string[];
  desiredCount?: number;
  routerSecurityGroupIds?: string[];
  instanceName: string;
  region: string;
  tokenKmsKeyArn?: string;
  targetOverrides?: Record<string, { target: string; host: string }>;
}

export function supervisorContainer(c: SupervisorConfig, logGroup: string) {
  const image = c.teamId === undefined
    ? c.image
    : c.teamImages?.[c.teamId] ?? c.image;
  if (!/^[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}$/.test(image)) {
    throw new Error("image must be pinned by digest");
  }
  const endpointUrl = c.teamId === undefined
    ? c.endpoint
    : c.teamEndpoints?.[c.teamId] ?? c.endpoint;
  const endpoint = new URL(endpointUrl);
  if (
    endpoint.protocol !== "https:" || endpoint.username || endpoint.password
  ) {
    throw new Error("endpoint must be an HTTPS URL without credentials");
  }
  if (
    c.teamId !== undefined &&
    (!/^[1-9][0-9]*$/.test(c.teamId) || !Number.isSafeInteger(Number(c.teamId)))
  ) {
    throw new Error("teamId must be a positive safe integer");
  }
  if (!/^[a-zA-Z0-9_-]+$/.test(c.instanceName)) {
    throw new Error("invalid instanceName");
  }
  if (
    !/^[a-z]{2}-[a-z]+-[0-9]+$/.test(c.region) ||
    !c.availabilityZone.startsWith(c.region)
  ) {
    throw new Error("availabilityZone must belong to region");
  }
  if (Boolean(c.tokenParameterArn) === Boolean(c.tokenSecretArn)) {
    throw new Error(
      "configure exactly one tokenParameterArn or tokenSecretArn",
    );
  }
  if (
    c.tokenParameterArn !== undefined &&
    !/^arn:aws:ssm:[a-z0-9-]+:[0-9]{12}:parameter\/[a-zA-Z0-9_./-]+$/.test(
      c.tokenParameterArn,
    )
  ) {
    throw new Error(
      "tokenParameterArn must identify an SSM SecureString parameter",
    );
  }
  if (
    c.tokenSecretArn !== undefined &&
    !/^arn:aws:secretsmanager:[a-z0-9-]+:[0-9]{12}:secret:[a-zA-Z0-9_./+=@-]+$/
      .test(c.tokenSecretArn)
  ) {
    throw new Error(
      "tokenSecretArn must identify the portal application secret",
    );
  }
  if (
    c.benchmarkScale !== undefined &&
    !["small", "large"].includes(c.benchmarkScale)
  ) {
    throw new Error("benchmarkScale must be small or large");
  }
  if (
    c.benchmarkImages !== undefined &&
    !/^(\/[a-zA-Z0-9._\/-]+|https:\/\/[a-zA-Z0-9._~:\/?#@!$&'()*+,;=%-]+)$/.test(
      c.benchmarkImages,
    )
  ) {
    throw new Error(
      "benchmarkImages must be an absolute path in the image or an HTTPS URL",
    );
  }
  const scaleArgs = c.benchmarkScale === undefined
    ? []
    : ["--scale", c.benchmarkScale];
  let command = [
    "/usr/local/bin/isureal-bench",
    "--target-port",
    "8080",
    ...scaleArgs,
  ];
  if (c.targetOverrides !== undefined) {
    const cases = Object.entries(c.targetOverrides).map(([address, route]) => {
      if (
        !isIPv4(address) || !isIPv4(route.target) ||
        !/^[a-zA-Z0-9.-]+$/.test(route.host)
      ) {
        throw new Error(
          "targetOverrides must map IPv4 addresses to Router IPv4 and Host",
        );
      }
      return `  '${address}') exec /usr/local/bin/isureal-bench "$@" --target '${route.target}' --host '${route.host}' --target-port 8080${
        c.benchmarkScale === undefined ? "" : ` --scale ${c.benchmarkScale}`
      } ;;`;
    });
    const script = `set -eu
case "$ISUXBENCH_TARGET" in
${cases.join("\n")}
  *) echo 'No Router route for the selected app' >&2; exit 2 ;;
esac`;
    command = ["/bin/sh", "-c", script, "bench-proxy"];
  }
  return {
    name: "supervisor",
    image,
    essential: true,
    command,
    environment: [
      { name: "ISUXPORTAL_SUPERVISOR_ENDPOINT_URL", value: endpointUrl },
      ...(c.teamId === undefined
        ? []
        : [{ name: "ISUXPORTAL_SUPERVISOR_TEAM_ID", value: c.teamId }]),
      { name: "ISUXPORTAL_SUPERVISOR_INSTANCE_NAME", value: c.instanceName },
      { name: "ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT", value: "210" },
      {
        name: "ISUXPORTAL_SUPERVISOR_LOG_DIRECTORY",
        value: "/var/log/isuxportal-supervisor",
      },
      // The benchmark child process inherits this and loads the photo archive
      // for posts from it (bench -images). Unset, it posts small generated images.
      ...(c.benchmarkImages === undefined
        ? []
        : [{ name: "ISUXBENCH_IMAGES", value: c.benchmarkImages }]),
    ],
    secrets: [{
      name: "ISUXPORTAL_SUPERVISOR_TOKEN",
      valueFrom: c.tokenParameterArn ??
        `${c.tokenSecretArn}:ISUXPORTAL_BENCH_TOKEN::`,
    }],
    linuxParameters: { initProcessEnabled: true },
    stopTimeout: 120,
    ulimits: ["nofile", "nproc"].map((name) => ({
      name,
      softLimit: 100000,
      hardLimit: 100000,
    })),
    logConfiguration: {
      logDriver: "awslogs",
      options: {
        "awslogs-group": logGroup,
        "awslogs-region": c.region,
        "awslogs-stream-prefix": "supervisor",
      },
    },
  };
}

export function routerTargetOverrides(
  overrides: Record<string, { target: string; host: string }>,
  routerPublicIps: Record<string, string>,
) {
  return Object.fromEntries(
    Object.entries(overrides).map((
      [address, route],
    ) => [address, {
      ...route,
      target: routerPublicIps[route.host] ?? route.target,
    }]),
  );
}

export function createSupervisor(c: SupervisorConfig) {
  // Validate before registering any resources.
  supervisorContainer(c, "validation");
  const tags = {
    Project: "isucon2026",
    Environment: pulumi.getStack(),
    ManagedBy: "pulumi",
  };
  if (
    c.sharedNetwork &&
    (!/^vpc-[a-zA-Z0-9-]+$/.test(c.sharedNetwork.vpcId) ||
      !/^subnet-[a-zA-Z0-9-]+$/.test(c.sharedNetwork.subnetId) ||
      !isIPv4(c.sharedNetwork.egressPublicIp))
  ) {
    throw new Error(
      "sharedNetwork requires a VPC, private subnet and IPv4 NAT egress",
    );
  }
  const dedicated = !c.sharedNetwork || c.retainDedicatedNetwork
    ? createDedicatedNetwork(c, tags)
    : undefined;
  const network = c.sharedNetwork
    ? { ...c.sharedNetwork, dependencies: [] }
    : dedicated!;
  const sg = new aws.ec2.SecurityGroup("supervisor-sg", {
    vpcId: network.vpcId,
    ingress: [],
    egress: [{
      protocol: "-1",
      fromPort: 0,
      toPort: 0,
      cidrBlocks: ["0.0.0.0/0"],
    }],
    tags,
  });
  const execution = new aws.iam.Role("supervisor-execution", {
    assumeRolePolicy: aws.iam.assumeRolePolicyForPrincipal({
      Service: "ecs-tasks.amazonaws.com",
    }),
    tags,
  });
  const executionPolicy = new aws.iam.RolePolicyAttachment(
    "supervisor-execution",
    {
      role: execution.name,
      policyArn: aws.iam.ManagedPolicy.AmazonECSTaskExecutionRolePolicy,
    },
  );
  const token = new aws.iam.RolePolicy("supervisor-token", {
    role: execution.id,
    policy: JSON.stringify({
      Version: "2012-10-17",
      Statement: [
        {
          Effect: "Allow",
          Action: [
            c.tokenParameterArn
              ? "ssm:GetParameters"
              : "secretsmanager:GetSecretValue",
          ],
          Resource: c.tokenParameterArn ?? c.tokenSecretArn,
        },
        ...(c.tokenKmsKeyArn
          ? [{
            Effect: "Allow",
            Action: ["kms:Decrypt"],
            Resource: c.tokenKmsKeyArn,
            Condition: {
              StringEquals: {
                "kms:ViaService": `${
                  c.tokenParameterArn ? "ssm" : "secretsmanager"
                }.${c.region}.amazonaws.com`,
                ...(c.tokenParameterArn
                  ? {
                    "kms:EncryptionContext:PARAMETER_ARN": c.tokenParameterArn,
                  }
                  : { "kms:EncryptionContext:SecretARN": c.tokenSecretArn }),
              },
            },
          }]
          : []),
      ],
    }),
  });
  const logs = new aws.cloudwatch.LogGroup("supervisor-logs", {
    retentionInDays: 0,
    skipDestroy: true,
    tags,
  });
  const cluster = new aws.ecs.Cluster("supervisor-cluster", { tags });
  const repository = new aws.ecr.Repository("supervisor-images", {
    name: `isucon2026-supervisor-${pulumi.getStack()}`,
    imageTagMutability: "IMMUTABLE",
    imageScanningConfiguration: { scanOnPush: true },
    tags,
  });
  const routerRules = (c.routerSecurityGroupIds ?? []).map((groupId, index) =>
    new aws.ec2.SecurityGroupRule(`supervisor-to-router-${index}`, {
      type: "ingress",
      securityGroupId: groupId,
      protocol: "tcp",
      fromPort: 8080,
      toPort: 8080,
      cidrBlocks: [pulumi.interpolate`${network.egressPublicIp}/32`],
      description: "Trial Supervisor via fixed NAT egress",
    })
  );
  const routerAddresses: Record<string, pulumi.Output<string>> = {};
  const routerAssociations: aws.ec2.EipAssociation[] = [];
  for (
    const [host, networkInterfaceId] of Object.entries(
      c.routerNetworkInterfaces ?? {},
    )
  ) {
    if (
      !/^[a-zA-Z0-9.-]+$/.test(host) ||
      !/^eni-[a-f0-9]+$/.test(networkInterfaceId)
    ) throw new Error("routerNetworkInterfaces must map safe Hosts to ENI IDs");
    const address = new aws.ec2.Eip(`router-${host}`, { domain: "vpc", tags });
    if (!c.detachedRouterHosts?.includes(host)) {
      routerAssociations.push(
        new aws.ec2.EipAssociation(`router-${host}`, {
          allocationId: address.id,
          networkInterfaceId,
          allowReassociation: false,
        }),
      );
    }
    routerAddresses[host] = address.publicIp;
  }
  const routerPublicIps = pulumi.all(routerAddresses);
  const workers = (c.teamIds ?? [c.teamId]).map((teamId) => {
    const suffix = teamId === undefined ? "" : `-${teamId}`;
    const workerConfig = { ...c, teamId };
    supervisorContainer(workerConfig, "validation");
    const task = new aws.ecs.TaskDefinition(`supervisor-task${suffix}`, {
      family: `isucon2026-supervisor-${pulumi.getStack()}${suffix}`,
      cpu: "4096",
      memory: "8192",
      networkMode: "awsvpc",
      requiresCompatibilities: ["FARGATE"],
      runtimePlatform: {
        cpuArchitecture: "X86_64",
        operatingSystemFamily: "LINUX",
      },
      executionRoleArn: execution.arn,
      containerDefinitions: pulumi.all([logs.name, routerPublicIps]).apply((
        [name, addresses],
      ) =>
        JSON.stringify([
          supervisorContainer({
            ...workerConfig,
            targetOverrides: c.targetOverrides === undefined
              ? undefined
              : routerTargetOverrides(c.targetOverrides, addresses),
          }, name),
        ])
      ),
      tags,
    });
    const service = new aws.ecs.Service(`supervisor-service${suffix}`, {
      cluster: cluster.arn,
      taskDefinition: task.arn,
      desiredCount: c.desiredCount ?? 1,
      launchType: "FARGATE",
      platformVersion: "1.4.0",
      // Stop the old worker before starting its replacement for the same team/AZ.
      deploymentMinimumHealthyPercent: 0,
      deploymentMaximumPercent: 100,
      deploymentCircuitBreaker: { enable: true, rollback: true },
      networkConfiguration: {
        subnets: [network.subnetId],
        securityGroups: [sg.id],
        assignPublicIp: false,
      },
      tags,
    }, {
      dependsOn: [
        ...network.dependencies,
        executionPolicy,
        token,
        ...routerRules,
      ],
    });
    return service;
  });
  return {
    serviceName: workers[0].name,
    serviceNames: workers.map((worker) => worker.name),
    routerPublicIps,
    repositoryUrl: repository.repositoryUrl,
    clusterName: cluster.name,
    logGroupName: logs.name,
    egressPublicAddress: pulumi.output(network.egressPublicIp),
  };
}

function createDedicatedNetwork(
  c: SupervisorConfig,
  tags: Record<string, string>,
) {
  const vpc = new aws.ec2.Vpc("supervisor-vpc", {
    cidrBlock: "10.80.0.0/24",
    enableDnsSupport: true,
    enableDnsHostnames: true,
    tags,
  });
  const publicSubnet = new aws.ec2.Subnet("supervisor-public", {
    vpcId: vpc.id,
    cidrBlock: "10.80.0.0/28",
    availabilityZone: c.availabilityZone,
    tags,
  });
  const privateSubnet = new aws.ec2.Subnet("supervisor-private", {
    vpcId: vpc.id,
    cidrBlock: "10.80.0.16/28",
    availabilityZone: c.availabilityZone,
    tags,
  });
  const gateway = new aws.ec2.InternetGateway("supervisor-igw", {
    vpcId: vpc.id,
    tags,
  });
  const publicRoutes = new aws.ec2.RouteTable("supervisor-public-routes", {
    vpcId: vpc.id,
    routes: [{ cidrBlock: "0.0.0.0/0", gatewayId: gateway.id }],
    tags,
  });
  const publicAssociation = new aws.ec2.RouteTableAssociation(
    "supervisor-public-routes",
    { subnetId: publicSubnet.id, routeTableId: publicRoutes.id },
  );
  const address = new aws.ec2.Eip("supervisor-egress", { domain: "vpc", tags });
  const nat = new aws.ec2.NatGateway("supervisor-nat", {
    allocationId: address.id,
    subnetId: publicSubnet.id,
    tags,
  }, { dependsOn: [gateway, publicAssociation] });
  const privateRoutes = new aws.ec2.RouteTable("supervisor-private-routes", {
    vpcId: vpc.id,
    routes: [{ cidrBlock: "0.0.0.0/0", natGatewayId: nat.id }],
    tags,
  });
  const privateAssociation = new aws.ec2.RouteTableAssociation(
    "supervisor-private-routes",
    { subnetId: privateSubnet.id, routeTableId: privateRoutes.id },
  );
  return {
    vpcId: vpc.id,
    subnetId: privateSubnet.id,
    egressPublicIp: address.publicIp,
    dependencies: [privateAssociation],
  };
}
