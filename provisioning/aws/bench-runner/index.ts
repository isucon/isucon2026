import * as pulumi from "@pulumi/pulumi";
import { createBenchRunner } from "../lib/bench-runner";

const config = new pulumi.Config();
const runner = createBenchRunner({
	region: new pulumi.Config("aws").require("region"),
	vpcId: config.require("vpcId"),
	subnetId: config.require("subnetId"),
	resultRetentionDays: config.getNumber("resultRetentionDays"),
	imageRetentionCount: config.getNumber("imageRetentionCount"),
	instanceRoleArns: config.getObject<string[]>("instanceRoleArns"),
});

export const infra = runner.infra;
