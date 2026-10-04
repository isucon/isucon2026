import * as aws from "@pulumi/aws";
import * as pulumi from "@pulumi/pulumi";
import { PortalConfig, processes } from "./config";

export function createPortal(c: PortalConfig) {
  const tags = {
    Project: "isucon2026-portal",
    Environment: c.name,
    ManagedBy: "pulumi",
  };
  const provider = new aws.Provider("portal", {
    region: c.region as aws.Region,
    defaultTags: { tags },
  });
  const opts = { provider };
  const account = aws.getCallerIdentityOutput({}, opts).accountId;
  const vpc = new aws.ec2.Vpc("portal", {
    cidrBlock: c.vpcCidr,
    enableDnsSupport: true,
    enableDnsHostnames: true,
  }, opts);
  const igw = new aws.ec2.InternetGateway("portal", { vpcId: vpc.id }, opts);
  const cidrPrefix = c.vpcCidr.split(".").slice(0, 2).join(".");
  const publicSubnets = c.availabilityZones.map((az, i) =>
    new aws.ec2.Subnet(`public-${i}`, {
      vpcId: vpc.id,
      availabilityZone: az,
      cidrBlock: `${cidrPrefix}.${i}.0/24`,
    }, opts)
  );
  const privateSubnets = c.availabilityZones.map((az, i) =>
    new aws.ec2.Subnet(`private-${i}`, {
      vpcId: vpc.id,
      availabilityZone: az,
      cidrBlock: `${cidrPrefix}.${i + 10}.0/24`,
    }, opts)
  );
  const publicRoutes = new aws.ec2.RouteTable("public", {
    vpcId: vpc.id,
    routes: [{ cidrBlock: "0.0.0.0/0", gatewayId: igw.id }],
  }, opts);
  publicSubnets.forEach((subnet, i) => {
    new aws.ec2.RouteTableAssociation(`public-${i}`, {
      subnetId: subnet.id,
      routeTableId: publicRoutes.id,
    }, opts);
  });
  const nats = publicSubnets.slice(0, c.natGatewayCount).map((subnet, i) => {
    const eip = new aws.ec2.Eip(`nat-${i}`, { domain: "vpc" }, opts);
    const nat = new aws.ec2.NatGateway(`portal-${i}`, {
      subnetId: subnet.id,
      allocationId: eip.id,
    }, { ...opts, dependsOn: [igw] });
    return { nat, eip };
  });
  privateSubnets.forEach((subnet, i) => {
    const routes = new aws.ec2.RouteTable(`private-${i}`, {
      vpcId: vpc.id,
      routes: [{
        cidrBlock: "0.0.0.0/0",
        natGatewayId: nats[i % nats.length].nat.id,
      }],
    }, opts);
    new aws.ec2.RouteTableAssociation(`private-${i}`, {
      subnetId: subnet.id,
      routeTableId: routes.id,
    }, opts);
  });
  const natIps = nats.map(({ eip }) => eip.publicIp);
  // Keep all ALB rules standalone; mixing inline and standalone rules causes drift.
  const albSg = new aws.ec2.SecurityGroup("alb", { vpcId: vpc.id }, opts);
  for (const port of [80, 443]) {
    new aws.ec2.SecurityGroupRule(`alb-ingress-${port}`, {
      type: "ingress",
      securityGroupId: albSg.id,
      protocol: "tcp",
      fromPort: port,
      toPort: port,
      cidrBlocks: ["0.0.0.0/0"],
    }, opts);
  }
  const tasksSg = new aws.ec2.SecurityGroup("tasks", {
    vpcId: vpc.id,
    ingress: [{
      protocol: "tcp",
      fromPort: 8081,
      toPort: 8081,
      securityGroups: [albSg.id],
    }],
    egress: [{
      protocol: "-1",
      fromPort: 0,
      toPort: 0,
      cidrBlocks: ["0.0.0.0/0"],
    }],
  }, opts);
  new aws.ec2.SecurityGroupRule("alb-egress", {
    type: "egress",
    securityGroupId: albSg.id,
    sourceSecurityGroupId: tasksSg.id,
    protocol: "tcp",
    fromPort: 8081,
    toPort: 8081,
  }, opts);
  const dataSg = new aws.ec2.SecurityGroup("data", {
    vpcId: vpc.id,
    ingress: [5432, 6379].map((port) => ({
      protocol: "tcp",
      fromPort: port,
      toPort: port,
      securityGroups: [tasksSg.id],
    })),
    egress: [],
  }, opts);
  const subnetIds = privateSubnets.map((s) => s.id);
  const dbSubnets = new aws.rds.SubnetGroup("portal", { subnetIds }, opts);
  const database = new aws.rds.Cluster("portal", {
    clusterIdentifier: c.name,
    engine: "aurora-postgresql",
    engineMode: "provisioned",
    engineVersion: c.auroraEngineVersion,
    databaseName: "isuxportal",
    masterUsername: "isuxportal",
    manageMasterUserPassword: true,
    storageEncrypted: true,
    dbSubnetGroupName: dbSubnets.name,
    vpcSecurityGroupIds: [dataSg.id],
    backupRetentionPeriod: 14,
    preferredBackupWindow: "18:00-19:00",
    preferredMaintenanceWindow: "sun:19:00-sun:20:00",
    deletionProtection: true,
    skipFinalSnapshot: false,
    finalSnapshotIdentifier: `${c.name}-final`,
    copyTagsToSnapshot: true,
    enabledCloudwatchLogsExports: ["postgresql"],
    serverlessv2ScalingConfiguration: {
      minCapacity: c.auroraMinAcu,
      maxCapacity: c.auroraMaxAcu,
    },
  }, { ...opts, protect: true });
  const databaseInstances = c.availabilityZones.map((az, i) =>
    new aws.rds.ClusterInstance(`portal-${i}`, {
      identifier: `${c.name}-${i + 1}`,
      clusterIdentifier: database.id,
      instanceClass: "db.serverless",
      engine: "aurora-postgresql",
      engineVersion: database.engineVersion,
      availabilityZone: az,
      dbSubnetGroupName: dbSubnets.name,
      publiclyAccessible: false,
      promotionTier: i,
      autoMinorVersionUpgrade: false,
      performanceInsightsEnabled: true,
    }, opts)
  );
  const databaseSecretArn = database.masterUserSecrets.apply((secrets) =>
    secrets[0].secretArn
  );
  const cacheSubnets = new aws.elasticache.SubnetGroup(
    "portal",
    { subnetIds },
    opts,
  );
  const cache = new aws.elasticache.ReplicationGroup("portal", {
    replicationGroupId: c.name,
    description: "Portal sessions and dashboard cache",
    engine: "valkey",
    engineVersion: "8.2",
    parameterGroupName: "default.valkey8",
    nodeType: c.cacheNodeType,
    numCacheClusters: 2,
    automaticFailoverEnabled: true,
    multiAzEnabled: true,
    preferredCacheClusterAzs: c.availabilityZones,
    atRestEncryptionEnabled: true,
    transitEncryptionEnabled: true,
    subnetGroupName: cacheSubnets.name,
    securityGroupIds: [dataSg.id],
    snapshotRetentionLimit: 7,
    snapshotWindow: "18:00-19:00",
    maintenanceWindow: "sun:19:00-sun:20:00",
    autoMinorVersionUpgrade: true,
  }, opts);
  const application = new aws.secretsmanager.Secret("application", {
    name: `${c.name}/application`,
    description: "JSON credentials populated outside Pulumi",
    recoveryWindowInDays: 30,
  }, opts);
  const ssh = new aws.secretsmanager.Secret("ssh", {
    name: `${c.name}/admin-ssh-key`,
    description: "Raw PEM SSH key; worker only",
    recoveryWindowInDays: 30,
  }, opts);
  function bucket(name: string, publicRead: boolean) {
    const resource = new aws.s3.Bucket(name, {
      bucket: pulumi.interpolate`${c.name}-${name}-${account}`,
    }, opts);
    const block = new aws.s3.BucketPublicAccessBlock(name, {
      bucket: resource.id,
      blockPublicAcls: true,
      ignorePublicAcls: true,
      blockPublicPolicy: !publicRead,
      restrictPublicBuckets: !publicRead,
    }, opts);
    new aws.s3.BucketVersioning(name, {
      bucket: resource.id,
      versioningConfiguration: { status: "Enabled" },
    }, opts);
    new aws.s3.BucketServerSideEncryptionConfiguration(name, {
      bucket: resource.id,
      rules: [{
        applyServerSideEncryptionByDefault: { sseAlgorithm: "AES256" },
      }],
    }, opts);
    new aws.s3.BucketOwnershipControls(name, {
      bucket: resource.id,
      rule: { objectOwnership: "BucketOwnerEnforced" },
    }, opts);
    new aws.s3.BucketPolicy(name, {
      bucket: resource.id,
      policy: pulumi.jsonStringify({
        Version: "2012-10-17",
        Statement: [
          {
            Effect: "Deny",
            Principal: "*",
            Action: "s3:*",
            Resource: [resource.arn, pulumi.interpolate`${resource.arn}/*`],
            Condition: { Bool: { "aws:SecureTransport": "false" } },
          },
          ...(publicRead
            ? [{
              Effect: "Allow",
              Principal: "*",
              Action: "s3:GetObject",
              Resource: pulumi.interpolate`${resource.arn}/avatars/*`,
            }]
            : []),
        ],
      }),
    }, { ...opts, dependsOn: [block] });
    return resource;
  }
  const avatars = bucket("avatars", true);
  const configBucket = bucket("config", false);
  new aws.s3.BucketCorsConfiguration("avatars", {
    bucket: avatars.id,
    corsRules: [{
      allowedHeaders: ["*"],
      allowedMethods: ["PUT", "GET", "HEAD"],
      allowedOrigins: [`https://${c.domainName}`],
      exposeHeaders: ["ETag"],
      maxAgeSeconds: 600,
    }],
  }, opts);
  const certificate = new aws.acm.Certificate("portal", {
    domainName: c.domainName,
    validationMethod: "DNS",
  }, opts);
  const validation = certificate.domainValidationOptions.apply((options) =>
    options[0]
  );
  const validationRecord = new aws.route53.Record("certificate", {
    zoneId: c.route53ZoneId,
    name: validation.resourceRecordName,
    type: validation.resourceRecordType,
    records: [validation.resourceRecordValue],
    ttl: 60,
  }, opts);
  const validatedCertificate = new aws.acm.CertificateValidation("portal", {
    certificateArn: certificate.arn,
    validationRecordFqdns: [validationRecord.fqdn],
  }, opts);
  const alb = new aws.lb.LoadBalancer("portal", {
    name: c.name,
    loadBalancerType: "application",
    subnets: publicSubnets.map((s) => s.id),
    securityGroups: [albSg.id],
    enableDeletionProtection: true,
    dropInvalidHeaderFields: true,
  }, opts);
  const target = new aws.lb.TargetGroup("portal", {
    name: c.name,
    vpcId: vpc.id,
    targetType: "ip",
    port: 8081,
    protocol: "HTTP",
    deregistrationDelay: 30,
    healthCheck: {
      path: "/site/sha",
      matcher: "200",
      interval: 15,
      healthyThreshold: 2,
      unhealthyThreshold: 3,
    },
  }, opts);
  new aws.lb.Listener("http", {
    loadBalancerArn: alb.arn,
    port: 80,
    protocol: "HTTP",
    defaultActions: [{
      type: "redirect",
      redirect: { port: "443", protocol: "HTTPS", statusCode: "HTTP_301" },
    }],
  }, opts);
  new aws.lb.Listener("https", {
    loadBalancerArn: alb.arn,
    port: 443,
    protocol: "HTTPS",
    sslPolicy: "ELBSecurityPolicy-TLS13-1-2-2021-06",
    certificateArn: validatedCertificate.certificateArn,
    defaultActions: [{ type: "forward", targetGroupArn: target.arn }],
  }, opts);
  new aws.route53.Record("portal", {
    zoneId: c.route53ZoneId,
    name: c.domainName,
    type: "A",
    aliases: [{
      name: alb.dnsName,
      zoneId: alb.zoneId,
      evaluateTargetHealth: true,
    }],
  }, opts);
  const cluster = new aws.ecs.Cluster("portal", {
    name: c.name,
    settings: [{ name: "containerInsights", value: "enabled" }],
  }, opts);
  const repository = new aws.ecr.Repository("portal", {
    name: c.name,
    imageTagMutability: "IMMUTABLE",
    imageScanningConfiguration: { scanOnPush: true },
  }, opts);
  const logs = new aws.cloudwatch.LogGroup("portal", {
    name: `/ecs/${c.name}`,
    retentionInDays: 30,
  }, opts);
  const assume = pulumi.jsonStringify({
    Version: "2012-10-17",
    Statement: [{
      Effect: "Allow",
      Principal: { Service: "ecs-tasks.amazonaws.com" },
      Action: "sts:AssumeRole",
      Condition: { StringEquals: { "aws:SourceAccount": account } },
    }],
  });
  const executionRoles: Record<string, pulumi.Output<string>> = {};
  const taskRoles: Record<string, pulumi.Output<string>> = {};
  for (const name of processes) {
    const execution = new aws.iam.Role(`${name}-exec`, {
      name: `${c.name}-${name}-exec`,
      assumeRolePolicy: assume,
    }, opts);
    new aws.iam.RolePolicyAttachment(`${name}-exec`, {
      role: execution.name,
      policyArn: aws.iam.ManagedPolicy.AmazonECSTaskExecutionRolePolicy,
    }, opts);
    new aws.iam.RolePolicy(`${name}-secrets`, {
      role: execution.id,
      policy: pulumi.jsonStringify({
        Version: "2012-10-17",
        Statement: [{
          Effect: "Allow",
          Action: ["secretsmanager:GetSecretValue"],
          Resource: [
            databaseSecretArn,
            ...(name === "migrate" ? [] : [application.arn]),
            ...(name === "worker" ? [ssh.arn] : []),
          ],
        }],
      }),
    }, opts);
    const task = new aws.iam.Role(name, {
      name: `${c.name}-${name}`,
      assumeRolePolicy: assume,
    }, opts);
    executionRoles[name] = execution.arn;
    taskRoles[name] = task.arn;
    if (name === "portal") {
      new aws.iam.RolePolicy("avatar-upload", {
        role: task.id,
        policy: pulumi.jsonStringify({
          Version: "2012-10-17",
          Statement: [{
            Effect: "Allow",
            Action: ["s3:PutObject"],
            Resource: pulumi.interpolate`${avatars.arn}/avatars/*`,
          }],
        }),
      }, opts);
    }
    if (name === "worker") {
      new aws.iam.RolePolicy("contest-instances", {
        role: task.id,
        policy: JSON.stringify({
          Version: "2012-10-17",
          Statement: [{
            Effect: "Allow",
            Action: ["ec2:DescribeInstances"],
            Resource: "*",
          }],
        }),
      }, opts);
    }
  }
  const deploy = new aws.iam.Role("github-deploy", {
    name: `${c.name}-github-deploy`,
    assumeRolePolicy: JSON.stringify({
      Version: "2012-10-17",
      Statement: [{
        Effect: "Allow",
        Principal: { Federated: c.githubOidcProviderArn },
        Action: "sts:AssumeRoleWithWebIdentity",
        Condition: {
          StringEquals: {
            "token.actions.githubusercontent.com:aud": "sts.amazonaws.com",
            "token.actions.githubusercontent.com:sub":
              `repo:${c.githubRepository}:environment:${c.githubEnvironment}`,
          },
        },
      }],
    }),
  }, opts);
  const ecsArn = pulumi.interpolate`arn:aws:ecs:${c.region}:${account}`;
  new aws.iam.RolePolicy("github-deploy", {
    role: deploy.id,
    policy: pulumi.jsonStringify({
      Version: "2012-10-17",
      Statement: [
        {
          Effect: "Allow",
          Action: [
            "ecr:GetAuthorizationToken",
            "application-autoscaling:DescribeScalableTargets",
            "application-autoscaling:DescribeScalingPolicies",
            "ecs:RegisterTaskDefinition",
            "ecs:DescribeTaskDefinition",
          ],
          Resource: "*",
        },
        {
          Effect: "Allow",
          Action: [
            "ecr:BatchCheckLayerAvailability",
            "ecr:InitiateLayerUpload",
            "ecr:UploadLayerPart",
            "ecr:CompleteLayerUpload",
            "ecr:PutImage",
            "ecr:BatchGetImage",
            "ecr:GetDownloadUrlForLayer",
            "ecr:DescribeImages",
          ],
          Resource: repository.arn,
        },
        {
          Effect: "Allow",
          Action: ["s3:GetObject"],
          Resource: pulumi.interpolate`${configBucket.arn}/infra.json`,
        },
        {
          Effect: "Allow",
          Action: ["ecs:RunTask"],
          Resource: pulumi
            .interpolate`${ecsArn}:task-definition/${c.name}-migrate:*`,
          Condition: { ArnEquals: { "ecs:cluster": cluster.arn } },
        },
        {
          Effect: "Allow",
          Action: ["ecs:DescribeTasks"],
          Resource: pulumi.interpolate`${ecsArn}:task/${c.name}/*`,
        },
        {
          Effect: "Allow",
          Action: [
            "ecs:CreateService",
            "ecs:UpdateService",
            "ecs:DescribeServices",
            "ecs:ListServiceDeployments",
            "ecs:ListTagsForResource",
          ],
          Resource: pulumi.interpolate`${ecsArn}:service/${c.name}/*`,
        },
        {
          Effect: "Allow",
          Action: ["ecs:DescribeServiceDeployments"],
          Resource: pulumi
            .interpolate`${ecsArn}:service-deployment/${c.name}/*`,
        },
        {
          Effect: "Allow",
          Action: ["ecs:DescribeServiceRevisions"],
          Resource: pulumi.interpolate`${ecsArn}:service-revision/${c.name}/*`,
        },
        {
          Effect: "Allow",
          Action: ["logs:GetLogEvents"],
          Resource: pulumi.interpolate`${logs.arn}:log-stream:*`,
        },
        {
          Effect: "Allow",
          Action: ["iam:PassRole"],
          Resource: [
            ...Object.values(taskRoles),
            ...Object.values(executionRoles),
          ],
          Condition: {
            StringEquals: { "iam:PassedToService": "ecs-tasks.amazonaws.com" },
          },
        },
      ],
    }),
  }, opts);
  const infra = pulumi.output({
    name: c.name,
    region: c.region,
    cluster: cluster.name,
    repository: repository.repositoryUrl,
    subnets: subnetIds,
    securityGroup: tasksSg.id,
    targetGroupArn: target.arn,
    logGroup: logs.name,
    applicationSecretArn: application.arn,
    databaseSecretArn,
    sshSecretArn: ssh.arn,
    taskRoles,
    executionRoles,
    secretKeys: c.secretKeys,
    enableBot: c.enableBot,
    environment: {
      ISUXPORTAL_ADMIN_ONLY: "1",
      ISUXPORTAL_ADMIN_LOGIN: "admin",
      ISUXPORTAL_MAX_TEAMS: "1000",
      ISUXPORTAL_CLOUDFORMATION_STAGING: "1",
      ...c.environment,
      PORTAL_LISTEN: ":8081",
      PORTAL_NOTIFYLOOP_TARGET:
        `https://${c.domainName}/admin/long_running_check`,
      DEFAULT_URL_HOST: c.domainName,
      MONITOR_ENV: c.name,
      AWS_REGION: c.region,
      ISUXPORTAL_DATABASE_URL: "postgres:///isuxportal?sslmode=verify-full",
      PGHOST: database.endpoint,
      PGSSLROOTCERT: "/etc/ssl/certs/rds-global-bundle.pem",
      REDIS_URL: pulumi
        .interpolate`rediss://${cache.primaryEndpointAddress}:6379/0`,
      ISUXPORTAL_AVATAR_BUCKET: avatars.id,
      ISUXPORTAL_AVATAR_BASE_PATH: "avatars/",
    },
  });
  new aws.s3.BucketObjectv2("ecspresso-infra", {
    bucket: configBucket.id,
    key: "infra.json",
    contentType: "application/json",
    content: pulumi.jsonStringify(infra),
  }, opts);
  const alerts = new aws.sns.Topic(
    "alerts",
    { name: `${c.name}-alerts` },
    opts,
  );
  new aws.cloudwatch.MetricAlarm("healthy-targets", {
    name: `${c.name}-healthy-targets`,
    namespace: "AWS/ApplicationELB",
    metricName: "HealthyHostCount",
    statistic: "Minimum",
    period: 60,
    evaluationPeriods: 2,
    threshold: 2,
    comparisonOperator: "LessThanThreshold",
    treatMissingData: "breaching",
    dimensions: { LoadBalancer: alb.arnSuffix, TargetGroup: target.arnSuffix },
    alarmActions: [alerts.arn],
    okActions: [alerts.arn],
  }, opts);
  new aws.cloudwatch.MetricAlarm("target-errors", {
    name: `${c.name}-target-5xx`,
    namespace: "AWS/ApplicationELB",
    metricName: "HTTPCode_Target_5XX_Count",
    statistic: "Sum",
    period: 60,
    evaluationPeriods: 2,
    threshold: 10,
    comparisonOperator: "GreaterThanOrEqualToThreshold",
    treatMissingData: "notBreaching",
    dimensions: { LoadBalancer: alb.arnSuffix },
    alarmActions: [alerts.arn],
  }, opts);
  databaseInstances.forEach((instance, i) =>
    new aws.cloudwatch.MetricAlarm(`aurora-capacity-${i}`, {
      name: `${c.name}-aurora-${i + 1}-capacity`,
      namespace: "AWS/RDS",
      metricName: "ACUUtilization",
      statistic: "Average",
      period: 60,
      evaluationPeriods: 5,
      threshold: 90,
      comparisonOperator: "GreaterThanOrEqualToThreshold",
      treatMissingData: "missing",
      dimensions: { DBInstanceIdentifier: instance.identifier },
      alarmActions: [alerts.arn],
    }, opts)
  );
  return {
    ecrRepositoryUrl: repository.repositoryUrl,
    deployRoleArn: deploy.arn,
    ecspressoConfigUri: pulumi.interpolate`s3://${configBucket.id}/infra.json`,
    applicationSecretArn: application.arn,
    sshSecretArn: ssh.arn,
    databaseSecretArn,
    databaseEndpoint: database.endpoint,
    url: `https://${c.domainName}`,
    natPublicIps: natIps,
    vpcId: vpc.id,
    privateSubnetIds: privateSubnets.map((subnet) => subnet.id),
    alertsTopicArn: alerts.arn,
  };
}
