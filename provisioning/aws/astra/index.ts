import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import * as pulumi from "@pulumi/pulumi";

// ISUCON: use only existing Team 12 VMs. No AWS resources are declared here.
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
if (typeof runtime.asiaReplicaEnabled !== "boolean" || typeof runtime.asiaReplicaReads !== "boolean"
    || (runtime.asiaReplicaEnabled && !runtime.replicasEnabled)
    || (runtime.asiaReplicaReads && (!runtime.asiaReplicaEnabled || !runtime.replicaReads))) {
    throw new Error("Asia replica reads require the existing Asia standby and enabled replica reads");
}
if (!["all", "feed_search"].includes(runtime.asiaReplicaReadScope)) {
    throw new Error("Unknown Asia replica read scope");
}
if (typeof (runtime.asiaReplicaSynchronous ?? false) !== "boolean"
    || (runtime.asiaReplicaSynchronous && (!runtime.asiaReplicaEnabled || !runtime.replicasEnabled
        || !runtime.writeFenceEnabled || runtime.writeFenceMode !== "native"))) {
    throw new Error("Synchronous Asia reads require the existing standby and native write response fences");
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

if (!["debug", "info", "error"].includes(runtime.applicationLogLevel)) {
    throw new Error("Invalid application log level; failed logins are emitted independently");
}

if (typeof runtime.nginxRequestBuffering !== "boolean") {
    throw new Error("Nginx request buffering must be explicitly configured");
}

if (typeof runtime.nginxAccessLogEnabled !== "boolean") {
    throw new Error("Nginx access logging must be explicitly configured");
}

if (!Number.isInteger(runtime.nginxAccessLogBufferBytes) || ![0, 16384, 32768, 65536].includes(runtime.nginxAccessLogBufferBytes)
    || !Number.isInteger(runtime.nginxAccessLogFlushSeconds) || runtime.nginxAccessLogFlushSeconds < 1 || runtime.nginxAccessLogFlushSeconds > 5) {
    throw new Error("Invalid bounded Nginx access log buffering");
}

if (runtime.fileImageStore) {
    const files = runtime.fileImageStore;
    if (typeof files.enabled !== "boolean" || typeof files.writesEnabled !== "boolean"
        || typeof files.archiveReadsEnabled !== "boolean" || (files.archiveReadsEnabled && !files.enabled)
        || files.root !== "/var/lib/astra-images" || files.port !== 8082
        || (files.enabled && !runtime.regionalImageStoresEnabled)
        || (files.writesEnabled && (!files.enabled || !runtime.regionalImageWritesEnabled || !runtime.imageStore?.writesEnabled))) {
        throw new Error("File images require deployed readers and the existing regional write flow");
    }
}

if (!Number.isInteger(runtime.listenBacklog) || runtime.listenBacklog < 128 || runtime.listenBacklog > 4096) {
    throw new Error("Invalid application listen backlog");
}

if (typeof runtime.signatureCacheCoalescing !== "boolean" || typeof runtime.signatureCacheMetrics !== "boolean") {
    throw new Error("Signature cache options must be explicit booleans");
}

if (typeof runtime.issuedImageUrlCacheEnabled !== "boolean") {
    throw new Error("Issued image URL caching must be explicitly configured");
}

if (!Number.isInteger(runtime.issuedImageUrlCacheEntries) || runtime.issuedImageUrlCacheEntries < 16
    || runtime.issuedImageUrlCacheEntries > 262144 || runtime.issuedImageUrlCacheEntries % 16 !== 0) {
    throw new Error("Invalid issued image URL cache capacity");
}

if (typeof runtime.nginxJsonGzipEnabled !== "boolean") {
    throw new Error("JSON response compression must be explicitly configured");
}

if (typeof runtime.signatureFileCacheEnabled !== "boolean" || runtime.signatureFileCachePath !== "var/astra/signatures.bin") {
    throw new Error("Invalid private signature file cache configuration");
}
export const signatureFileSha256 = runtime.signatureFileCacheEnabled
    ? createHash("sha256").update(readFileSync("../../../" + runtime.signatureFileCachePath)).digest("hex") : "";

if (!Number.isInteger(runtime.imageCacheMiB) || runtime.imageCacheMiB < 64 || runtime.imageCacheMiB > 1024) {
    throw new Error("Invalid image byte cache capacity");
}

if (!Number.isInteger(runtime.metadataPoolConnections) || runtime.metadataPoolConnections < 4 || runtime.metadataPoolConnections > 64) {
    throw new Error("Invalid metadata connection pool capacity");
}

if (typeof runtime.notificationBatchWritesEnabled !== "boolean") {
    throw new Error("Notification batch writes must be explicitly configured");
}

if (!Number.isInteger(runtime.primarySharedBuffersMiB) || runtime.primarySharedBuffersMiB < 128 || runtime.primarySharedBuffersMiB > 1536) {
    throw new Error("Invalid primary PostgreSQL shared buffer capacity");
}

if (runtime.archivePeer) {
    const peer = runtime.archivePeer;
    if (typeof peer.enabled !== "boolean" || typeof peer.readsEnabled !== "boolean"
        || (peer.readsEnabled && !peer.enabled)
        || (peer.aioThreads !== undefined && typeof peer.aioThreads !== "boolean")
        || peer.host !== runtime.database.host || peer.instanceId !== runtime.database.instanceId
        || peer.host !== "192.168.180.10" || peer.port !== 8082
        || peer.root !== "/var/lib/astra-images/packs"
        || (peer.enabled && !runtime.fileImageStore?.archiveReadsEnabled)) {
        throw new Error("Archive peer must use existing db1 with verified immutable packs");
    }
}
export const archivePeerManifestSha256 = runtime.archivePeer?.enabled
    ? createHash("sha256").update(readFileSync("../../../webapp-optmized-astra/deploy/archive-peer-manifest.json")).digest("hex") : "";

const clientBodyBufferKiB = runtime.nginxClientBodyBufferKiB ?? 8;
if (!Number.isInteger(clientBodyBufferKiB) || clientBodyBufferKiB < 8 || clientBodyBufferKiB > 1024) {
    throw new Error("Invalid Nginx client body buffer capacity");
}

if (![0, 64, 128, 256, 512].includes(runtime.nginxMallocThresholdKiB ?? 0)) {
    throw new Error("Invalid Nginx allocator threshold");
}

if (![0, 64, 128, 256, 512].includes(runtime.applicationMallocThresholdKiB ?? 0)) {
    throw new Error("Invalid application allocator threshold");
}

if (runtime.authTimingEnabled !== undefined && typeof runtime.authTimingEnabled !== "boolean") {
    throw new Error("Invalid auth timing flag");
}

if (runtime.postTimingEnabled !== undefined && typeof runtime.postTimingEnabled !== "boolean") {
    throw new Error("Invalid post timing flag");
}

const exportConcurrency = runtime.exportConcurrency ?? 2;
if (!Number.isInteger(exportConcurrency) || exportConcurrency < 1 || exportConcurrency > 16) {
    throw new Error("Invalid export concurrency");
}

if (runtime.readTimingEnabled !== undefined && typeof runtime.readTimingEnabled !== "boolean") {
    throw new Error("Invalid read timing flag");
}

if (runtime.sessionCacheEnabled !== undefined && typeof runtime.sessionCacheEnabled !== "boolean") {
    throw new Error("Invalid session cache flag");
}
if (runtime.sessionCacheEnabled && (!runtime.writeFenceEnabled || runtime.writeFenceMode !== "native" || !runtime.replicasEnabled || !runtime.replicaReads)) {
    throw new Error("Session caching requires native write visibility and regional replicas");
}

if (runtime.initialHistoryEnabled !== undefined && typeof runtime.initialHistoryEnabled !== "boolean") {
    throw new Error("Invalid initial history flag");
}

if (runtime.initialHistoryExportEnabled !== undefined && typeof runtime.initialHistoryExportEnabled !== "boolean") {
    throw new Error("Invalid initial history export flag");
}
if (runtime.initialHistoryExportEnabled && !runtime.initialHistoryEnabled) {
    throw new Error("Initial export requires the initial history snapshot");
}

const memoryPoolConnections = runtime.memoryPoolConnections ?? 0;
if (!Number.isInteger(memoryPoolConnections) || memoryPoolConnections < 0 || memoryPoolConnections > 16) {
    throw new Error("Invalid memory pool capacity");
}

const shortReadPoolConnections = runtime.shortReadPoolConnections ?? 0;
if (!Number.isInteger(shortReadPoolConnections) || shortReadPoolConnections < 0 || shortReadPoolConnections > 16) {
    throw new Error("Invalid short read pool capacity");
}

if (runtime.initialGraphEnabled !== undefined && typeof runtime.initialGraphEnabled !== "boolean") {
    throw new Error("Invalid initial graph flag");
}

for (const key of ["primaryAsiaSessionsEnabled", "localAsiaSessionWritesEnabled"]) {
    if (runtime[key] !== undefined && typeof runtime[key] !== "boolean") {
        throw new Error("Invalid session authority flag: " + key);
    }
}
if (runtime.localAsiaSessionWritesEnabled && (!runtime.primaryAsiaSessionsEnabled || !runtime.writeFenceEnabled || runtime.writeFenceMode !== "native")) {
    throw new Error("Local session responses require primary authentication and native barriers for other writes");
}
if (runtime.primaryAsiaSessionsEnabled && runtime.sessionCacheEnabled) {
    throw new Error("Primary session routing requires fresh SQL authentication");
}

const sessionPoolConnections = runtime.sessionPoolConnections ?? 0;
if (!Number.isInteger(sessionPoolConnections) || sessionPoolConnections < 0 || sessionPoolConnections > 16) {
    throw new Error("Invalid session pool capacity");
}

if (!["", "primary", "europe", "america"].includes(runtime.recommendationReadRegion ?? "")) {
    throw new Error("Invalid recommendation read region");
}
if (runtime.recommendationReadRegion && (!runtime.replicasEnabled || !runtime.writeFenceEnabled || runtime.writeFenceMode !== "native")) {
    throw new Error("Remote recommendations require existing replicas and native write barriers");
}
const recommendationPoolConnections = runtime.recommendationPoolConnections ?? 32;
if (!Number.isInteger(recommendationPoolConnections) || recommendationPoolConnections < 4 || recommendationPoolConnections > 64) {
    throw new Error("Invalid recommendation pool capacity");
}

if (typeof runtime.initialPostImagesEnabled !== "boolean" || (runtime.initialPostImagesEnabled && (!runtime.imageStore?.enabled || !runtime.initialHistoryEnabled))) {
    throw new Error("Initial post images require immutable image storage and initial history migrations");
}

const initialImagePoolConnections = runtime.initialImagePoolConnections ?? 0;
if (!Number.isInteger(initialImagePoolConnections) || initialImagePoolConnections < 0 || initialImagePoolConnections > 16
    || (initialImagePoolConnections && !runtime.initialPostImagesEnabled)) {
    throw new Error("Invalid initial image pool capacity");
}
