// ECS サービスは作らず、ecspresso run で単発のタスクとして動かす。
// service_definition はネットワーク設定を渡すためだけに使う。
local infra = std.parseJson(std.native('must_env')('BENCH_RUNNER_INFRA'));
{
  region: infra.region,
  cluster: infra.cluster,
  service_definition: 'service.jsonnet',
  task_definition: 'task.jsonnet',
  timeout: '20m',
}
