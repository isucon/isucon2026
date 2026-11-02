local infra = import 'infra.json';
local service = std.native('must_env')('SERVICE');
local standby = std.get(infra, 'standby', false);
local singleton = std.member(['bot', 'notifyloop'], service);
{
  serviceName: service,
  desiredCount: if service == 'portal' then std.get(infra, 'portalDesiredCount', 2) else if standby && !(service == 'bot' && std.get(infra, 'enableBotInStandby', false)) then 0 else if service == 'bot' && !infra.enableBot then 0 else 1,
  launchType: 'FARGATE',
  availabilityZoneRebalancing: if singleton then 'DISABLED' else 'ENABLED',
  propagateTags: 'NONE',
  healthCheckGracePeriodSeconds: if service == 'portal' then 60 else 0,
  platformVersion: '1.4.0',
  networkConfiguration: { awsvpcConfiguration: {
    subnets: infra.subnets,
    securityGroups: [infra.securityGroup],
    assignPublicIp: 'DISABLED',
  } },
  deploymentConfiguration: {
    minimumHealthyPercent: if singleton then 0 else 100,
    maximumPercent: if singleton then 100 else 200,
    deploymentCircuitBreaker: { enable: true, rollback: true },
  },
} + (if service == 'portal' then {
       loadBalancers: [{ targetGroupArn: infra.targetGroupArn, containerName: 'portal', containerPort: 8081 }],
     } else {})
