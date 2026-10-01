import * as pulumi from "@pulumi/pulumi";
import { beforeAll, describe, expect, test } from "vitest";
import { createPortal } from "../infrastructure";
import { defaults, validateConfig } from "../config";

const resources: pulumi.runtime.MockResourceArgs[] = [];
pulumi.runtime.setMocks(
  {
    newResource: (args) => {
      resources.push(args);
      return {
        id: `${args.name}-id`,
        state: {
          ...args.inputs,
          arn: `arn:aws:mock:ap-northeast-1:123456789012:${args.name}`,
          endpoint: "db.example.com",
          primaryEndpointAddress: "cache.example.com",
          repositoryUrl:
            "123456789012.dkr.ecr.ap-northeast-1.amazonaws.com/portal",
          masterUserSecrets: [{
            secretArn:
              "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:db",
          }],
          domainValidationOptions: [{
            resourceRecordName: "_acm.example.com",
            resourceRecordType: "CNAME",
            resourceRecordValue: "validation.example.com",
          }],
          domainName: "alb.example.com",
          dnsName: "alb.example.com",
          zoneId: "ZALB",
          arnSuffix: args.name,
        },
      };
    },
    call: (args) =>
      args.token.includes("getCallerIdentity")
        ? { accountId: "123456789012" }
        : args.inputs,
  },
  "portal",
  "test",
  false,
);
const config = {
  ...defaults,
  name: "portal-test",
  region: "ap-northeast-1",
  domainName: "portal.example.com",
  route53ZoneId: "ZTEST",
  githubRepository: "isucon/isucon2026",
  githubEnvironment: "portal-develop",
  githubOidcProviderArn:
    "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com",
};
const ofType = (type: string) => resources.filter((r) => r.type === type);

beforeAll(async () => {
  await pulumi.runtime.runInPulumiStack(async () =>
    createPortal(validateConfig(config))
  );
});
describe("portal infrastructure", () => {
  test("keeps ECS services and task definitions under ecspresso ownership", () => {
    expect(ofType("aws:ecs/service:Service")).toHaveLength(0);
    expect(ofType("aws:ecs/taskDefinition:TaskDefinition")).toHaveLength(0);
    expect(ofType("aws:ecs/cluster:Cluster")).toHaveLength(1);
  });
  test("uses warm encrypted Aurora Serverless v2 in two AZs", () => {
    const db = ofType("aws:rds/cluster:Cluster")[0].inputs;
    expect(db).toMatchObject({
      engine: "aurora-postgresql",
      engineVersion: "17.7",
      engineMode: "provisioned",
      storageEncrypted: true,
      deletionProtection: true,
      manageMasterUserPassword: true,
      serverlessv2ScalingConfiguration: { minCapacity: 2, maxCapacity: 16 },
    });
    const instances = ofType("aws:rds/clusterInstance:ClusterInstance");
    expect(instances).toHaveLength(2);
    expect(new Set(instances.map((r) => r.inputs.availabilityZone)).size).toBe(
      2,
    );
    for (const instance of instances) {
      expect(instance.inputs).toMatchObject({
        instanceClass: "db.serverless",
        publiclyAccessible: false,
      });
    }
  });
  test("keeps TLS cache and redundant outbound access", () => {
    expect(
      ofType("aws:elasticache/replicationGroup:ReplicationGroup")[0].inputs,
    ).toMatchObject({
      transitEncryptionEnabled: true,
      atRestEncryptionEnabled: true,
      automaticFailoverEnabled: true,
      numCacheClusters: 2,
    });
    expect(ofType("aws:ec2/natGateway:NatGateway")).toHaveLength(2);
    const albGroup = ofType("aws:ec2/securityGroup:SecurityGroup").find((r) =>
      r.name === "alb"
    )!;
    expect(albGroup.inputs).not.toHaveProperty("egress");
    expect(albGroup.inputs).not.toHaveProperty("ingress");
    const albEgress = ofType("aws:ec2/securityGroupRule:SecurityGroupRule")
      .find((r) => r.name === "alb-egress")!;
    expect(albEgress.inputs).toMatchObject({
      sourceSecurityGroupId: "tasks-id",
      fromPort: 8081,
      toPort: 8081,
    });
  });
  test("exports infrastructure data without rendering ECS definitions or secret values", () => {
    const object = ofType("aws:s3/bucketObjectv2:BucketObjectv2")[0];
    const data = JSON.parse(object.inputs.content);
    expect(data).toMatchObject({
      name: "portal-test",
      region: "ap-northeast-1",
      enableBot: false,
    });
    expect(data).not.toHaveProperty("tasks");
    expect(data).not.toHaveProperty("services");
    expect(ofType("aws:secretsmanager/secretVersion:SecretVersion"))
      .toHaveLength(0);
  });
  test("grants ecspresso the read APIs it uses before deployment and while waiting", () => {
    const policy = JSON.parse(
      ofType("aws:iam/rolePolicy:RolePolicy").find((r) =>
        r.name === "github-deploy"
      )!.inputs.policy,
    );
    const actions = policy.Statement.flatMap((
      statement: { Action: string[] },
    ) => statement.Action);
    for (
      const action of [
        "ecs:DescribeTaskDefinition",
        "ecs:ListServiceDeployments",
        "ecs:DescribeServiceDeployments",
        "application-autoscaling:DescribeScalableTargets",
        "application-autoscaling:DescribeScalingPolicies",
        "logs:GetLogEvents",
      ]
    ) expect(actions).toContain(action);
    expect(actions).not.toContain("secretsmanager:GetSecretValue");
  });
  test("rejects auto-pause, duplicate AZs and plaintext credentials", () => {
    for (
      const override of [
        { auroraMinAcu: 0 },
        { availabilityZones: ["ap-northeast-1a", "ap-northeast-1a"] },
        { environment: { ISUXPORTAL_ADMIN_PASSWORD: "bad" } },
        { enableBot: true },
      ]
    ) {
      expect(() => validateConfig({ ...config, ...override })).toThrow();
    }
  });
});
