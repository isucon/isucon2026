"""Migrate one initial-image partition after durable copies match in all regions.

Usage: python3 .../migrate-image-archive.py image_objects_202509
Rollback: append --restore ARCHIVE_UUID_HEX. Published archives must never be
removed manually; image-gc knows which complete files still have references.
"""
from concurrent.futures import ThreadPoolExecutor
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import tarfile
import time
import uuid

root = Path(__file__).resolve().parents[2]
deploy = root / 'webapp-optmized-astra/deploy'
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('partition')
parser.add_argument('--restore')
parser.add_argument('--reclaim')
parser.add_argument('--resume', help='Publish an already built but unpublished archive after verification')
args = parser.parse_args()
assert re.fullmatch(r'image_objects_202[4-7](0[1-9]|1[0-2])', args.partition)
assert sum(bool(mode) for mode in (args.restore, args.reclaim, args.resume)) <= 1
archive = args.restore or args.reclaim or args.resume or uuid.uuid4().hex
assert re.fullmatch('[0-9a-f]{32}', archive)
runtime = json.loads((deploy / 'runtime.json').read_text())
assert runtime['fileImageStore']['enabled'] and runtime['fileImageStore']['archiveReadsEnabled']
lock = open(root / 'var/astra/operation.lock', 'w')
fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
env = dict(os.environ, AWS_CONFIG_FILE=str(root / 'provisioning/aws-sdk.config'), AWS_SDK_LOAD_CONFIG='1', PULUMI_BACKEND_URL='s3://isucon2026-pulumi-state')
os.environ.update(env)
spec = importlib.util.spec_from_file_location('asset_gc', deploy / 'asset-gc.py')
gc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gc)
gc.ensure_idle()
subprocess.run(['python3', str(deploy / 'verify-runtime.py')], cwd=root, env=env, check=True)
apps = runtime['applications']
asia = next(app for app in apps if app['region'] == 'asia')
database = runtime['database']
ssh = str(deploy / 'ssh.sh')
binary = '/home/isucon/webapp-optmized-astra/rust/archive-images'
helper = '/home/isucon/astra-verify-regional-image-restart.py'
report_path = '/var/lib/astra-maint/archives/' + archive + '.json'
output = root / 'var/astra/archive-migrations' / archive
output.mkdir(parents=True, mode=0o700, exist_ok=True)
with tarfile.open(root / runtime['archivePath']) as package:
    expected_binary = hashlib.file_digest(package.extractfile('rust/archive-images'), 'sha256').hexdigest()


def remote(app, command):
    return subprocess.check_output([ssh, app['instanceId'], command], env=env, text=True)


def sql(query):
    return remote(database, 'sudo -u postgres psql -XAt -v ON_ERROR_STOP=1 -d isureal -c ' + shlex.quote(query)).strip()


def parallel(function):
    with ThreadPoolExecutor(max_workers=3) as pool:
        return list(pool.map(function, apps))


def units(app):
    return 'webapp-optmized-astra webapp-optmized-astra-writer' if app['region'] == 'asia' else 'webapp-optmized-astra'


def invoke(app, mode):
    host = database['host'] if app['region'] == 'asia' else '127.0.0.1'
    command = f'set -a; . /home/isucon/env.sh; set +a; export ISUREAL_DB_HOST={host}; {binary} {mode} {args.partition} {archive}'
    for attempt in range(3):
        try:
            result = json.loads(remote(app, 'sudo bash -c ' + shlex.quote(command)))
            break
        except subprocess.CalledProcessError as error:
            # An EICE/SSH connection can expire during a long migration. Only
            # pure verification is retried; publication has an ambiguous
            # outcome on disconnect and must be inspected before resuming.
            if mode not in ('verify', 'verify-source') or error.returncode != 255 or attempt == 2:
                raise
            print(app['region'], mode, 'SSH disconnected; retrying read-only verification', flush=True)
            time.sleep(1)
    print(app['region'], mode, json.dumps(result), flush=True)
    return result


def capture(app):
    assert remote(app, 'sha256sum ' + binary).split()[0] == expected_binary
    subprocess.run([ssh, app['instanceId'], '--copy', str(deploy / 'verify-regional-image-restart.py'), helper], env=env, check=True)
    command = f'set -a; . /home/isucon/env.sh; set +a; python3 {helper} capture {args.partition}'
    print(app['region'], remote(app, 'sudo bash -c ' + shlex.quote(command)).strip(), flush=True)


def start(app):
    remote(app, 'sudo systemctl start ' + units(app))
    program = '''import socket,time
deadline=time.monotonic()+40
while True:
 try:
  with socket.create_connection(('127.0.0.1',8080),timeout=1):break
 except OSError:
  if time.monotonic()>deadline:raise
  time.sleep(.2)
'''
    remote(app, 'python3 -c ' + shlex.quote(program))


