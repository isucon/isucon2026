import { buildRuntimeTopology, routerEnvironment, routerNetplan } from "./network";
import { ServerNetwork, serverNetworks } from "./topology";

function base64(content: string): string {
  return Buffer.from(content, "utf8").toString("base64");
}

function file(
  path: string,
  content: string,
  mode = "0644",
  owner = "root:root",
): string {
  return `  - path: ${path}
    encoding: b64
    permissions: "${mode}"
    owner: ${owner}
    content: ${base64(content)}`;
}

function multipartCloudConfig(config: string): string {
  return `#cloud-config
${config}
`;
}

function requireSafeSecret(name: string, value: string, pattern: RegExp): void {
  if (!pattern.test(value)) {
    throw new Error(`${name} has unsupported characters or length`);
  }
}

export function serviceUserData(
  releaseId: string,
  network: ServerNetwork,
  domain: string,
  databasePassword: string,
  cursorKey: string,
): string {
  requireSafeSecret(
    "databasePassword",
    databasePassword,
    /^[A-Za-z0-9._~-]{16,128}$/,
  );
  requireSafeSecret("cursorHmacKey", cursorKey, /^[A-Za-z0-9_-]{32,256}$/);
  const topology = buildRuntimeTopology("https://portal.invalid", domain);
  const backendEnvironment = `ISUREAL_LISTEN_ADDRESS=127.0.0.1:8081
ISUREAL_PUBLIC_ORIGIN=https://app.team1-aws.${domain}
ISUREAL_DATABASE_URL=postgresql://isureal:${databasePassword}@127.0.0.1:5432/isureal?sslmode=disable
ISUREAL_IMAGE_ROOT=/var/lib/isureal/images
ISUREAL_CURSOR_HMAC_KEY=${cursorKey}
ISUREAL_OPENAPI_PATH=/opt/isucon/share/openapi.yaml
ISUREAL_IMAGE_SANITIZER_PATH=/opt/isucon/bin/image-sanitizer
`;
  const nginx = `server {
    listen ${network.serviceIp}:8080 default_server;
    server_name _;
    root /opt/isucon/frontend;
    index index.html;
    client_max_body_size 32m;
    location /api/ {
        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    location / { try_files $uri $uri/ /index.html; }
}
`;
  const initialize = `#!/bin/sh
set -eu
test "$(cat /etc/isucon/aws-release-id)" = "${releaseId}"
pg_createcluster 16 main --start || test -e /var/lib/postgresql/16/main/PG_VERSION
systemctl enable --now postgresql.service
runuser -u postgres -- psql -v ON_ERROR_STOP=1 --set=password="$(printf %s "${databasePassword}" | sed "s/'/''/g")" <<'SQL'
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'isureal') THEN
    CREATE ROLE isureal LOGIN;
  END IF;
END
$$;
ALTER ROLE isureal PASSWORD :'password';
SELECT 'CREATE DATABASE isureal OWNER isureal'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'isureal')\\gexec
SQL
ISUREAL_DATABASE_URL="postgresql://isureal:${databasePassword}@127.0.0.1:5432/isureal?sslmode=disable" \\
  /opt/isucon/bin/seed --if-empty --dir /opt/isucon/share/migrations
ln -sfn /etc/nginx/sites-available/isureal /etc/nginx/sites-enabled/isureal
rm -f /etc/nginx/sites-enabled/default
cat >/usr/local/sbin/isureal-node-check <<'CHECK'
#!/bin/sh
set -eu
pg_isready -q
curl -fsS http://127.0.0.1:8081/health/ready >/dev/null
curl -fsS -H 'Host: app.team1-aws.${domain}' http://${network.serviceIp}:8080/ >/dev/null
CHECK
chmod 0755 /usr/local/sbin/isureal-node-check
systemctl daemon-reload
systemctl enable --now isureal-api.service isureal-worker.service nginx.service
`;
  const initializeUnit = `[Unit]
Description=Initialize ISUReal AWS problem server runtime
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/isureal-aws-initialize
RemainAfterExit=yes
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
`;
  return multipartCloudConfig(`write_files:
${file("/etc/isucon/aws-release-id", `${releaseId}\n`)}
${file("/etc/isucon/aws-topology.json", `${JSON.stringify(topology, null, 2)}\n`)}
${file("/etc/isucon/backend.env", backendEnvironment, "0640", "root:isureal")}
${file("/etc/nginx/sites-available/isureal", nginx)}
${file("/usr/local/sbin/isureal-aws-initialize", initialize, "0750")}
${file("/etc/systemd/system/isureal-aws-initialize.service", initializeUnit)}
runcmd:
  - [systemctl, daemon-reload]
  - [systemctl, enable, --now, isureal-aws-initialize.service]
`);
}

