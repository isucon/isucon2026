"""Persist reproducible evidence for one completed Team 12 benchmark."""
import hashlib
import json
import os
import shlex
from pathlib import Path
import shutil
import subprocess
import sys

run, label = sys.argv[1:]
source = Path("var/aws-bench") / run
result = json.loads((source / "result.json").read_text())
meta = json.loads((source / "meta.json").read_text())
assert meta["team"] == 12 and meta["exitCode"] is not None
destination = Path("webapp-optmized-astra/results") / run
destination.mkdir(parents=True, exist_ok=True)
for name in ("result.json", "meta.json", "report.txt"):
    if (source / name).exists():
        shutil.copyfile(source / name, destination / name)
        if name == "report.txt":
            report = destination / name
            report.write_text("\n".join(line.rstrip() for line in report.read_text().splitlines()) + "\n")
experiment = {
    "name": label,
    "source_parent": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
    "artifact_sha256": hashlib.sha256(Path("var/astra/release.tar.gz").read_bytes()).hexdigest(),
    "benchmark_image": meta["image"],
    "team": 12,
    "pass": result["pass"],
    "score": result["score"],
    "note": "The result commit includes source and Pulumi configuration used for this run.",
}
space = Path("var/astra/space-before-benchmark.json")
if space.exists():
    experiment["storage_before_benchmark"] = json.loads(space.read_text())
# Record the actual published archive references, not merely the migration
# history: restoring a partition can leave older reports in the repository.
root = Path(__file__).resolve().parents[2]
runtime = json.loads((root / "webapp-optmized-astra/deploy/runtime.json").read_text())
if runtime.get("signatureFileCacheEnabled"):
    cache = root / runtime["signatureFileCachePath"]
    with cache.open("rb") as file:
        header = file.read(16)
    assert header[:8] == b"ASTRAS01"
    with cache.open("rb") as file:
        digest = hashlib.file_digest(file, "sha256").hexdigest()
    experiment["signature_file_cache"] = {
        "sha256": digest,
        "expires": int.from_bytes(header[8:], "little", signed=True),
        "rows": (cache.stat().st_size - 16) // 80,
    }
query = """SELECT coalesce(json_agg(row_to_json(t)), '[]'::json) FROM (
 SELECT tableoid::regclass::text AS partition,
        split_part(asset_key, ':', 2) AS archive, count(*) AS rows
 FROM image_objects WHERE asset_key LIKE 'pack:%'
 GROUP BY tableoid, split_part(asset_key, ':', 2)
 ORDER BY tableoid::regclass::text, split_part(asset_key, ':', 2)
) t"""
try:
    published = json.loads(subprocess.check_output([
        str(root / "webapp-optmized-astra/deploy/ssh.sh"),
        runtime["database"]["instanceId"],
        "sudo -u postgres psql -XAt -v ON_ERROR_STOP=1 -d isureal -c " + shlex.quote(query),
    ], text=True, timeout=60, env=dict(os.environ,
        AWS_CONFIG_FILE=str(root / "provisioning/aws-sdk.config"),
        AWS_SDK_LOAD_CONFIG="1")))
    for archive in published:
        report = root / "webapp-optmized-astra/results" / ("archive-" + archive["archive"]) / "report.json"
        if report.exists():
            evidence = json.loads(report.read_text())
            archive.update({key: evidence[key] for key in ("bytes", "sha256", "manifest_sha256")})
    experiment["published_image_archives"] = published
except (OSError, subprocess.SubprocessError, ValueError, KeyError) as error:
    # Preserve the measured score even if the separate evidence query fails.
    experiment["archive_evidence_error"] = type(error).__name__
(destination / "experiment.json").write_text(json.dumps(experiment, indent=2) + "\n")
with Path("webapp-optmized-astra/README.md").open("a") as file:
    file.write(f'| {run} | {label} | {"PASS" if result["pass"] else "FAIL"} | {result["score"]:,} |\n')
