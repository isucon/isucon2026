import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import * as pulumi from "@pulumi/pulumi";

// ISUCON: use only existing Team 10 VMs. No AWS resources are declared here.
// Runtime configuration is versioned by this stack and applied via SSH/scp.
const config = new pulumi.Config();
export const runtime = JSON.parse(
	readFileSync(config.require("runtimeConfigPath"), "utf8"),
);
if (!Number.isInteger(runtime.nginxWorkerConnections) || runtime.nginxWorkerConnections < 768 || runtime.nginxWorkerConnections > 65536
	|| !Number.isInteger(runtime.nginxWorkerRlimitNofile) || runtime.nginxWorkerRlimitNofile < runtime.nginxWorkerConnections || runtime.nginxWorkerRlimitNofile > 524288) {
	throw new Error("Invalid nginx connection or file descriptor limit");
}
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

if (runtime.imageStore) {
    const store = runtime.imageStore;
    const asia = runtime.applications.find((app: {region: string}) => app.region === "asia");
    if (typeof store.enabled !== "boolean" || typeof store.writesEnabled !== "boolean"
        || (store.writesEnabled && !store.enabled) || store.port !== 5433
        || store.host !== asia.host || store.instanceId !== asia.instanceId) {
        throw new Error("The image store must use the existing Asia application VM and requires enabled readers before writes");
    }
}

if (runtime.imageCachePushEnabled && !runtime.imageStore?.enabled) {
    throw new Error("Image cache warming requires immutable image storage");
}

if (runtime.guardJournalUnit !== "webapp-optmized-astra*.service") {
    throw new Error("The existing login guard must monitor both application and private writer units");
}
