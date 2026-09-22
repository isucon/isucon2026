import {
  controlRouterIp,
  controlSubnetCidr,
  ServerNetwork,
  serverNetworks,
} from "./topology";

export interface RuntimeNetwork {
  cidr: string;
  routerAddress: string;
  peerAddress: string;
  serviceSubnetCidr?: string;
  routerSubnetCidr?: string;
}

export interface RuntimeTopology {
  schemaVersion: number;
  environmentId: string;
  baseCidr: string;
  domain: string;
  portal: {
    mode: string;
    endpoint: string;
    teamNumber: number;
    teamHash: string;
  };
  clientTlsForward: { hostPort: number };
  nodes: Record<string, { role: string }>;
  networks: Record<string, RuntimeNetwork>;
}

export function buildRuntimeTopology(
  portalEndpoint: string,
  domain: string,
): RuntimeTopology {
  return {
    schemaVersion: 3,
    environmentId: "isureal-aws-validation",
    baseCidr: "192.168.0.0/16",
    domain,
    portal: {
      mode: "external",
      endpoint: portalEndpoint,
      teamNumber: 1,
      teamHash: "aws",
    },
    clientTlsForward: { hostPort: 443 },
    nodes: {
      server1: { role: "problem-server" },
      server2: { role: "problem-server" },
      server3: { role: "problem-server" },
      router: { role: "router" },
    },
    networks: {
      ...Object.fromEntries(
        serverNetworks.map((network) => [
          `${network.id}Data`,
          {
            cidr: network.vpcCidr,
            serviceSubnetCidr: network.serviceSubnetCidr,
            routerSubnetCidr: network.routerSubnetCidr,
            routerAddress: network.routerIp,
            peerAddress: network.serviceIp,
          },
        ]),
      ),
      control: {
        cidr: controlSubnetCidr,
        serviceSubnetCidr: controlSubnetCidr,
        routerAddress: controlRouterIp,
        peerAddress: portalEndpoint,
      },
    },
  };
}

function firstUsable(cidr: string): string {
  const [address] = cidr.split("/");
  const octets = address.split(".").map(Number);
  octets[3] += 1;
  return octets.join(".");
}

export function routerNetplan(
  controlMac: string,
  dataMacs: readonly string[],
): string {
  if (dataMacs.length !== serverNetworks.length) {
    throw new Error("Router requires exactly three data ENIs");
  }
  const data = serverNetworks
    .map(
      (network, index) => `    ${network.id}:
      match:
        macaddress: ${dataMacs[index].toLowerCase()}
      set-name: ${network.id}
      dhcp4: false
      addresses:
        - ${network.routerIp}/${network.routerSubnetCidr.split("/")[1]}
      routes:
        - to: ${network.serviceSubnetCidr}
          via: ${firstUsable(network.routerSubnetCidr)}
          on-link: true`,
    )
    .join("\n");
  return `network:
  version: 2
  ethernets:
    control0:
      match:
        macaddress: ${controlMac.toLowerCase()}
      set-name: control0
      dhcp4: true
      dhcp4-overrides:
        use-routes: true
${data}
`;
}

export function routerEnvironment(topology: RuntimeTopology): string {
  const portal = new URL(topology.portal.endpoint);
  const lines = [
    `ROUTER_PORTAL_ENDPOINT=${topology.portal.endpoint}`,
    `ROUTER_TEAM_NUMBER=${topology.portal.teamNumber}`,
    "ROUTER_XDS_LISTEN_ADDRESS=127.0.0.1:18000",
    `ROUTER_STATUS_LISTEN_ADDRESS=${topology.networks.control.routerAddress}:18081`,
    "ROUTER_ENVOY_NODE_ID=isureal-router-envoy",
    "ROUTER_ROUTE_CONFIG_NAME=isureal_routes",
    `ROUTER_APP_HOST=app.team1-aws.${topology.domain}`,
  ];
  serverNetworks.forEach((network, index) => {
    const number = index + 1;
    const data = topology.networks[`${network.id}Data`];
    lines.push(
      `ROUTER_SERVER${number}_HOST=${network.id}.team1-aws.${topology.domain}`,
      `ROUTER_SERVER${number}_SUBNET=${data.cidr}`,
      `ROUTER_SERVER${number}_REGION_LOCAL_IP=${data.routerAddress}`,
      `ROUTER_SERVER${number}_IP=${data.peerAddress}`,
    );
  });
  if (!["http:", "https:"].includes(portal.protocol)) {
    throw new Error("Portal endpoint must use HTTP or HTTPS");
  }
  return `${lines.join("\n")}\n`;
}
