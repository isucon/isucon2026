// Supervisor と同じ private subnet から共有 NAT 経由で Router へ接続する。
local infra = std.parseJson(std.native('must_env')('BENCH_RUNNER_INFRA'));
{
  launchType: 'FARGATE',
  platformVersion: '1.4.0',
  networkConfiguration: {
    awsvpcConfiguration: {
      subnets: [infra.subnetId],
      securityGroups: [infra.securityGroupId],
      assignPublicIp: 'DISABLED',
    },
  },
}
