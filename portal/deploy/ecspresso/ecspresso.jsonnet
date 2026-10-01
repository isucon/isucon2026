local infra = import 'infra.json';
local service = std.native('must_env')('SERVICE');
assert std.member(['portal', 'worker', 'notifyloop', 'bot', 'migrate'], service) : 'unknown SERVICE';
{
  region: infra.region,
  cluster: infra.cluster,
  service: service,
  service_definition: 'service.jsonnet',
  task_definition: 'task.jsonnet',
  timeout: '20m',
}
