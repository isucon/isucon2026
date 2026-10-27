#!/usr/bin/env python3
"""ベンチ 1 回分の統計（ベンチのタスクとチームのサーバー）を、負荷走行の区間でまとめる。

使い方: report.py RUN_DIR

RUN_DIR の stats.tsv（ベンチのタスク）と hosts/<役割>/stats.tsv（sampler.sh の出力）を読み、
bench.log の "phase: load" から "phase: final check" までを負荷走行の区間とする。
hosts/<役割>/ebs.json があれば、ディスクの実測値を EBS の上限と比べる。
要約を標準出力へ、1 秒ごとの値を RUN_DIR/timeline.csv へ書く。
"""

import csv
import json
import re
import sys
from datetime import datetime
from pathlib import Path

PHASE = re.compile(r'^time=(\S+) .*msg="phase: ([^"]+)"')
# 集計しないネットワークインターフェース（ループバックとコンテナ用の仮想インターフェース）
IGNORED_NET = re.compile(r"^(lo|docker|veth|br-|virbr|tailscale)")
HOST_ORDER = ["bench", "router", "app1", "app2", "app3", "db1"]
# この割合を超えたら飽和とみなす
SATURATED = 0.85


def to_ms(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp() * 1000


def phases(path):
    found = {}
    if path.exists():
        for line in path.read_text(errors="replace").splitlines():
            m = PHASE.match(line)
            if m and m.group(2) not in found:
                found[m.group(2)] = to_ms(m.group(1))
    return found


def load(path):
    header, samples, procs = {}, [], {}
    for line in path.read_text(errors="replace").splitlines():
        kind, _, rest = line.partition(" ")
        kv = dict(f.split("=", 1) for f in rest.split() if "=" in f)
        if kind == "H":
            header.update(kv)
        elif kind == "D":
            header.setdefault("disks", {})[kv["name"]] = kv.get("serial", "")
        elif kind == "S" and kv.get("cpu"):
            samples.append(kv)
        elif kind == "P":
            procs.setdefault(int(kv["ts"]), []).append(kv)
    return header, samples, procs


def ints(value):
    return [int(x) for x in value.split(",")]


def disk_rates(a, b, dt):
    """/proc/diskstats の累積値の差から、デバイスごとの IOPS、MB/s、util、待ち時間を出す。"""
    disks = {}
    for key, value in b.items():
        if not key.startswith("disk.") or key not in a:
            continue
        r0, rs0, rms0, w0, ws0, wms0, _, io0, wt0 = ints(a[key])
        r1, rs1, rms1, w1, ws1, wms1, _, io1, wt1 = ints(value)
        ops = (r1 - r0) + (w1 - w0)
        disks[key.removeprefix("disk.")] = {
            "r_iops": (r1 - r0) / dt,
            "w_iops": (w1 - w0) / dt,
            "r_mbs": (rs1 - rs0) * 512 / dt / 1e6,
            "w_mbs": (ws1 - ws0) * 512 / dt / 1e6,
            # NVMe は並列に処理するので、100% でも飽和しているとは限らない
            "util": min(100.0, (io1 - io0) / (dt * 1000) * 100),
            "queue": (wt1 - wt0) / (dt * 1000),
            # 区間をまとめた待ち時間は、回数で重み付けして出す
            "ops": ops,
            "wait_ms": (rms1 - rms0) + (wms1 - wms0),
        }
    return disks


def intervals(header, samples):
    """隣り合うサンプルの差分から、区間ごとの率を出す。"""
    ncpu = int(header.get("ncpu", 1))
    rows = []
    for a, b in zip(samples, samples[1:]):
        dt = (int(b["ts"]) - int(a["ts"])) / 1000
        if dt <= 0:
            continue
        d = [y - x for x, y in zip(ints(a["cpu"]), ints(b["cpu"]))]
        user, nice, system, idle, iowait, irq, softirq, steal = d
        total = sum(d) or 1
        mem_total, mem_avail, *dirty = ints(b["mem"])
        rx = tx = 0
        nets = {}
        for key, value in b.items():
            name = key.removeprefix("net.")
            if key.startswith("net.") and not IGNORED_NET.match(name) and key in a:
                (rx0, tx0), (rx1, tx1) = ints(a[key]), ints(value)
                nets[name] = ((rx1 - rx0) * 8 / dt / 1e6, (tx1 - tx0) * 8 / dt / 1e6)
                rx += nets[name][0]
                tx += nets[name][1]
        row = {
            "ts": int(b["ts"]),
            "cpu": (total - idle - iowait) / total * 100,
            "cores": (total - idle - iowait) / total * ncpu,
            "user": (user + nice) / total * 100,
            "sys": system / total * 100,
            "softirq": (irq + softirq) / total * 100,
            "steal": steal / total * 100,
            "iowait": iowait / total * 100,
            "mem": (mem_total - mem_avail) / 1024,
            "mem_total": mem_total / 1024,
            # 書き戻し待ち（Dirty + Writeback）
            "dirty": sum(dirty) / 1024,
            "rx": rx,
            "tx": tx,
            "nets": nets,
            "tcp": int(b.get("tcp", "0,0").split(",")[0]),
            "disks": disk_rates(a, b, dt),
            # PSI: 少なくとも 1 つ（some）またはすべて（full）のタスクが待たされた時間の割合
            "psi": {
                # 時刻の取り方のずれで 100% をわずかに超えることがある
                key.removeprefix("psi."): min(100.0, (int(b[key]) - int(a[key])) / (dt * 1e6) * 100)
                for key in b
                if key.startswith("psi.") and key in a
            },
        }
        # cgroup v2 は usage_usec、v1 は usage_ns
        for key, scale in (("cg.usage_usec", 1e6), ("cg.usage_ns", 1e9)):
            if key in a and key in b:
                row["cg_cores"] = (int(b[key]) - int(a[key])) / scale / dt
        if "cg.nr_periods" in a and "cg.nr_periods" in b:
            periods = int(b["cg.nr_periods"]) - int(a["cg.nr_periods"])
            if periods > 0:
                row["throttled"] = (int(b["cg.nr_throttled"]) - int(a["cg.nr_throttled"])) / periods * 100
        rows.append(row)
    return rows


def process_cores(header, procs, start, end):
    """区間の前後のスナップショットの差から、コマンド名ごとの使用コア数を出す。

    ticks は回収済みの子の時間を含むので、コマンド名ごとの合計の差を取れば、区間の途中で
    終了したプロセス（PostgreSQL のバックエンドなど）の時間も親のコマンド名に入る。
    """
    times = sorted(procs)
    if not times:
        return []
    t0 = max((t for t in times if t <= start), default=times[0])
    t1 = min((t for t in times if t >= end), default=times[-1])
    if t1 <= t0:
        return []
    hz = int(header.get("hz", 100))
    usage = {}
    for sign, snapshot in ((1, procs[t1]), (-1, procs[t0])):
        for p in snapshot:
            # kworker/u8:3-events のように、カーネルスレッドの名前は処理中の仕事で変わる
            comm = p["comm"].split("/")[0] if p["comm"].startswith("kworker/") else p["comm"]
            usage[comm] = usage.get(comm, 0) + sign * int(p["ticks"])
    seconds = (t1 - t0) / 1000
    return sorted(((comm, ticks / hz / seconds) for comm, ticks in usage.items() if ticks > 0), key=lambda x: -x[1])


def avg(values):
    return sum(values) / len(values) if values else 0.0


def p95(values):
    if not values:
        return 0.0
    ordered = sorted(values)
    return ordered[min(len(ordered) - 1, int(len(ordered) * 0.95))]


def counters_delta(start_path, end_path, pattern):
    def read(path):
        values = {}
        for line in path.read_text(errors="replace").splitlines():
            name, _, value = line.rpartition(": ")
            if value.isdigit():
                values[name] = int(value)
        return values

    if not start_path.exists() or not end_path.exists():
        return []
    a, b = read(start_path), read(end_path)
    return sorted(
        ((name, value - a.get(name, 0)) for name, value in b.items() if pattern.search(name) and value - a.get(name, 0) > 0),
        key=lambda x: -x[1],
    )


def tc_delta(start_path, end_path):
    """qdisc ごとの送信バイト、破棄、overlimits の差分。"""
    head = re.compile(r"^qdisc (\S+) (\S+) dev (\S+) (parent \S+|root)")
    sent = re.compile(r"Sent (\d+) bytes (\d+) pkt \(dropped (\d+), overlimits (\d+)")

    def read(path):
        values, key = {}, None
        for line in path.read_text(errors="replace").splitlines():
            if m := head.match(line):
                key = (m.group(3), m.group(1), m.group(2), m.group(4))
            elif key and (m := sent.search(line)):
                values[key] = tuple(int(x) for x in m.groups())
        return values

    if not start_path.exists() or not end_path.exists():
        return []
    a, b = read(start_path), read(end_path)
    rows = []
    for key, value in b.items():
        base = a.get(key, (0, 0, 0, 0))
        rows.append((key, *(y - x for x, y in zip(base, value))))
    return rows


def number(value):
    return "-" if value is None else f"{value:.0f}"


def ebs_limits(run, name, header):
    """デバイス名ごとの EBS の上限（ボリュームと、インスタンスタイプの EBS の帯域）。"""
    path = run / "hosts" / name / "ebs.json"
    if not path.exists():
        return {}
    ebs = json.loads(path.read_text())
    volumes = {v["id"].replace("-", ""): v for v in ebs.get("volumes") or []}
    disks = header.get("disks", {})
    limits = {}
    for device, serial in disks.items():
        volume = volumes.get(serial)
        if volume is None and len(volumes) == 1 and len(disks) == 1:
            volume = next(iter(volumes.values()))
        if volume:
            limits[device] = (volume, ebs.get("instanceType"), ebs.get("instanceLimits") or {})
    return limits


def disk_section(run, names, hosts, series, start, end):
    flags, lines, busy = [], [], []
    for name in names:
        rows = [r for r in series[name] if start < r["ts"] <= end]
        limits = ebs_limits(run, name, hosts[name][0])
        for device in sorted({d for r in rows for d in r["disks"]}):
            ds = [r["disks"][device] for r in rows if device in r["disks"]]
            ops = sum(d["ops"] for d in ds)
            if ops == 0:
                continue
            iops = [d["r_iops"] + d["w_iops"] for d in ds]
            mbs = [d["r_mbs"] + d["w_mbs"] for d in ds]
            util = [d["util"] for d in ds]
            # ログの書き出し程度のデバイスは 5 秒ごとの表に出さない
            if max(util) >= 5 or avg(iops) >= 50:
                busy.append((name, device))
            wait = sum(d["wait_ms"] for d in ds) / ops

            def cell(key, width=6):
                values = [d[key] for d in ds]
                return f"{avg(values):{width}.1f}/{max(values):<{width}.1f}"

            lines.append(f"  {name:<7} {device:<9} {cell('r_iops')} {cell('w_iops')} {cell('r_mbs', 5)} "
                         f"{cell('w_mbs', 5)} {avg(util):5.0f}/{max(util):<4.0f} {wait:7.2f} {cell('queue', 4)} "
                         f"{max(r['dirty'] for r in rows):7.0f}")
            if avg(util) >= SATURATED * 100:
                flags.append(f"{name} {device}: disk busy (util avg {avg(util):.0f}%, await {wait:.1f} ms)")
            if device not in limits:
                continue
            volume, instance_type, instance = limits[device]
            vol_iops, vol_mib = volume.get("iops"), volume.get("throughput")
            lines.append(f"  {'':<7} {'':<9} limit: {volume.get('type')} {number(vol_iops)} IOPS {number(vol_mib)} MiB/s; "
                         f"{instance_type} baseline {number(instance.get('BaselineIops'))} IOPS "
                         f"{number(instance.get('BaselineThroughputInMBps'))} MB/s, "
                         f"burst {number(instance.get('MaximumIops'))} IOPS "
                         f"{number(instance.get('MaximumThroughputInMBps'))} MB/s")
            if vol_iops and max(iops) >= 0.9 * vol_iops:
                flags.append(f"{name} {device}: near the volume IOPS limit (max {max(iops):.0f} of {vol_iops})")
            if vol_mib and max(mbs) >= 0.9 * vol_mib * 1.048576:
                flags.append(f"{name} {device}: near the volume throughput limit (max {max(mbs):.0f} MB/s of {vol_mib} MiB/s)")
            # インスタンスの EBS の帯域は、基準を超えると 1 日 30 分までのバーストを使う
            if avg(iops) >= instance.get("BaselineIops", float("inf")) or \
                    avg(mbs) >= instance.get("BaselineThroughputInMBps", float("inf")):
                flags.append(f"{name} {device}: above the {instance_type} EBS baseline (uses burst credits)")
    if not lines:
        return flags
    print()
    print("disk in the load window (avg/max):")
    print(f"  {'host':<7} {'device':<9} {'r IOPS':^13} {'w IOPS':^13} {'r MB/s':^11} {'w MB/s':^11} "
          f"{'util %':^10} {'await ms':>8} {'queue':^9} {'dirty MiB':>9}")
    for line in lines:
        print(line)
    if not busy:
        return flags
    print()
    print("disk util % / await ms every 5s from the load start:")
    print("  " + f"{'t':>4} " + " ".join(f"{name + ':' + device:>17}" for name, device in busy))
    for t in range(0, int((end - start) / 1000), 5):
        lo, hi = start + t * 1000, start + (t + 5) * 1000
        cells = []
        for name, device in busy:
            ds = [r["disks"][device] for r in series[name] if lo < r["ts"] <= hi and device in r["disks"]]
            ops = sum(d["ops"] for d in ds)
            wait = sum(d["wait_ms"] for d in ds) / ops if ops else 0
            cells.append(f"{avg([d['util'] for d in ds]):9.0f}%/{wait:5.1f}ms" if ds else f"{'-':>17}")
        print("  " + f"{t:>3}s " + " ".join(cells))
    return flags


def pressure_section(names, series, start, end):
    """PSI。CPU、I/O、メモリで待たされた時間の割合。"""
    keys = ["cpu.some", "io.some", "io.full", "memory.some", "memory.full"]
    flags, lines = [], []
    for name in names:
        rows = [r for r in series[name] if start < r["ts"] <= end and r["psi"]]
        if not rows:
            continue
        cells = []
        for key in keys:
            values = [r["psi"].get(key, 0) for r in rows]
            cells.append(f"{avg(values):6.1f}/{max(values):<5.0f}")
        lines.append(f"  {name:<7} " + " ".join(cells))
        io_full = avg([r["psi"].get("io.full", 0) for r in rows])
        if io_full >= 10:
            flags.append(f"{name}: all tasks stalled on I/O {io_full:.0f}% of the time (PSI io full)")
        memory = avg([r["psi"].get("memory.some", 0) for r in rows])
        if memory >= 10:
            flags.append(f"{name}: tasks stalled on memory {memory:.0f}% of the time (PSI memory some)")
    if lines:
        print()
        print("pressure stall in the load window (% of time, avg/max):")
        print(f"  {'host':<7} " + " ".join(f"{key:^12}" for key in keys))
        for line in lines:
            print(line)
    return flags


def main():
    run = Path(sys.argv[1])
    found = phases(run / "bench.log")
    start = found.get("load")
    end = found.get("final check")
    hosts = {}
    if (run / "stats.tsv").exists():
        hosts["bench"] = load(run / "stats.tsv")
    for path in sorted((run / "hosts").glob("*/stats.tsv")):
        hosts[path.parent.name] = load(path)
    if not hosts:
        print("no stats in", run)
        return
    names = sorted(hosts, key=lambda n: (HOST_ORDER.index(n) if n in HOST_ORDER else len(HOST_ORDER), n))
    series = {name: intervals(hosts[name][0], hosts[name][1]) for name in names}
    if start is None or end is None:
        all_ts = [r["ts"] for rows in series.values() for r in rows]
        start, end = min(all_ts), max(all_ts)
        print("load window: not found in bench.log; using the whole run")
    else:
        print(f"load window: {datetime.fromtimestamp(start / 1000).astimezone().strftime('%H:%M:%S')} +{(end - start) / 1000:.0f}s")

    with open(run / "timeline.csv", "w", newline="") as f:
        out = csv.writer(f)
        out.writerow(["host", "ts_ms", "t_from_load_s", "cpu_pct", "user_pct", "sys_pct", "softirq_pct", "steal_pct",
                      "iowait_pct", "mem_used_mib", "dirty_mib", "rx_mbps", "tx_mbps", "tcp_inuse",
                      "disk_r_iops", "disk_w_iops", "disk_r_mbs", "disk_w_mbs", "disk_util_pct", "disk_await_ms",
                      "psi_cpu_some_pct", "psi_io_some_pct", "psi_io_full_pct", "psi_memory_some_pct"])
        for name in names:
            for r in series[name]:
                disks = r["disks"].values()
                ops = sum(d["ops"] for d in disks)
                out.writerow([
                    name, r["ts"], round((r["ts"] - start) / 1000, 1),
                    *(round(r[k], 2) for k in ("cpu", "user", "sys", "softirq", "steal", "iowait", "mem", "dirty",
                                               "rx", "tx")),
                    r["tcp"],
                    *(round(sum(d[k] for d in disks), 2) for k in ("r_iops", "w_iops", "r_mbs", "w_mbs")),
                    round(max((d["util"] for d in disks), default=0), 1),
                    round(sum(d["wait_ms"] for d in disks) / ops, 2) if ops else 0,
                    *(round(r["psi"].get(k, 0), 2) for k in ("cpu.some", "io.some", "io.full", "memory.some")),
                ])

    print()
    print(f"{'host':<7} {'cpus':>4} {'cpu avg':>8} {'p95':>6} {'max':>6}  {'usr/sys/sirq/steal/iow %':<25} "
          f"{'mem MiB':>13} {'rx Mbps avg/max':>16} {'tx Mbps avg/max':>16} {'tcp':>6}")
    flags = []
    for name in names:
        header = hosts[name][0]
        rows = [r for r in series[name] if start < r["ts"] <= end]
        if not rows:
            print(f"{name:<7} no samples in the load window")
            continue
        cpu = [r["cpu"] for r in rows]
        print(f"{name:<7} {header.get('ncpu', '?'):>4} {avg(cpu):7.1f}% {p95(cpu):5.0f}% {max(cpu):5.0f}%  "
              f"{avg([r['user'] for r in rows]):4.0f}/{avg([r['sys'] for r in rows]):3.0f}/"
              f"{avg([r['softirq'] for r in rows]):4.0f}/{avg([r['steal'] for r in rows]):4.1f}/"
              f"{avg([r['iowait'] for r in rows]):3.0f}    "
              f"{max(r['mem'] for r in rows):6.0f}/{rows[-1]['mem_total']:<6.0f} "
              f"{avg([r['rx'] for r in rows]):7.1f}/{max(r['rx'] for r in rows):<8.1f} "
              f"{avg([r['tx'] for r in rows]):7.1f}/{max(r['tx'] for r in rows):<8.1f} {max(r['tcp'] for r in rows):6d}")
        if avg(cpu) >= SATURATED * 100 or p95(cpu) >= 95:
            flags.append(f"{name}: CPU is saturated (avg {avg(cpu):.0f}%, p95 {p95(cpu):.0f}%)")
        if name == "bench" and any("throttled" in r for r in rows):
            throttled = avg([r.get("throttled", 0) for r in rows])
            if throttled > 0:
                flags.append(f"bench: CPU throttled in {throttled:.0f}% of cgroup periods")

    print()
    print("processes in the load window (cores):")
    for name in names:
        top = process_cores(hosts[name][0], hosts[name][2], start, end)[:5]
        if top:
            print(f"  {name:<7} " + ", ".join(f"{comm} {cores:.2f}" for comm, cores in top))
            if name == "bench":
                ncpu = int(hosts[name][0].get("ncpu", 1))
                bench = sum(c for comm, c in top if comm == "isureal-bench")
                if bench >= SATURATED * ncpu:
                    flags.append(f"bench: isureal-bench uses {bench:.2f} of {ncpu} cores")

    # 平均では見えない、投稿期間が開いた直後などの詰まりを 5 秒ごとに見る
    print()
    print("cpu % every 5s from the load start:")
    print("  " + f"{'t':>4} " + " ".join(f"{name:>7}" for name in names))
    for t in range(0, int((end - start) / 1000), 5):
        lo, hi = start + t * 1000, start + (t + 5) * 1000
        cells = []
        for name in names:
            cpu = [r["cpu"] for r in series[name] if lo < r["ts"] <= hi]
            cells.append(f"{avg(cpu):7.0f}" if cpu else f"{'-':>7}")
        print("  " + f"{t:>3}s " + " ".join(cells))

    flags += disk_section(run, names, hosts, series, start, end)
    flags += pressure_section(names, series, start, end)

    nets = {}
    for name in names:
        rows = [r for r in series[name] if start < r["ts"] <= end]
        for r in rows:
            for iface, (rx, tx) in r["nets"].items():
                nets.setdefault((name, iface), []).append((rx, tx))
    if nets:
        print()
        print("network by interface in the load window (Mbps, avg/max):")
        for (name, iface), values in nets.items():
            rx, tx = [v[0] for v in values], [v[1] for v in values]
            print(f"  {name:<7} {iface:<10} rx {avg(rx):7.1f}/{max(rx):<8.1f} tx {avg(tx):7.1f}/{max(tx):.1f}")

    router = run / "hosts" / "router"
    qdiscs = tc_delta(router / "tc-start.txt", router / "tc-end.txt")
    if qdiscs:
        print()
        print("router qdisc (whole run):")
        for dev in sorted({k[0] for k, *_ in qdiscs}):
            rows = [q for q in qdiscs if q[0][0] == dev]
            roots = [q for q in rows if q[0][3] == "root"]
            sent = sum(q[1] for q in roots) if roots else 0
            dropped = sum(q[3] for q in rows)
            over = sum(q[4] for q in rows)
            print(f"  {dev:<8} sent {sent / 1e6:9.1f} MB  dropped {dropped:8d}  overlimits {over:8d}")
            if dropped:
                flags.append(f"router: {dropped} packets dropped by qdisc on {dev}")
    # HAProxy の要求・接続・応答のエラー、中断、5xx
    haproxy = counters_delta(router / "haproxy-start.txt", router / "haproxy-end.txt",
                             re.compile(r"\.(ereq|econ|eresp|eint|wretr|wredis|dreq|dresp|cli_abrt|srv_abrt|hrsp_5xx)$"))
    if haproxy:
        print()
        print("router haproxy counters (whole run, non-zero):")
        for counter, delta in haproxy[:15]:
            print(f"  {counter}: +{delta}")

    print()
    if flags:
        for flag in flags:
            print("! " + flag)
    else:
        print("no saturated host found")
    print(f"timeline: {run / 'timeline.csv'}")
    score_section(run)


def score_section(run):
    """result.json の scores（リージョン・日・要素ごとの得点）を、日×地域と要素×地域の表にする。"""
    path = run / "result.json"
    if not path.exists():
        return
    cells = json.loads(path.read_text()).get("scores") or []
    if not cells:
        return
    regions = ["asia", "europe", "america"]

    def points(cond):
        return [sum(c["points"] for c in cells if c["region"] == r and cond(c)) for r in regions]

    def row(label, values):
        print(f"  {label:<16}" + "".join(f"{v:>9}" for v in values) + f"{sum(values):>9}")

    print()
    print("score by day and region (points; day 0 = before the region's first day)")
    print("  " + "day".ljust(16) + "".join(f"{r:>9}" for r in regions) + f"{'total':>9}")
    for day in sorted({c["day"] for c in cells}):
        row(str(day), points(lambda c, day=day: c["day"] == day))
    row("all", points(lambda c: True))
    print("score by element and region (points)")
    tags = sorted({c["tag"] for c in cells}, key=lambda t: -sum(c["points"] for c in cells if c["tag"] == t))
    for tag in tags:
        row(tag, points(lambda c, tag=tag: c["tag"] == tag))


if __name__ == "__main__":
    main()
