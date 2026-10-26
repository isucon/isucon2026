// supervisor/Dockerfile のイメージから bench だけを起動し、結果を S3 の runs/<RUN_ID>/ に置く。
// bench の引数と RUN_ID は実行ごとに overrides で渡す。
local infra = std.parseJson(std.native('must_env')('BENCH_RUNNER_INFRA'));
local image = std.native('must_env')('BENCH_IMAGE');
assert std.startsWith(image, infra.repository + '@sha256:') && std.length(image) == std.length(infra.repository) + 72 : 'BENCH_IMAGE must be pinned by digest in the runner repository';

local logs(prefix) = {
  logDriver: 'awslogs',
  options: {
    'awslogs-group': infra.logGroup,
    'awslogs-region': infra.region,
    'awslogs-stream-prefix': prefix,
  },
};

// 標準出力（結果 JSON）を result.json へ、標準エラー出力（進行ログ）を bench.log と CloudWatch Logs へ送る。
// 走行中はタスクの CPU、メモリ、ネットワークを stats.tsv に記録する。
local benchScript = |||
  set -u
  sh -c "$SAMPLER_SCRIPT" sampler /results/.stop 3600 >/results/stats.tsv 2>/dev/null &
  sampler=$!
  { /usr/local/bin/isureal-bench "$@" 2>&1 >/results/result.json; echo "$?" >/results/exit-code; } | tee /results/bench.log >&2
  touch /results/.stop
  wait "$sampler"
  rm -f /results/.stop
  exit "$(cat /results/exit-code)"
|||;

local uploadScript = |||
  set -eu
  : "${RUN_ID:?}"
  aws s3 cp --recursive --no-progress /results "s3://${RESULTS_BUCKET}/runs/${RUN_ID}/"
|||;

{
  family: infra.name,
  networkMode: 'awsvpc',
  requiresCompatibilities: ['FARGATE'],
  // 本番の Supervisor と同じ大きさ
  cpu: '4096',
  memory: '8192',
  runtimePlatform: { cpuArchitecture: 'X86_64', operatingSystemFamily: 'LINUX' },
  executionRoleArn: infra.executionRoleArn,
  taskRoleArn: infra.taskRoleArn,
  volumes: [{ name: 'results', host: {} }],
  containerDefinitions: [
    {
      name: 'bench',
      image: image,
      essential: false,
      // Fargate の bind mount は root 所有で作られるため
      user: '0',
      entryPoint: ['/bin/sh', '-c', benchScript, 'isureal-bench'],
      environment: [{ name: 'SAMPLER_SCRIPT', value: importstr '../sampler.sh' }],
      linuxParameters: { initProcessEnabled: true },
      ulimits: [
        { name: name, softLimit: 100000, hardLimit: 100000 }
        for name in ['nofile', 'nproc']
      ],
      mountPoints: [{ sourceVolume: 'results', containerPath: '/results', readOnly: false }],
      logConfiguration: logs('bench'),
    },
    {
      name: 'uploader',
      image: 'public.ecr.aws/aws-cli/aws-cli:2.38.5@sha256:c8156a0e74e13929a42d7e60f0d4e14e7d28d980cd21822505d4bff80e5460c1',
      essential: true,
      dependsOn: [{ containerName: 'bench', condition: 'COMPLETE' }],
      entryPoint: ['/bin/sh', '-c', uploadScript],
      environment: [{ name: 'RESULTS_BUCKET', value: infra.bucket }],
      mountPoints: [{ sourceVolume: 'results', containerPath: '/results', readOnly: true }],
      logConfiguration: logs('uploader'),
    },
  ],
}
