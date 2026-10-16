import * as pulumi from "@pulumi/pulumi";

export const processes = [
  "portal",
  "worker",
  "notifyloop",
  "bot",
  "migrate",
] as const;
export const requiredSecrets = [
  "ISUXPORTAL_SESSION_SECRET",
  "ISUXPORTAL_BYPASS_SECRET",
  "ISUXPORTAL_CHECKER_SECRET",
  "ISUXPORTAL_BENCH_TOKEN",
  "ISUXPORTAL_ADMIN_PASSWORD",
];
export const defaults = {
  natGatewayCount: 2,
  availabilityZones: ["ap-northeast-1a", "ap-northeast-1c"],
  vpcCidr: "10.26.0.0/16",
  auroraEngineVersion: "17.7",
  auroraMinAcu: 2,
  auroraMaxAcu: 16,
  cacheNodeType: "cache.t4g.small",
  enableBot: false,
  logRetentionDays: 30,
  dnsManagement: "route53" as "route53" | "external",
  applicationDatabase: "isuxportal",
  redisDatabase: 0,
  environment: {} as Record<string, string>,
  secretKeys: [
    ...requiredSecrets,
    "ISUXPORTAL_GITHUB_CLIENT_SECRET",
    "ISUXPORTAL_DISCORD_CLIENT_SECRET",
  ],
};
export type PortalConfig = typeof defaults & {
  name: string;
  region: string;
  domainName: string;
  route53ZoneId: string;
  githubRepository: string;
  githubEnvironment: string;
  githubOidcProviderArn: string;
};
export function validateConfig(c: PortalConfig): PortalConfig {
  if (!["route53", "external"].includes(c.dnsManagement)) {
    throw new Error("dnsManagement must be route53 or external");
  }
  if (c.dnsManagement === "route53" && !c.route53ZoneId) {
    throw new Error("route53ZoneId is required for Route 53 DNS");
  }
  if (!/^[a-z][a-z0-9_]{0,62}$/.test(c.applicationDatabase)) {
    throw new Error(
      "applicationDatabase must be a lowercase PostgreSQL database name",
    );
  }
  if (
    !Number.isInteger(c.redisDatabase) || c.redisDatabase < 0 ||
    c.redisDatabase > 15
  ) {
    throw new Error("redisDatabase must be an integer from 0 to 15");
  }
  if (!/^[a-z][a-z0-9-]{1,23}$/.test(c.name)) {
    throw new Error("name must be 2-24 lowercase letters, digits or hyphens");
  }
  if (
    c.availabilityZones.length !== 2 ||
    new Set(c.availabilityZones).size !== 2 ||
    c.availabilityZones.some((az) => !az.startsWith(c.region))
  ) throw new Error("two distinct AZs in the deployment region are required");
  if (![1, 2].includes(c.natGatewayCount)) {
    throw new Error("natGatewayCount must be 1 or 2");
  }
  if (
    c.auroraMinAcu < 0.5 || c.auroraMaxAcu < c.auroraMinAcu ||
    c.auroraMaxAcu > 256 || !Number.isInteger(c.auroraMinAcu * 2) ||
    !Number.isInteger(c.auroraMaxAcu * 2)
  ) {
    throw new Error(
      "Aurora capacity must be positive multiples of 0.5, min <= max <= 256",
    );
  }
  // A /16 keeps the two public /24 and two private /24 subnet calculations explicit.
  if (
    !/^(?:\d{1,3}\.){2}0\.0\/16$/.test(c.vpcCidr) ||
    c.vpcCidr.split(".").slice(0, 2).some((n) => Number(n) > 255)
  ) throw new Error("vpcCidr must be a canonical IPv4 /16");
  for (const [key, value] of Object.entries(c.environment)) {
    if (
      (!key.startsWith("ISUXPORTAL_") &&
        !["SENTRY_DSN", "MONITOR_ENV"].includes(key)) ||
      /SECRET|PASSWORD|TOKEN|PRIVATE_KEY|DATABASE_URL|WEBHOOK_URL/.test(key) ||
      typeof value !== "string"
    ) {
      throw new Error(
        `Use secretKeys for credentials, not environment: ${key}`,
      );
    }
  }
  if (requiredSecrets.some((key) => !c.secretKeys.includes(key))) {
    throw new Error("required authentication secrets cannot be omitted");
  }
  if (c.enableBot && !c.secretKeys.includes("ISUXPORTAL_DISCORD_BOT_TOKEN")) {
    throw new Error(
      "enableBot requires ISUXPORTAL_DISCORD_BOT_TOKEN in secretKeys",
    );
  }
  return c;
}
export function loadConfig(): PortalConfig {
  const config = new pulumi.Config();
  return validateConfig({
    name: config.require("name"),
    region: new pulumi.Config("aws").require("region"),
    domainName: config.require("domainName"),
    route53ZoneId: config.get("route53ZoneId") ?? "",
    dnsManagement: config.get<"route53" | "external">("dnsManagement") ??
      defaults.dnsManagement,
    githubRepository: config.require("githubRepository"),
    githubEnvironment: config.require("githubEnvironment"),
    githubOidcProviderArn: config.require("githubOidcProviderArn"),
    natGatewayCount: config.getNumber("natGatewayCount") ??
      defaults.natGatewayCount,
    availabilityZones: config.getObject<string[]>("availabilityZones") ??
      defaults.availabilityZones,
    vpcCidr: config.get("vpcCidr") ?? defaults.vpcCidr,
    auroraEngineVersion: config.get("auroraEngineVersion") ??
      defaults.auroraEngineVersion,
    auroraMinAcu: config.getNumber("auroraMinAcu") ?? defaults.auroraMinAcu,
    auroraMaxAcu: config.getNumber("auroraMaxAcu") ?? defaults.auroraMaxAcu,
    cacheNodeType: config.get("cacheNodeType") ?? defaults.cacheNodeType,
    enableBot: config.getBoolean("enableBot") ?? defaults.enableBot,
    logRetentionDays: config.getNumber("logRetentionDays") ??
      defaults.logRetentionDays,
    applicationDatabase: config.get("applicationDatabase") ??
      defaults.applicationDatabase,
    redisDatabase: config.getNumber("redisDatabase") ?? defaults.redisDatabase,
    environment: config.getObject<Record<string, string>>("environment") ??
      defaults.environment,
    secretKeys: config.getObject<string[]>("secretKeys") ?? defaults.secretKeys,
  });
}