def verify_replay():
    # A vacuum rewrite commits outside the application transaction. Wait for
    # its WAL on both standbys before starting any local image lookups.
    target = sql('SELECT pg_current_wal_lsn()')
    program = 'target=' + repr(target) + '\n' + '''import subprocess,time
deadline=time.monotonic()+180
while True:
 query="SELECT count(*) FROM pg_stat_replication WHERE state='streaming' AND replay_lsn>='"+target+"'::pg_lsn"
 count=subprocess.check_output(['sudo','-u','postgres','psql','-XAt','-d','isureal','-c',query],text=True).strip()
 if count=='2':break
 if time.monotonic()>deadline:raise RuntimeError('metadata replicas did not catch up')
 time.sleep(.5)
'''
    remote(database, 'python3 -c ' + shlex.quote(program))


def await_cleanup_horizon():
    # Replay acknowledgement alone does not refresh hot-standby feedback. A
    # stale slot xmin would make VACUUM FULL retain the old image row versions.
    barrier=sql('SELECT pg_current_xact_id()::text::xid')
    assert barrier.isdigit()
    program='barrier='+repr(barrier)+'\n'+'''import subprocess,time
query="SELECT count(*)=2 AND bool_and(xmin IS NULL OR age(xmin)<=age('"+barrier+"'::xid)) FROM pg_replication_slots WHERE slot_name IN ('astra_europe','astra_america')"
deadline=time.monotonic()+60
while True:
 ready=subprocess.check_output(['sudo','-u','postgres','psql','-XAt','-d','isureal','-c',query],text=True).strip()
 if ready=='t':break
 if time.monotonic()>deadline:raise RuntimeError('standby cleanup horizon has not advanced')
 time.sleep(.5)
'''
    remote(database,'python3 -c '+shlex.quote(program))


parallel(capture)
if args.restore or args.reclaim or args.resume:
    previous = json.loads(remote(asia, 'sudo cat ' + report_path))
    bytes_required = previous['bytes']
else:
    bytes_required = int(sql(f"SELECT coalesce(sum(octet_length(data)),0) FROM {args.partition} WHERE asset_key IS NULL AND created_at<=(SELECT created_at FROM initial_data_boundary)"))
assert 0 < bytes_required <= 8 * 1024**3
for app in apps + [database]:
    available = int(remote(app, 'df -B1 --output=avail /var/lib/postgresql | tail -n1').strip())
    assert available > bytes_required * 2 + 2 * 1024**3, 'Insufficient migration and WAL headroom'
    print(app.get('region', 'database'), 'available_bytes', available, flush=True)

stopped = []
try:
    for app in apps:
        stopped.append(app)
        remote(app, 'sudo systemctl stop ' + units(app))
    sql(f'BEGIN; LOCK TABLE {args.partition} IN ACCESS EXCLUSIVE MODE; COMMIT;')
    verify_replay()
    if not (args.restore or args.reclaim or args.resume):
        reports = parallel(lambda app: invoke(app, 'build'))
        assert reports[0] == reports[1] == reports[2], 'Regional archives differ; metadata remains unchanged'
    reports = parallel(lambda app: invoke(app, 'verify'))
    assert reports[0] == reports[1] == reports[2], 'Regional archive verification differs'
    report = reports[0]
    (output / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
    # Asia can publish/restore over the normal connection to the primary;
    # the primary does not need a fourth copy of the immutable file.
    if args.resume:
        assert int(sql(f"SELECT count(*) FROM {args.partition} WHERE asset_key LIKE 'pack:{archive}:%'")) == 0, 'Archive already published; use --reclaim after inspecting the previous operation'
    if not args.reclaim:
        invoke(asia, 'restore' if args.restore else 'apply')
    expected = 0 if args.restore else report['rows']
    assert int(sql(f"SELECT count(*) FROM {args.partition} WHERE asset_key LIKE 'pack:{archive}:%'")) == expected
    print('Reclaiming the old partition storage after publication', flush=True)
    await_cleanup_horizon()
    before=int(sql("SELECT pg_total_relation_size('"+args.partition+"')"))
    sql(f'VACUUM (FULL, ANALYZE) {args.partition}')
    after=int(sql("SELECT pg_total_relation_size('"+args.partition+"')"))
    print('partition_bytes',before,'->',after,flush=True)
    sql('CHECKPOINT')
    verify_replay()
    for app in apps:
        if app['region']!='asia':remote(app,'sudo -u postgres psql -X -d isureal -c CHECKPOINT')
    if args.restore:
        reports = parallel(lambda app: invoke(app, 'verify-source'))
        assert reports[0] == reports[1] == reports[2]
finally:
    with ThreadPoolExecutor(max_workers=3) as pool:
        list(pool.map(start, stopped))

for app in apps:
    print(app['region'], remote(app, 'sudo python3 ' + helper + ' verify').strip(), flush=True)
subprocess.run(['python3', str(deploy / 'verify-runtime.py')], cwd=root, env=env, check=True)
evidence = root / 'webapp-optmized-astra/results' / ('archive-' + archive + ('-restore' if args.restore else '-reclaim' if args.reclaim else ''))
evidence.mkdir()
(evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
(evidence / 'validation.txt').write_text('All three archive and manifest hashes matched before SQL publication. Available public images (up to five per region) matched before/after migration and application restart. Runtime and both metadata replicas verified.\n')
print('Completed', 'restore' if args.restore else 'reclaim' if args.reclaim else 'migration', args.partition, archive, flush=True)
