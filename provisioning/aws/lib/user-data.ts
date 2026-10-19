import {
	appHttpPort,
	databaseIp,
	type ServerNetwork,
	type Team,
	vpcCidr,
} from "./topology";

// アドレスはチームごとに違うので、AMI には焼き込まず起動時に設定する
export function serviceUserData(t: Team, network: ServerNetwork): string {
	const commands =
		network.region === undefined
			? ["systemctl enable --now postgresql"]
			: [
					`sed -i -e 's/^ISUREAL_REGION=.*/ISUREAL_REGION=${network.region}/' -e 's/^ISUREAL_DB_HOST=.*/ISUREAL_DB_HOST=${databaseIp(t)}/' /home/isucon/env.sh`,
					"systemctl enable --now isureal-go isureal-guard nginx",
				];
	return `#!/bin/sh
set -eu
${commands.join("\n")}
`;
}

function file(
	path: string,
	content: string,
	mode: string,
	owner = "root:root",
): string {
	return `  - path: ${path}
    encoding: b64
    permissions: "${mode}"
    owner: ${owner}
    content: ${Buffer.from(content, "utf8").toString("base64")}`;
}

function firstUsable(cidr: string): string {
	const octets = cidr.split("/")[0].split(".").map(Number);
	octets[3] += 1;
	return octets.join(".");
}

// パブリック用 ENI は cloud-init の既定の設定（DHCP）に任せ、サーバーのサブネット宛てだけを
// データ用 ENI から VPC へ送る
export function routerNetplan(t: Team, dataMac: string): string {
	const routes = t.servers
		.map(
			(network) => `        - to: ${network.subnetCidr}
          via: ${firstUsable(t.routerDataSubnetCidr)}`,
		)
		.join("\n");
	return `network:
  version: 2
  ethernets:
    data0:
      match:
        macaddress: ${dataMac.toLowerCase()}
      set-name: data0
      dhcp4: false
      addresses:
        - ${t.routerDataIp}/${t.routerDataSubnetCidr.split("/")[1]}
      routes:
${routes}
`;
}

export function routerEnvironment(t: Team): string {
	const lines = [
		"ROUTER_PORTAL_ENDPOINT=http://127.0.0.1:18082",
		`ROUTER_TEAM_NUMBER=${t.number}`,
		"ROUTER_XDS_LISTEN_ADDRESS=127.0.0.1:18000",
		`ROUTER_STATUS_LISTEN_ADDRESS=${t.routerPublicIp}:18081`,
		"ROUTER_ENVOY_NODE_ID=isureal-router-envoy",
		"ROUTER_ROUTE_CONFIG_NAME=isureal_routes",
		`ROUTER_APP_HOST=app.${t.domain}`,
	];
	for (const server of t.servers) {
		const prefix = `ROUTER_${server.id.toUpperCase()}`;
		if (server.region !== undefined) {
			lines.push(`${prefix}_HOST=${server.id}.${t.domain}`);
		}
		lines.push(
			`${prefix}_SUBNET=${server.subnetCidr}`,
			`${prefix}_REGION_LOCAL_IP=${t.routerDataIp}`,
			`${prefix}_IP=${server.serviceIp}`,
		);
	}
	return `${lines.join("\n")}\n`;
}

export function portalEnvironment(t: Team): string {
	return `PORTAL_TEAM_NUMBER=${t.number}
PORTAL_TEAM_HASH=aws
PORTAL_DOMAIN_SUFFIX=${t.domain.slice(t.domain.indexOf(".") + 1)}
PORTAL_ROUTER_MODE=external-router
PORTAL_LISTEN_ADDRESS=127.0.0.1:18082
PORTAL_WEB_DIR=
`;
}

const typed = (type: string, fields: object = {}) => ({
	"@type": `type.googleapis.com/${type}`,
	...fields,
});

