#!/bin/bash
set -euo pipefail
service=${1:?service required}
case "$service" in webapp-optmized-astra|webapp-optmized-astra-writer) ;; *) exit 2 ;; esac
read -r threads backlog image_cache_mib metadata_connections batch_writes export_concurrency graph_concurrency < <(python3 - <<'PYCONFIG'
import json
runtime=json.load(open('/home/isucon/astra-deploy/runtime.json'))
value=runtime['tokioWorkerThreads']
backlog=runtime['listenBacklog']
assert type(backlog) is int and 128 <= backlog <= 4096
assert type(value) is int and 1 <= value <= 16
image_cache_mib=runtime['imageCacheMiB']
assert type(image_cache_mib) is int and 64 <= image_cache_mib <= 1024
metadata_connections=runtime['metadataPoolConnections']
assert type(metadata_connections) is int and 4 <= metadata_connections <= 64
batch_writes=runtime['notificationBatchWritesEnabled']
assert type(batch_writes) is bool
export_concurrency=runtime.get('exportConcurrency',2)
assert type(export_concurrency) is int and 1 <= export_concurrency <= 16
graph_concurrency=runtime.get('graphQueryConcurrency',0)
assert type(graph_concurrency) is int and 0 <= graph_concurrency <= 16
print(value,backlog,image_cache_mib,metadata_connections,int(batch_writes),export_concurrency,graph_concurrency)
PYCONFIG
)
mkdir -p "/etc/systemd/system/$service.service.d"
printf '[Service]\nEnvironment=TOKIO_WORKER_THREADS=%s\nEnvironment=ISUREAL_LISTEN_BACKLOG=%s\nEnvironment=ISUREAL_IMAGE_CACHE_MIB=%s\nEnvironment=ISUREAL_METADATA_POOL_CONNECTIONS=%s\nEnvironment=ISUREAL_NOTIFICATION_BATCH_WRITES=%s\nEnvironment=ISUREAL_EXPORT_CONCURRENCY=%s\nEnvironment=ISUREAL_GRAPH_QUERY_CONCURRENCY=%s\n' "$threads" "$backlog" "$image_cache_mib" "$metadata_connections" "$batch_writes" "$export_concurrency" "$graph_concurrency" > "/etc/systemd/system/$service.service.d/tokio.conf"
