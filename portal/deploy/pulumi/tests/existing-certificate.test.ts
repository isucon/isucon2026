import * as pulumi from "@pulumi/pulumi";
import { expect, test } from "vitest";
import { createPortal } from "../infrastructure";
import { defaults, validateConfig } from "../config";

const resources: pulumi.runtime.MockResourceArgs[] = [];
pulumi.runtime.setMocks(
  {
    newResource: (args) => {
      resources.push(args);
      return {
        id: args.name,
        state: {
          ...args.inputs,
          arn: `arn:aws:mock:ap-northeast-1:123456789012:${args.name}`,
          endpoint: "db.example.com",
          primaryEndpointAddress: "cache.example.com",
          repositoryUrl: "registry.example.com/portal",
          dnsName: "alb.example.com",
          zoneId: "ZALB",
          masterUserSecrets: [{
            secretArn:
              "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:db",
          }],
          domainValidationOptions: [{
            resourceRecordName: "_acm.portal.example.com",
            resourceRecordType: "CNAME",
            resourceRecordValue: "validation.example.com",
          }],
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

test("standby reuses an existing certificate without DNS or certificate changes", async () => {
  await pulumi.runtime.runInPulumiStack(async () =>
    createPortal(validateConfig({
      ...defaults,
      name: "portal-test",
      region: "ap-northeast-1",
      domainName: "portal.example.com",
      dnsManagement: "external",
      existingCertificateArn:
        "arn:aws:acm:ap-northeast-1:123456789012:certificate/00000000-0000-0000-0000-000000000000",
      standby: true,
      route53ZoneId: "",
      githubRepository: "example/portal",
      githubEnvironment: "develop",
      githubOidcProviderArn:
        "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com",
    }))
  );
  expect(resources.filter((r) => r.type === "aws:route53/record:Record"))
    .toHaveLength(0);
  expect(
    resources.filter((r) =>
      r.type === "aws:acm/certificate:Certificate" ||
      r.type === "aws:acm/certificateValidation:CertificateValidation"
    ),
  ).toHaveLength(0);
  expect(
    resources.filter((r) =>
      r.type === "aws:ec2/amiLaunchPermission:AmiLaunchPermission"
    ),
  ).toHaveLength(0);
  const listener = resources.find((r) =>
    r.type === "aws:lb/listener:Listener" && r.name === "https"
  )!;
  expect(listener.inputs.certificateArn).toBe(
    "arn:aws:acm:ap-northeast-1:123456789012:certificate/00000000-0000-0000-0000-000000000000",
  );
  const logs = resources.find((r) =>
    r.type === "aws:cloudwatch/logGroup:LogGroup"
  )!;
  expect(logs.inputs.retentionInDays).toBe(0);
  expect(logs.inputs.skipDestroy).toBe(true);
});
