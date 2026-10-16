local infra = import 'infra.json';
local service = std.native('must_env')('SERVICE');
local digest = std.native('must_env')('IMAGE_DIGEST');
assert std.length(digest) == 71 && std.substr(digest, 0, 7) == 'sha256:' && std.length(std.findSubstr(':', digest)) == 1 : 'IMAGE_DIGEST must be a sha256 digest';
assert std.length([c for c in std.stringChars(std.substr(digest, 7, 64)) if !std.member(std.stringChars('0123456789abcdef'), c)]) == 0 : 'invalid digest hex';
{
  family: infra.name + '-' + service,
  networkMode: 'awsvpc',
  requiresCompatibilities: ['FARGATE'],
  cpu: '512',
  memory: '1024',
  runtimePlatform: { cpuArchitecture: 'ARM64', operatingSystemFamily: 'LINUX' },
  executionRoleArn: infra.executionRoles[service],
  taskRoleArn: infra.taskRoles[service],
  volumes: [{ name: 'tmp' }],
  containerDefinitions: [{
    name: service,
    image: infra.repository + '@' + digest,
    command: [service],
    essential: true,
    user: '1000',
    stopTimeout: 60,
    readonlyRootFilesystem: true,
    linuxParameters: { initProcessEnabled: true },
    mountPoints: [{ sourceVolume: 'tmp', containerPath: '/tmp', readOnly: false }],
    portMappings: if service == 'portal' then [{ containerPort: 8081, hostPort: 8081, protocol: 'tcp' }] else [],
    environment: [{ name: key, value: infra.environment[key] } for key in std.objectFields(infra.environment)],
    secrets: [
      { name: 'PGUSER', valueFrom: infra.databaseSecretArn + ':username::' },
      { name: 'PGPASSWORD', valueFrom: infra.databaseSecretArn + ':password::' },
    ] + (if service == 'migrate' then [] else [
           { name: key, valueFrom: infra.applicationSecretArn + ':' + key + '::' }
           for key in infra.secretKeys
           if key != 'ISUXPORTAL_DISCORD_BOT_TOKEN' || service == 'worker' || (service == 'bot' && infra.enableBot)
         ]) + (if service == 'worker' then [{ name: 'ISUXPORTAL_ADMIN_SSH_PRIVATE_KEY', valueFrom: infra.sshSecretArn }] else []),
    logConfiguration: {
      logDriver: 'awslogs',
      options: {
        'awslogs-group': infra.logGroup,
        'awslogs-region': infra.region,
        'awslogs-stream-prefix': service,
        mode: 'non-blocking',
        'max-buffer-size': '25m',
      },
    },
  }],
}
