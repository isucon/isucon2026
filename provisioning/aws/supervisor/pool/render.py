"""共有プールの Supervisor の ecspresso 用の定義を作る。Pulumi が設定を保持し、ecspresso が配備する。

  python3 render.py build pool.json task.json service.json   pool.json と雛形（稼働中のタスク定義とサービス）から
                                                             deployments（1 件の配列）の JSON を標準出力に書く
  python3 render.py config pool.json [stack]                 雛形を AWS から取り、build の結果を stack の設定 deployments に書く
  python3 render.py generated                                pulumi stack output の JSON を標準入力から読み、
                                                             generated/<service>/ に ecspresso の JSON を書く

出どころは isucon/isucon2026-private の ops/shared-supervisor/render.py（README を参照）。
"""

import copy
import json
import pathlib
import re
import subprocess
import sys
from urllib.parse import urlparse

IMAGE = re.compile(r"[A-Za-z0-9./:_-]+@sha256:[a-f0-9]{64}")
DURATION = re.compile(r"[0-9]+(ms|s|m|h)")
# ベンチの -images（イメージ内の絶対パスか https の URL）
IMAGES = re.compile(r"/[A-Za-z0-9._/-]+|https://[A-Za-z0-9._~:/?#@!$&'()*+,;=%-]+")


def build(task, service, config):
    """雛形のタスク定義 task とサービス service に config（pool.json）を当てて、ecspresso 用の定義を返す。

    雛形からは CPU、メモリ、実行 role、コンテナ定義（ログ、secrets、ulimit など）とネットワーク設定を写し、
    イメージ、環境変数、ベンチの起動コマンドを config から作る。雛形は変更しない。
    """
    if not IMAGE.fullmatch(config["image"]):
        raise ValueError("image must be pinned")
    url = urlparse(config["endpoint"])
    if url.scheme != "https" or not url.hostname or url.username or url.password:
        raise ValueError("endpoint must be HTTPS without credentials")
    if not re.fullmatch(r"[A-Za-z0-9_-]+", config["name"]):
        raise ValueError("invalid service name")
    if not isinstance(config["desiredCount"], int) or config["desiredCount"] < 1:
        raise ValueError("desiredCount must be positive")
    bench_timeout = config.get("benchTimeout")
    if bench_timeout is not None and not DURATION.fullmatch(bench_timeout):
        raise ValueError("benchTimeout must be a Go duration such as 300s")
    hard_timeout = config.get("hardTimeout")
    if hard_timeout is not None and not re.fullmatch(r"[0-9]+", hard_timeout):
        raise ValueError("hardTimeout must be seconds")
    bench_images = config.get("benchImages")
    if bench_images is not None and not IMAGES.fullmatch(bench_images):
        raise ValueError(
            "benchImages must be an absolute path in the image or an HTTPS URL"
        )
    bench_args = " --target-port 8080 --scale large"
    if bench_timeout is not None:
        bench_args += f" --timeout {bench_timeout}"
    keys = [
        "cpu",
        "memory",
        "networkMode",
        "requiresCompatibilities",
        "executionRoleArn",
        "taskRoleArn",
        "containerDefinitions",
    ]
    result = {k: copy.deepcopy(task[k]) for k in keys if k in task}
    result["runtimePlatform"] = {
        "cpuArchitecture": "ARM64",
        "operatingSystemFamily": "LINUX",
    }
    result["family"] = config["name"]
    c = result["containerDefinitions"][0]
    c["image"] = config["image"]
    env = {e["name"]: e["value"] for e in c.get("environment", [])}
    # 共有プールはチームも AZ も絞らない
    env.pop("ISUXPORTAL_SUPERVISOR_TEAM_ID", None)
    env["ISUXPORTAL_SUPERVISOR_INSTANCE_NAME"] = ""
    env["ISUXPORTAL_SUPERVISOR_ENDPOINT_URL"] = config["endpoint"]
    if hard_timeout is not None:
        env["ISUXPORTAL_SUPERVISOR_HARD_TIMEOUT"] = hard_timeout
    # 子プロセスのベンチが写真のアーカイブの場所として読む（bench -images の既定値）
    env.pop("ISUXBENCH_IMAGES", None)
    if bench_images is not None:
        env["ISUXBENCH_IMAGES"] = bench_images
    c["environment"] = [{"name": k, "value": v} for k, v in sorted(env.items())]
    script = (
        # Router IP and Host come from the portal registration, never a static map.
        "set -eu\n"
        + 'if [ "${1:-}" != "--host" ] || [ -z "${2:-}" ]; then echo "Router registration required" >&2; exit 2; fi\n'
        + f'exec /usr/local/bin/isureal-bench "$@"{bench_args}\n'
    )
    c["command"] = ["/bin/sh", "-c", script, "bench-proxy"]
    return {
        "taskDefinition": result,
        "serviceDefinition": {
            "desiredCount": config["desiredCount"],
            "propagateTags": "NONE",
            "healthCheckGracePeriodSeconds": 0,
            "launchType": "FARGATE",
            "platformVersion": "1.4.0",
            "deploymentConfiguration": {
                "minimumHealthyPercent": 0,
                "maximumPercent": 100,
                "deploymentCircuitBreaker": {"enable": True, "rollback": True},
            },
            "networkConfiguration": copy.deepcopy(service["networkConfiguration"]),
        },
        "ecspressoConfig": {
            "region": "ap-northeast-1",
            "cluster": service["clusterArn"],
            "service": config["name"],
            "task_definition": "task-definition.json",
            "service_definition": "service-definition.json",
            "timeout": "10m",
        },
    }


