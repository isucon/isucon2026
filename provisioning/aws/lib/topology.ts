export type NodeId = "server1" | "server2" | "server3";

export interface ServerNetwork {
  id: NodeId;
  vpcCidr: string;
  serviceSubnetCidr: string;
  routerSubnetCidr: string;
  serviceIp: string;
  routerIp: string;
}

export const controlSubnetCidr = "192.168.60.64/28";
export const controlRouterIp = "192.168.60.69";

export const serverNetworks: readonly ServerNetwork[] = [
  {
    id: "server1",
    vpcCidr: "192.168.60.0/24",
    serviceSubnetCidr: "192.168.60.0/28",
    routerSubnetCidr: "192.168.60.16/28",
    serviceIp: "192.168.60.6",
    routerIp: "192.168.60.21",
  },
  {
    id: "server2",
    vpcCidr: "192.168.61.0/24",
    serviceSubnetCidr: "192.168.61.0/28",
    routerSubnetCidr: "192.168.61.16/28",
    serviceIp: "192.168.61.6",
    routerIp: "192.168.61.21",
  },
  {
    id: "server3",
    vpcCidr: "192.168.62.0/24",
    serviceSubnetCidr: "192.168.62.0/28",
    routerSubnetCidr: "192.168.62.16/28",
    serviceIp: "192.168.62.6",
    routerIp: "192.168.62.21",
  },
] as const;
