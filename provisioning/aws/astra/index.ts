import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import * as pulumi from "@pulumi/pulumi";

// ISUCON: use only existing Team 10 VMs. No AWS resources are declared here.
// Runtime configuration is versioned by this stack and applied via SSH/scp.
const config = new pulumi.Config();
export const runtime = JSON.parse(
	readFileSync(config.require("runtimeConfigPath"), "utf8"),
);
if (runtime.replicaReads && !runtime.replicasSynchronous && !runtime.readFenceEnabled) {
	throw new Error("Replica reads require synchronous remote_apply replication or read fences");
}
export const artifactSha256 = createHash("sha256")
	.update(readFileSync("../../../" + runtime.archivePath))
	.digest("hex");
