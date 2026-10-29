# Production Supervisor

This project creates an independent production benchmark pool. Pulumi owns the
cluster, execution role, security group, persistent logs, three public subnets
in the production portal VPC, and deployment configuration. Ecspresso owns the
ECS service and task definition. The development pool is not modified.

The initial service runs three ARM64 Fargate tasks, each with 4 vCPU and 8 GiB.
Each task receives a public IP and reaches participant Routers directly without
the portal NAT. The service uses a digest-pinned image containing the photo
archive, the large dataset, a 300-second benchmark timeout, and a 310-second
hard timeout. It polls all teams and AZs on the production portal. Jobs must
include a registered Router and Host. No participant AMI settings are managed
here.

Only the execution role can fetch the production application secret. The task
receives its `ISUXPORTAL_BENCH_TOKEN` JSON key through ECS secrets; neither the
token nor any other secret value belongs in stack configuration. The worker has
no task IAM role. CloudWatch logs never expire and survive stack deletion.

## Deploy

Install dependencies with `npm ci --prefix provisioning/aws`. Use a separate S3
backend prefix for this project, and a `production` stack. Restore the private
`Pulumi.production.yaml` configuration into this directory; the example
documents the required non-secret resource references. Set `PULUMI_BACKEND_URL`
explicitly to avoid inheriting the development backend from the parent mise
configuration.

The applied stack configuration is also retained in the Pulumi backend. If the
local stack file is missing, select the backend and stack, then restore it with
`pulumi config refresh --stack production --force`. The deployment evidence and
a copy of the stack file have been prepared locally for the private operations
repository; they have not been pushed there yet.

```sh
export PULUMI_BACKEND_URL=s3://isucon2026-pulumi-state-124502390568/production-supervisor
pulumi stack select production
pulumi preview --diff
pulumi up
pulumi stack output --json | python3 ../pool/render.py generated
cd generated/isucon2026-production-supervisor
ecspresso --config ecspresso.json diff
ecspresso --config ecspresso.json deploy
```

For initial creation, initialize the stack using the organization's KMS secrets
provider before applying configuration. The renderer only writes deployment
files from Pulumi outputs; it does not copy roles or secret references from the
development service. Do not run the pool renderer's `config` command here.

After deployment, verify desired/running counts of three, pending zero,
completed rollout, correct image/ARM64, public IPs, and successful production
portal polling in each task's log stream. A live benchmark requires a separately
provisioned competition environment. Before subsequent deployment, drain running
jobs: ECS allows only 120 seconds for task shutdown, less than the full
benchmark budget.

Validate changes with `npm --prefix provisioning/aws run typecheck` and
`npm --prefix provisioning/aws test -- supervisor/production/infrastructure.test.ts`.

## Deployment record: 2026-10-29

The production stack and service were created with Pulumi and ecspresso 2.8.7.
Desired/running counts are three, pending is zero, and rollout is completed. One
ARM64 task runs in each of `ap-northeast-1a`, `ap-northeast-1c`, and
`ap-northeast-1d`, each with a public IP. Every task successfully polls the
production portal with its production token. The image matches the
photograph-containing development pool digest pinned in the private stack
configuration. No benchmark was submitted; production end-to-end benchmarking
remains unverified until competition environments are prepared.

Pulumi `preview --expect-no-changes` succeeded and ecspresso `diff` was empty.
Availability-zone rebalancing is explicitly disabled because ECS rejects it with
the stop-before-start deployment maximum of 100 percent. The initial task
placement across all three AZs was verified separately.
