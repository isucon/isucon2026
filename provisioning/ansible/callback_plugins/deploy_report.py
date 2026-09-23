"""Record timings and per-node results without storing task output or secrets."""
import json
import os
from pathlib import Path
import time

from ansible.plugins.callback import CallbackBase


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = "aggregate"
    CALLBACK_NAME = "deploy_report"
    CALLBACK_NEEDS_ENABLED = True

    def __init__(self):
        super().__init__()
        self.started = None
        self.phase = None
        self.seconds = {}

    def finish_task(self):
        if self.started is not None:
            self.seconds[self.phase] = self.seconds.get(self.phase, 0) + time.monotonic() - self.started
            self.started = None

    def v2_playbook_on_task_start(self, task, is_conditional):
        self.finish_task()
        self.phase = next((p for p in ("precheck", "restart", "verify", "deploy") if p in task.tags), "other")
        self.started = time.monotonic()

    def v2_playbook_on_stats(self, stats):
        self.finish_task()
        target = os.environ.get("ISUREAL_DEPLOY_REPORT")
        if target:
            Path(target).write_text(json.dumps({
                "seconds": {k: round(v, 3) for k, v in self.seconds.items()},
                "nodes": {host: stats.summarize(host) for host in sorted(stats.processed)},
            }, indent=2))
