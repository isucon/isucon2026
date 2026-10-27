"""Persist reproducible evidence for one completed Team 10 benchmark."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys

run, label = sys.argv[1:]
source = Path("var/aws-bench") / run
result = json.loads((source / "result.json").read_text())
meta = json.loads((source / "meta.json").read_text())
assert meta["team"] == 10 and meta["exitCode"] is not None
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
    "team": 10,
    "pass": result["pass"],
    "score": result["score"],
    "note": "The result commit includes source and Pulumi configuration used for this run.",
}
(destination / "experiment.json").write_text(json.dumps(experiment, indent=2) + "\n")
with Path("webapp-optmized-astra/README.md").open("a") as file:
    file.write(f'| {run} | {label} | {"PASS" if result["pass"] else "FAIL"} | {result["score"]:,} |\n')