def export(deployments, directory):
    for deployment in deployments:
        root = pathlib.Path(directory) / deployment["ecspressoConfig"]["service"]
        root.mkdir(parents=True, exist_ok=True)
        for name, key in [
            ("task-definition.json", "taskDefinition"),
            ("service-definition.json", "serviceDefinition"),
            ("ecspresso.json", "ecspressoConfig"),
        ]:
            (root / name).write_text(json.dumps(deployment[key], indent=2) + "\n")


def load(path):
    with open(path) as f:
        return json.load(f)


def templates(config):
    """稼働中のタスク定義（family の最新の ACTIVE）とサービスを雛形として AWS から取る。"""
    region = "ap-northeast-1"
    task = json.loads(
        subprocess.run(
            [
                "aws",
                "ecs",
                "describe-task-definition",
                "--region",
                region,
                "--task-definition",
                config["name"],
                "--query",
                "taskDefinition",
                "--output",
                "json",
            ],
            check=True,
            capture_output=True,
            text=True,
        ).stdout
    )
    cluster = subprocess.run(
        [
            "aws",
            "ecs",
            "list-clusters",
            "--region",
            region,
            "--query",
            "clusterArns",
            "--output",
            "json",
        ],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    cluster = next(arn for arn in json.loads(cluster) if "/supervisor-cluster-" in arn)
    service = json.loads(
        subprocess.run(
            [
                "aws",
                "ecs",
                "describe-services",
                "--region",
                region,
                "--cluster",
                cluster,
                "--services",
                config["name"],
                "--query",
                "services[0]",
                "--output",
                "json",
            ],
            check=True,
            capture_output=True,
            text=True,
        ).stdout
    )
    if not service or service.get("status") != "ACTIVE":
        raise SystemExit(f"service {config['name']} is not active in {cluster}")
    return task, service


def main(argv):
    if argv[:1] == ["build"] and len(argv) == 4:
        config = load(argv[1])
        print(json.dumps([build(load(argv[2]), load(argv[3]), config)], indent=2))
    elif argv[:1] == ["config"] and len(argv) in (2, 3):
        config = load(argv[1])
        task, service = templates(config)
        value = json.dumps([build(task, service, config)])
        stack = argv[2] if len(argv) == 3 else "portal-dev"
        subprocess.run(
            ["pulumi", "config", "set", "--stack", stack, "--plaintext", "deployments"],
            check=True,
            input=value,
            text=True,
            cwd=pathlib.Path(__file__).parent,
        )
        print(
            f"deployments of stack {stack} updated from {argv[1]} (task definition {task['family']}:{task['revision']})"
        )
    elif len(argv) == 1:
        export(json.load(sys.stdin)["deployments"], argv[0])
    else:
        raise SystemExit(__doc__)


if __name__ == "__main__":
    main(sys.argv[1:])
