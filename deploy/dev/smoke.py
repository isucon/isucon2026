#!/usr/bin/env python3
"""Exercise the running development stack without resetting its database."""
import json
import pathlib
import statistics
import subprocess
import time

ROOT = pathlib.Path(__file__).resolve().parents[2]
COMPOSE = ["docker", "compose", "-f", str(ROOT / "compose.yaml")]


def run(*args):
    return subprocess.check_output(COMPOSE + list(args), text=True)


def request(host, country, path, method="GET", body=None, token=None):
    args = ["exec", "-T", "app-asia", "curl", "-sS", "--max-time", "45",
            "-w", "\n%{http_code}\n%{time_total}",
            "-H", "Host: " + host, "-H", "X-Country-Code: " + country,
            "-X", method]
    if token:
        args += ["-H", "Authorization: Bearer " + token]
    if body is not None:
        args += ["-H", "Content-Type: application/json", "--data", json.dumps(body)]
    output = run(*args, "http://172.29.1.2:8080" + path)
    data, status, duration = output.rsplit("\n", 2)
    return data, int(status), float(duration)


ready = run("exec", "-T", "router", "wget", "-qO-", "http://127.0.0.1:18081/readyz")
print("router ready:", ready.strip())
for index, region, country in [(1, "asia", "JP"), (2, "europe", "DE"), (3, "america", "US")]:
    data, status, _ = request("app.127.0.0.1.nip.io", country, "/")
    assert status == 200 and "<html" in data.lower(), (region, status, data)
    username = "compose_" + region + "_" + str(time.time_ns())
    data, status, _ = request("app.127.0.0.1.nip.io", country, "/api/auth/register", "POST",
                              {"username": username, "displayName": region, "region": region})
    assert status == 201, (region, status, data)
    secret = json.loads(data)["secretKey"]
    data, status, _ = request("app.127.0.0.1.nip.io", country, "/api/auth/login", "POST",
                              {"username": username, "secretKey": secret})
    assert status == 200, (region, status, data)
    token = json.loads(data)["token"]
    data, status, _ = request("app.127.0.0.1.nip.io", country, "/api/me", token=token)
    assert status == 200 and json.loads(data)["region"] == region, (region, status, data)
    wrong = "europe.127.0.0.1.nip.io" if region == "asia" else "asia.127.0.0.1.nip.io"
    data, status, _ = request(wrong, country, "/api/me", token=token)
    assert status == 403 and json.loads(data)["error"] == "region_mismatch", (region, status, data)
    data, status, _ = request("app.127.0.0.1.nip.io", country, "/api/auth/logout", "POST", token=token)
    assert status == 204, (region, status, data)
    print(region + ": frontend, register, login, region gate, logout PASS", flush=True)

for host in ["asia.127.0.0.1.nip.io", "europe.127.0.0.1.nip.io"]:
    samples = []
    for _ in range(3):
        data, status, elapsed = request(host, "JP", "/healthz")
        assert status == 200, (status, data)
        samples.append(elapsed)
    print(host + " from JP median seconds:", round(statistics.median(samples), 3), flush=True)
for app in ["app-asia", "app-europe", "app-america"]:
    print(app + " -> DB ping:")
    print(run("exec", "-T", app, "ping", "-c", "3", "172.29.4.10").strip())
for service in ["app-asia", "app-europe", "app-america", "db-asia"]:
    cid = run("ps", "-q", service).strip()
    info = json.loads(subprocess.check_output(["docker", "inspect", cid], text=True))[0]
    limits = info["HostConfig"]
    assert limits["NanoCpus"] > 0 and limits["Memory"] > 0, service
    print(service, "CPUs:", limits["NanoCpus"] / 1e9, "memory:", limits["Memory"])
print("development stack smoke PASS")
