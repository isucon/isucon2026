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
    if (typeof store.enabled !== "boolean" || typeof store.writesEnabled !== "boolean" || typeof store.cacheEnabled !== "boolean"
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

if (runtime.regionalUploadCacheEnabled && (!runtime.writeProxyEnabled || !runtime.replicaReads || !runtime.imageStore?.enabled || !runtime.imageStore?.writesEnabled)) {
    throw new Error("Regional upload caching requires the private writer, local replicas and immutable image storage");
}

if (runtime.localUploadCacheEnabled && (!runtime.imageStore?.enabled || !runtime.imageStore?.writesEnabled)) {
    throw new Error("Local upload caching requires immutable image storage");
}

if (!Number.isInteger(runtime.tokioWorkerThreads) || runtime.tokioWorkerThreads < 1 || runtime.tokioWorkerThreads > 16) {
    throw new Error("Invalid Tokio worker thread count");
}

if (runtime.regionalReactionCacheEnabled && !runtime.regionalUploadCacheEnabled) {
    throw new Error("Regional reaction caching requires upload caching");
}

if (runtime.regionalImageStoresEnabled && !runtime.imageStore?.enabled) {
    throw new Error("Regional immutable image readers require the existing image store");
}
if (runtime.regionalImageWritesEnabled && (!runtime.regionalImageStoresEnabled || !runtime.regionalUploadCacheEnabled || !runtime.imageStore?.writesEnabled || runtime.regionalUploadEnvPath !== "var/astra/.regional-upload.env")) {
    throw new Error("Regional image writes require deployed readers, the upload proxy, and a private SCP environment file");
}

if (typeof runtime.asiaUpstreamKeepaliveEnabled !== "boolean") {
    throw new Error("Asia upstream connection reuse must be explicitly configured");
}

if (!["debug", "info"].includes(runtime.applicationLogLevel)) {
    throw new Error("Keep failed-login info logs for the existing abuse guard");
}