function edgeListener(name: string, port: number, tls: boolean): object {
	const [address, prefix] = vpcCidr.split("/");
	const connectionManager = {
		name: "envoy.filters.network.http_connection_manager",
		typed_config: typed(
			"envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
			{
				stat_prefix: name,
				codec_type: "AUTO",
				request_timeout: "30s",
				normalize_path: true,
				merge_slashes: true,
				internal_address_config: {
					cidr_ranges: [
						{ address_prefix: address, prefix_len: Number(prefix) },
					],
				},
				rds: {
					route_config_name: "isureal_routes",
					config_source: {
						ads: {},
						resource_api_version: "V3",
						initial_fetch_timeout: "15s",
					},
				},
				http_filters: [
					{
						name: "envoy.filters.http.fault",
						typed_config: typed(
							"envoy.extensions.filters.http.fault.v3.HTTPFault",
						),
					},
					{
						name: "envoy.filters.http.bandwidth_limit",
						typed_config: typed(
							"envoy.extensions.filters.http.bandwidth_limit.v3.BandwidthLimit",
							{ stat_prefix: `${name}_bandwidth`, enable_mode: "DISABLED" },
						),
					},
					{
						name: "envoy.filters.http.router",
						typed_config: typed(
							"envoy.extensions.filters.http.router.v3.Router",
						),
					},
				],
			},
		),
	};
	const transportSocket = {
		name: "envoy.transport_sockets.tls",
		typed_config: typed(
			"envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext",
			{
				common_tls_context: {
					tls_certificates: [
						{
							certificate_chain: {
								filename: "/etc/isucon/envoy/tls/server.crt",
							},
							private_key: { filename: "/etc/isucon/envoy/tls/server.key" },
						},
					],
				},
			},
		),
	};
	return {
		name,
		address: { socket_address: { address: "0.0.0.0", port_value: port } },
		filter_chains: [
			{
				...(tls ? { transport_socket: transportSocket } : {}),
				filters: [connectionManager],
			},
		],
	};
}

function staticCluster(
	name: string,
	address: string,
	port: number,
	extra: object = {},
): object {
	return {
		name,
		type: "STATIC",
		...extra,
		load_assignment: {
			cluster_name: name,
			endpoints: [
				{
					lb_endpoints: [
						{
							endpoint: {
								address: { socket_address: { address, port_value: port } },
							},
						},
					],
				},
			],
		},
	};
}

// YAML は JSON の上位互換なので、bootstrap.yaml として JSON を書く
export function envoyBootstrap(t: Team): string {
	const bootstrap = {
		node: { cluster: "isureal-router", id: "isureal-router-envoy" },
		admin: {
			address: { socket_address: { address: "127.0.0.1", port_value: 9901 } },
		},
		dynamic_resources: {
			ads_config: {
				api_type: "GRPC",
				transport_api_version: "V3",
				grpc_services: [{ envoy_grpc: { cluster_name: "xds_cluster" } }],
			},
		},
		static_resources: {
			listeners: [
				edgeListener("edge_http", 8080, false),
				edgeListener("edge_https", 443, true),
			],
			clusters: [
				staticCluster("xds_cluster", "127.0.0.1", 18000, {
					connect_timeout: "1s",
					typed_extension_protocol_options: {
						"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": typed(
							"envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
							{ explicit_http_config: { http2_protocol_options: {} } },
						),
					},
				}),
				...t.servers
					.filter((server) => server.region !== undefined)
					.map((server) =>
						staticCluster(server.id, server.serviceIp, appHttpPort, {
							connect_timeout: "2s",
							circuit_breakers: {
								thresholds: [
									{
										priority: "DEFAULT",
										max_connections: 100000,
										max_pending_requests: 100000,
										max_requests: 100000,
									},
								],
							},
						}),
					),
			],
		},
	};
	return `${JSON.stringify(bootstrap, null, 2)}\n`;
}

// データ用 ENI は起動後に attach されるので、アドレスが載るまで netplan を当て直してから起動する
export function routerUserData(
	t: Team,
	dataMac: string,
	tlsCertificate: string,
	tlsPrivateKey: string,
): string {
	return `#cloud-config
write_files:
${file("/etc/cloud/cloud.cfg.d/99-isucon-router-network.cfg", "network: {config: disabled}\n", "0644")}
${file("/etc/netplan/90-isucon.yaml", routerNetplan(t, dataMac), "0600")}
${file("/etc/isucon/router.env", routerEnvironment(t), "0640", "root:isureal-router")}
${file("/etc/isucon/portal.env", portalEnvironment(t), "0640", "root:isureal-router")}
${file("/etc/isucon/envoy/bootstrap.yaml", envoyBootstrap(t), "0644")}
${file("/etc/isucon/envoy/tls/server.crt", tlsCertificate, "0644")}
${file("/etc/isucon/envoy/tls/server.key", tlsPrivateKey, "0640", "root:isureal-router")}
runcmd:
  - until ip -4 -o address show dev data0 | grep -Fq ' ${t.routerDataIp}/'; do netplan apply; sleep 5; done
  - systemctl enable --now isureal-portal isureal-router isureal-envoy
`;
}
