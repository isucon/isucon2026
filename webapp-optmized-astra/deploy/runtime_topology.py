"""Metadata replica roles on the existing Team 10 application VMs."""

def asia_synchronous(runtime):
    enabled = runtime.get("asiaReplicaSynchronous", False)
    assert isinstance(enabled, bool)
    assert not enabled or (runtime.get("asiaReplicaEnabled", False)
                           and runtime["replicasEnabled"]
                           and runtime.get("writeFenceEnabled", False)
                           and runtime.get("writeFenceMode") == "native")
    return enabled


def synchronous_standby_names(runtime):
    if asia_synchronous(runtime):
        return "FIRST 3 (astra_europe, astra_america, astra_asia)"
    if runtime["replicasSynchronous"] or (
            runtime.get("writeFenceEnabled", False) and runtime.get("writeFenceMode") == "native"):
        return "FIRST 2 (astra_europe, astra_america)"
    return ""


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
        # An asynchronous Asia standby needs a fresh WAL barrier per read.
        return "127.0.0.1", not asia_synchronous(runtime)
    return "127.0.0.1", runtime.get("readFenceEnabled", False)


def read_scope(runtime, app, *, writer=False):
    scope = runtime.get("asiaReplicaReadScope", "all")
    assert scope in ("all", "feed_search")
    if writer or app["region"] != "asia" or not runtime.get("asiaReplicaReads", False):
        return "all"
    assert runtime.get("asiaReplicaEnabled", False) and runtime["replicaReads"]
    return scope
