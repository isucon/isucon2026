"""Metadata replica roles on the existing Team 10 application VMs."""

def metadata_replicas(runtime):
    replicas = [dict(app) for app in runtime["applications"] if "replicaName" in app]
    assert {app["replicaName"] for app in replicas} == {"astra_europe", "astra_america"}
    if runtime.get("asiaReplicaEnabled", False):
        assert runtime["replicasEnabled"]
        asia = dict(next(app for app in runtime["applications"] if app["region"] == "asia"))
        asia["replicaName"] = "astra_asia"
        replicas.append(asia)
    return replicas


def read_settings(runtime, app, *, writer=False):
    if writer or not runtime["replicaReads"]:
        return "", False
    if app["region"] == "asia":
        if not runtime.get("asiaReplicaReads", False):
            return "", False
        assert runtime.get("asiaReplicaEnabled", False)
        # The remote write barrier still waits only for Europe and America.
        # Capture/replay a fresh primary WAL position before every Asia read.
        return "127.0.0.1", True
    return "127.0.0.1", runtime.get("readFenceEnabled", False)
