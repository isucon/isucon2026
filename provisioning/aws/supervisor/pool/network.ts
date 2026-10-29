import * as aws from "@pulumi/aws";

export interface PoolNetworkConfig {
	vpcId: string;
	internetGatewayId: string;
	subnets: { availabilityZone: string; cidrBlock: string }[];
}

export function createPoolNetwork(config: PoolNetworkConfig) {
	const routes = new aws.ec2.RouteTable(
		"pool-public-routes",
		{
			vpcId: config.vpcId,
			routes: [{ cidrBlock: "0.0.0.0/0", gatewayId: config.internetGatewayId }],
		},
		{ aliases: [{ name: "pool-private-routes" }] },
	);
	return config.subnets.map((spec) => {
		const subnet = new aws.ec2.Subnet(`pool-${spec.availabilityZone}`, {
			...spec,
			vpcId: config.vpcId,
			mapPublicIpOnLaunch: false,
			tags: { Name: `supervisor-pool-${spec.availabilityZone}` },
		});
		new aws.ec2.RouteTableAssociation(`pool-${spec.availabilityZone}`, {
			subnetId: subnet.id,
			routeTableId: routes.id,
		});
		return subnet.id;
	});
}
