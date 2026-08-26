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

export function routerMediatedIngressCidrs(
  destination: ServerNetwork,
): string[] {
  return [
    `${destination.routerIp}/32`,
    ...serverNetworks
      .filter((source) => source.id !== destination.id)
      .map((source) => source.vpcCidr),
  ];
}

export interface PublicIpIsolationRule {
  protocol: "-1";
  ruleNo: number;
  action: "allow" | "deny";
  cidrBlock: string;
  fromPort: 0;
  toPort: 0;
}

export function publicIpIsolationRules(
  blockedPublicIps: readonly string[],
): PublicIpIsolationRule[] {
  return [
    ...blockedPublicIps.map((address, index) => ({
      protocol: "-1" as const,
      ruleNo: 100 + index * 10,
      action: "deny" as const,
      cidrBlock: `${address}/32`,
      fromPort: 0 as const,
      toPort: 0 as const,
    })),
    {
      protocol: "-1",
      ruleNo: 1000,
      action: "allow",
      cidrBlock: "0.0.0.0/0",
      fromPort: 0,
      toPort: 0,
    },
  ];
}
