import * as pulumi from "@pulumi/pulumi";
import { createSupervisor } from "../lib/supervisor";

const config = new pulumi.Config();
const worker = createSupervisor({
  detachedRouterHosts: config.getObject<string[]>("detachedRouterHosts"),
  benchmarkScale: config.get("benchmarkScale"),
  routerNetworkInterfaces: config.getObject<Record<string, string>>(
    "routerNetworkInterfaces",
  ),
  retainDedicatedNetwork: config.getBoolean("retainDedicatedNetwork"),
  sharedNetwork: config.getObject<
    { vpcId: string; subnetId: string; egressPublicIp: string }
  >("sharedNetwork"),
  region: new pulumi.Config("aws").require("region"),
  availabilityZone: config.require("availabilityZone"),
  image: config.require("image"),
  teamImages: config.getObject<Record<string, string>>("teamImages"),
  endpoint: config.require("portalEndpoint"),
  tokenParameterArn: config.get("tokenParameterArn"),
  tokenSecretArn: config.get("tokenSecretArn"),
  teamIds: config.getObject<string[]>("teamIds"),
  routerSecurityGroupIds: config.getObject<string[]>("routerSecurityGroupIds"),
  targetOverrides: config.getObject<
    Record<string, { target: string; host: string }>
  >("targetOverrides"),
  desiredCount: config.getNumber("desiredCount"),
  tokenKmsKeyArn: config.get("tokenKmsKeyArn"),
  teamId: config.get("teamId"),
  instanceName: config.require("instanceName"),
});
export const serviceName = worker.serviceName;
export const clusterName = worker.clusterName;
export const logGroupName = worker.logGroupName;
export const egressPublicAddress = worker.egressPublicAddress;

export const serviceNames = worker.serviceNames;
export const repositoryUrl = worker.repositoryUrl;

export const routerPublicIps = worker.routerPublicIps;
