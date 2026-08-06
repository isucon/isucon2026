from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import yaml


REPO_ROOT = Path(__file__).resolve().parents[3]
FEATURE_DIR = REPO_ROOT / "specs" / "004-local-vm-provisioning"
PROVISIONING_DIR = REPO_ROOT / "provisioning"


def load_json(path: Path) -> Any:
    return json.loads(path.read_text())


def load_yaml(path: Path) -> Any:
    return yaml.safe_load(path.read_text())
