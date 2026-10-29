import * as pulumi from "@pulumi/pulumi";
import { createPoolNetwork, type PoolNetworkConfig } from "./network";

// 共有プールの ecspresso 用の定義（タスク定義、サービス定義、ecspresso の設定）を stack の設定に保持し、
// 専用ネットワークがある場合は subnet ID を反映し、render.py が出力から ecspresso の JSON を書く。
const config = new pulumi.Config();
const network = config.getObject<PoolNetworkConfig>("network");
export const subnetIds = network ? createPoolNetwork(network) : [];
interface Deployment {
	serviceDefinition: {
		networkConfiguration: { awsvpcConfiguration: { subnets: string[] } };
	};
}
export const deployments = pulumi.all(subnetIds).apply((ids) =>
	config.requireObject<Deployment[]>("deployments").map((deployment) => {
		if (network) {
			deployment.serviceDefinition.networkConfiguration.awsvpcConfiguration.subnets =
				ids;
		}
		return deployment;
	}),
);