export function routerUserData(
  releaseId: string,
  portalEndpoint: string,
  domain: string,
  controlIp: string,
  controlMac: string,
  dataMacs: readonly string[],
  tlsCertificate: string,
  tlsPrivateKey: string,
): string {
  const topology = buildRuntimeTopology(portalEndpoint, domain);
  topology.networks.control.routerAddress = controlIp;
  const env = routerEnvironment(topology);
  const portalEnvironment = `PORTAL_TEAM_NUMBER=1
PORTAL_TEAM_HASH=aws
PORTAL_DOMAIN_SUFFIX=${domain}
PORTAL_ROUTER_MODE=external-router
PORTAL_LISTEN_ADDRESS=127.0.0.1:18082
PORTAL_WEB_DIR=
`;
  const portalUnit = `[Unit]
Description=ISUReal validation Portal
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=isureal-router
Group=isureal-router
EnvironmentFile=/etc/isucon/portal.env
ExecStart=/opt/isucon/bin/isureal-portal
Restart=on-failure
RestartSec=1s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true

[Install]
WantedBy=multi-user.target
`;
  const interfaceSetup = `#!/bin/sh
set -eu
for expected in ${serverNetworks.map((network) => network.routerIp).join(" ")}; do
  if ! ip -4 address show | grep -Fq " $expected/"; then
    echo "missing Router data address: $expected" >&2
    exit 1
  fi
done
sysctl -w net.ipv4.ip_forward=1 >/dev/null
sysctl -w net.ipv4.conf.all.rp_filter=0 >/dev/null
sysctl -w net.ipv4.conf.default.rp_filter=0 >/dev/null
nft delete table ip isureal_aws_egress 2>/dev/null || true
nft -f - <<'NFT'
table ip isureal_aws_egress {
  chain forward {
    type filter hook forward priority -10; policy accept;
  }
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    ip saddr 192.168.0.0/16 oifname "control0" masquerade
  }
}
NFT
`;
  const envoy = `node:
  cluster: isureal-router
  id: isureal-router-envoy
admin:
  address:
    socket_address: { address: 127.0.0.1, port_value: 9901 }
dynamic_resources:
  ads_config:
    api_type: GRPC
    transport_api_version: V3
    grpc_services:
      - envoy_grpc: { cluster_name: xds_cluster }
static_resources:
  listeners:
    - name: edge_http
      address:
        socket_address: { address: 0.0.0.0, port_value: 8080 }
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: edge_http
                codec_type: AUTO
                rds:
                  route_config_name: isureal_routes
                  config_source: { ads: {}, resource_api_version: V3 }
                http_filters:
                  - name: envoy.filters.http.fault
                    typed_config: { "@type": type.googleapis.com/envoy.extensions.filters.http.fault.v3.HTTPFault }
                  - name: envoy.filters.http.bandwidth_limit
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.bandwidth_limit.v3.BandwidthLimit
                      stat_prefix: edge_bandwidth
                      enable_mode: DISABLED
                  - name: envoy.filters.http.router
                    typed_config: { "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router }
    - name: edge_https
      address:
        socket_address: { address: 0.0.0.0, port_value: 443 }
      filter_chains:
        - transport_socket:
            name: envoy.transport_sockets.tls
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext
              common_tls_context:
                tls_certificates:
                  - certificate_chain: { filename: /etc/isucon/envoy/tls/server.crt }
                    private_key: { filename: /etc/isucon/envoy/tls/server.key }
          filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: edge_https
                codec_type: AUTO
                rds:
                  route_config_name: isureal_routes
                  config_source: { ads: {}, resource_api_version: V3 }
                http_filters:
                  - name: envoy.filters.http.fault
                    typed_config: { "@type": type.googleapis.com/envoy.extensions.filters.http.fault.v3.HTTPFault }
                  - name: envoy.filters.http.bandwidth_limit
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.bandwidth_limit.v3.BandwidthLimit
                      stat_prefix: edge_https_bandwidth
                      enable_mode: DISABLED
                  - name: envoy.filters.http.router
                    typed_config: { "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router }
  clusters:
    - name: xds_cluster
      type: STATIC
      connect_timeout: 1s
      typed_extension_protocol_options:
        envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
          "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
          explicit_http_config: { http2_protocol_options: {} }
      load_assignment:
        cluster_name: xds_cluster
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address: { address: 127.0.0.1, port_value: 18000 }
${serverNetworks
  .map(
    (network) => `    - name: ${network.id}
      type: STATIC
      connect_timeout: 2s
      load_assignment:
        cluster_name: ${network.id}
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address: { address: ${network.serviceIp}, port_value: 8080 }`,
  )
  .join("\n")}
`;
  const initialize = `#!/bin/sh
set -eu
test "$(cat /etc/isucon/aws-release-id)" = "${releaseId}"
rm -f /etc/netplan/50-cloud-init.yaml
netplan apply
/usr/local/sbin/isureal-aws-network-check
/opt/isucon/bin/envoy --mode validate -c /etc/isucon/envoy/bootstrap.yaml
systemctl daemon-reload
systemctl enable --now isureal-portal.service
for attempt in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18082/healthz >/dev/null; then
    break
  fi
  if [ "$attempt" -eq 30 ]; then
    echo "Portal did not become healthy" >&2
    exit 1
  fi
  sleep 2
done
systemctl enable --now isureal-router.service isureal-envoy.service
cat >/usr/local/sbin/isureal-node-check <<'CHECK'
#!/bin/sh
set -eu
curl -fsS http://${controlIp}:18081/healthz >/dev/null
curl -fsS http://${controlIp}:18081/readyz >/dev/null
CHECK
chmod 0755 /usr/local/sbin/isureal-node-check
`;
  const initializeUnit = `[Unit]
Description=Initialize ISUReal AWS Router runtime
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/isureal-aws-initialize
RemainAfterExit=yes
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
`;
  return multipartCloudConfig(`write_files:
${file("/etc/isucon/aws-release-id", `${releaseId}\n`)}
${file("/etc/isucon/aws-topology.json", `${JSON.stringify(topology, null, 2)}\n`)}
${file("/etc/netplan/90-isucon.yaml", routerNetplan(controlMac, dataMacs), "0600")}
${file("/etc/isucon/router.env", env, "0640", "root:isureal-router")}
${file("/etc/isucon/portal.env", portalEnvironment, "0640", "root:isureal-router")}
${file("/etc/systemd/system/isureal-portal.service", portalUnit)}
${file("/etc/isucon/envoy/bootstrap.yaml", envoy)}
${file("/etc/isucon/envoy/tls/server.crt", tlsCertificate)}
${file("/etc/isucon/envoy/tls/server.key", tlsPrivateKey, "0640", "root:isureal-router")}
${file("/usr/local/sbin/isureal-aws-network-check", interfaceSetup, "0755")}
${file("/usr/local/sbin/isureal-aws-initialize", initialize, "0750")}
${file("/etc/systemd/system/isureal-aws-initialize.service", initializeUnit)}
runcmd:
  - [systemctl, daemon-reload]
  - [systemctl, enable, --now, isureal-aws-initialize.service]
`);
}
