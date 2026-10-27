import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import * as pulumi from "@pulumi/pulumi";

// ISUCON: use only existing Team 10 VMs. No AWS resources are declared here.
// Runtime configuration is versioned by this stack and applied via SSH/scp.
const config = new pulumi.Config();
export const runtime = JSON.parse(
	readFileSync(config.require("runtimeConfigPath"), "utf8"),
);
if (!["poll", "native"].includes(runtime.writeFenceMode ?? "poll")) {
	throw new Error("Unknown write fence mode");
}
if (runtime.writeFenceMode === "native" && (!runtime.writeFenceEnabled || !runtime.replicasEnabled)) {
	throw new Error("Native write fences require enabled write fences and replicas");
}
if (runtime.replicaReads && !runtime.replicasSynchronous && !runtime.readFenceEnabled && !runtime.writeFenceEnabled) {
	throw new Error("Replica reads require synchronous remote_apply replication, read fences, or write response fences");
}
export const artifactSha256 = createHash("sha256")
	.update(readFileSync("../../../" + runtime.archivePath))
	.digest("hex");
