# EC2 CLI benchmark

This stack creates one `c7i.xlarge` EC2 instance with Docker and a digest-pinned
benchmark image. Access uses AWS Systems Manager; no inbound ports or portal
credentials are configured. The benchmark connects to the Router on TCP/8080
using its private IP and an application Host header.

```bash
aws --profile isucon2026 --region ap-northeast-1 ssm start-session \
  --target i-0057610e0acc5b38c
sudo /usr/local/bin/isureal-bench \
  --target 192.168.11.10 --target-port 8080 \
  --host app.team1-aws.127.0.0.1.nip.io --scale large
```

Use the `app` Host to route each user to its matching region. The fixed `app1`,
`app2`, and `app3` Hosts force one backend and are unsuitable for the full
multi-region benchmark. The default load duration is 60 seconds; stdout reports
`pass` and `score` as JSON. CLI results are not submitted to the portal.

Deployment: project `isucon2026-benchmark-ec2`, stack `trial`, backend
`s3://isucon2026-pulumi-state-124502390568/benchmark-ec2`. Instance
`i-0057610e0acc5b38c`, private IP `192.168.11.174`, public IP `52.195.171.99`.
Copy `Pulumi.trial.yaml.example` to `Pulumi.trial.yaml` when recreating the
stack. Set `AWS_PROFILE=isucon2026` and `PULUMI_CONFIG_PASSPHRASE=''`, then use
`pulumi preview` and `pulumi up`. The existing Fargate services remain deployed.

## Verified result

On 2026-10-04 JST, the default 60-second load against team1 with `--scale large`
completed with `pass: true`, `score: 16305`, and language `go`. SSM command ID:
`4a737291-6de0-418a-bd57-197c4637b512`. Result and diagnostic logs are on the
EC2 instance at `/var/log/isureal-bench/team1-large.json` and
`/var/log/isureal-bench/team1-large.log`. The benchmark and dataset sources
match origin/main `acf0addb8fdfeb69e7631c6c2009c5e3c5a422f7`. The initial
attempt used the small dataset and a fixed regional backend Host; it failed
initial validation. Using the large dataset and the region-routing `app` Host
resolved that mismatch without application changes. IaC tests (13), TypeScript
checks, and post-deployment preview (8 unchanged resources) passed.

## Team 1–3 configuration

| Team | Pulumi stack | Router private IP | HTTP Host                        | Benchmark EC2              |
| ---- | ------------ | ----------------- | -------------------------------- | -------------------------- |
| 1    | `trial`      | `192.168.11.10`   | `app.team1-aws.127.0.0.1.nip.io` | `i-0057610e0acc5b38c`      |
| 2    | `team2`      | `192.168.27.10`   | `app.team2-aws.127.0.0.1.nip.io` | Pending EC2 quota increase |
| 3    | `team3`      | `192.168.43.10`   | `app.team3-aws.127.0.0.1.nip.io` | Pending EC2 quota increase |

Run the following command on each team's benchmark EC2, substituting its Router
IP and HTTP Host from the table:

```bash
sudo /usr/local/bin/isureal-bench \
  --target 192.168.27.10 --target-port 8080 \
  --host app.team2-aws.127.0.0.1.nip.io --scale large
```

Configuration examples for the additional stacks are `Pulumi.team2.yaml.example`
and `Pulumi.team3.yaml.example`. Both stack previews passed; team2 created its
IAM and networking resources but EC2 creation hit the on-demand vCPU quota. The
current regional quota is 30; three teams' five 2-vCPU servers plus three 4-vCPU
benchmark instances require 42 vCPUs. A quota increase to 48 was requested:
request ID `6838ada7903a4812a4b7c5cae84de50dWyZTt7gm`, AWS Support case
`179104930600692`. Team2 and team3's app1–3 and DB instances were started; their
Router instances could not start due to the same quota. Team2 and team3 have not
completed a benchmark yet.

After quota approval, start Router instances `i-0449527294342d8ce` and
`i-0ed6cfe9487aaa637`, then deploy the `team2` and `team3` benchmark stacks and
verify their scores. Starting the existing application instances also changes
their public IPs; CLI benchmark routing uses the stable private Router IPs.
