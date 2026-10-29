import * as pulumi from "@pulumi/pulumi";
import type { PoolNetworkConfig } from "../pool/network";
import { createProductionSupervisor } from "./infrastructure";

const config = new pulumi.Config();
const supervisor = createProductionSupervisor({
  name: config.get("name") ?? "isucon2026-production-supervisor",
  region: new pulumi.Config("aws").require("region"),
  endpoint: config.require("endpoint"),
  image: config.require("image"),
  tokenSecretArn: config.require("tokenSecretArn"),
  desiredCount: config.getNumber("desiredCount") ?? 3,
  network: config.requireObject<PoolNetworkConfig>("network"),
});
export const deployments = supervisor.deployments;
export const subnetIds = supervisor.subnetIds;
export const clusterName = supervisor.clusterName;
export const logGroupName = supervisor.logGroupName;
