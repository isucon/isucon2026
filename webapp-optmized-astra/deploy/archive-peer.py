"""Copy or verify immutable archives on existing db1. Run from repository root."""
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys


def main():
    assert sys.argv[1:] in (['copy'], ['verify'])
    mode = sys.argv[1]
    root = Path(__file__).resolve().parents[2]
    deploy = root / 'webapp-optmized-astra/deploy'
    runtime = json.loads((deploy / 'runtime.json').read_text())
    peer = runtime['archivePeer']
    assert peer['enabled'] and peer['instanceId'] == runtime['database']['instanceId']
    assert peer['host'] == runtime['database']['host'] == '192.168.180.10'
    assert peer['port'] == 8082 and peer['root'] == '/var/lib/astra-images/packs'
    env = dict(os.environ, AWS_CONFIG_FILE=str(root / 'provisioning/aws-sdk.config'), AWS_SDK_LOAD_CONFIG='1')
    ssh = str(deploy / 'ssh.sh')
    if mode == 'copy':
        assert not peer['readsEnabled'], 'Prepare copies before enabling peer reads'
        lock = open(root / 'var/astra/operation.lock', 'w')
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        os.environ.update(env)
        spec = importlib.util.spec_from_file_location('asset_gc', deploy / 'asset-gc.py')
        gc = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(gc)
        gc.ensure_idle()
    manifest = json.loads((deploy / 'archive-peer-manifest.json').read_text())
    query = "SELECT DISTINCT split_part(asset_key,':',2) FROM image_objects WHERE asset_key LIKE 'pack:%' ORDER BY 1"
    current = subprocess.check_output([ssh, peer['instanceId'], 'sudo -u postgres psql -XAt -v ON_ERROR_STOP=1 -d isureal -c ' + shlex.quote(query)], env=env, text=True).splitlines()
    assert current == sorted(item['archive'] for item in manifest['archives']), 'Published archive set differs from the managed manifest'
    if mode == 'copy':
        for name in ['archive-peer-files.py', 'archive-peer-manifest.json']:
            subprocess.run([ssh, peer['instanceId'], '--copy', str(deploy / name), '/home/isucon/astra-deploy/' + name], env=env, check=True)
    subprocess.run([ssh, peer['instanceId'], 'python3 /home/isucon/astra-deploy/archive-peer-files.py ' + mode], env=env, check=True)


if __name__ == '__main__':
    main()
