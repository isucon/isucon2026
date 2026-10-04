import { loadConfig } from "./config";
import { createPortal } from "./infrastructure";

const portal = createPortal(loadConfig());
export const ecrRepositoryUrl = portal.ecrRepositoryUrl;
export const deployRoleArn = portal.deployRoleArn;
export const ecspressoConfigUri = portal.ecspressoConfigUri;
export const applicationSecretArn = portal.applicationSecretArn;
export const sshSecretArn = portal.sshSecretArn;
export const databaseSecretArn = portal.databaseSecretArn;
export const databaseEndpoint = portal.databaseEndpoint;
export const url = portal.url;
export const natPublicIps = portal.natPublicIps;
export const alertsTopicArn = portal.alertsTopicArn;

export const vpcId = portal.vpcId;
export const privateSubnetIds = portal.privateSubnetIds;
