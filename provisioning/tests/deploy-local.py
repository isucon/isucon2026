#!/usr/bin/env python3
"""Exercise real Ansible copy/restart conditions using temporary local app targets.

Systemd and readiness are replaced with local command stand-ins. This does not
claim VM/network conformance; it checks the deployment control flow and files.
"""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import tempfile
from threading import Thread

import yaml

ROOT = Path(__file__).resolve().parents[2]


class Frontend(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'<!doctype html><html></html>')

    def log_message(self, *args):
        pass


def rewrite(value, target):
    if isinstance(value, list):
        return [rewrite(v, target) for v in value]
    if isinstance(value, str):
        return value.replace('/opt/isucon', str(target))
    if not isinstance(value, dict):
        return value
    result = {k: rewrite(v, target) for k, v in value.items()}
    if 'ansible.builtin.systemd_service' in result:
        result.pop('ansible.builtin.systemd_service')
        result['ansible.builtin.command'] = {'argv': [
            '/usr/bin/python3', '-c',
            "import sys; open(sys.argv[1], 'a').write(sys.argv[2] + '\\n')",
            str(target / 'restarts'), '{{ item }}',
        ]}
    if result.get('ansible.builtin.command') == '/usr/local/sbin/isureal-node-check':
        result['ansible.builtin.command'] = '/bin/true'
    if result.get('owner') == 'root':
        result.pop('owner')
        result.pop('group')
    return result


def main():
    server = ThreadingHTTPServer(('127.0.0.1', 0), Frontend)
    Thread(target=server.serve_forever, daemon=True).start()
    try:
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            target = work / 'target'
            for path in ('bin', 'share', 'frontend'):
                (target / path).mkdir(parents=True)
            payload = work / 'payload'
            for path in ('bin', 'share', 'frontend/assets'):
                (payload / path).mkdir(parents=True)
            for name in ('api', 'worker', 'image-sanitizer', 'migrate', 'reset', 'seed'):
                (payload / 'bin' / name).write_text('test executable')
            (payload / 'share/openapi.yaml').write_text('test contract')
            (payload / 'frontend/index.html').write_text('<!doctype html>new frontend')
            (payload / 'frontend/assets/new.js').write_text('new asset')
            (target / 'frontend/obsolete.js').write_text('old asset')
            playbook = yaml.safe_load((ROOT / 'provisioning/ansible/playbooks/deploy.yml').read_text())
            (work / 'playbook.yml').write_text(yaml.safe_dump(rewrite(playbook, target)))
            (work / 'inventory.yml').write_text(yaml.safe_dump({'all': {
                'vars': {'ansible_connection': 'local', 'ansible_become': False,
                         'ansible_python_interpreter': '/usr/bin/python3'},
                'children': {'app_servers': {'hosts': {'app1': {}}}},
            }}))
            variables = {
                'deploy_backend': str(payload), 'deploy_frontend': str(payload),
                'deploy_frontend_files': ['index.html', 'assets/new.js'],
                'isucon_topology': {'clientForward': {'hostAddress': '127.0.0.1', 'hostPort': server.server_port},
                                   'portal': {'teamNumber': 1, 'teamHash': 'local'}, 'domain': 'example.test'},
            }

            def run(label, expected_code=0):
                (work / 'vars.json').write_text(json.dumps(variables))
                result = subprocess.run([
                    'ansible-playbook', '-i', str(work / 'inventory.yml'), str(work / 'playbook.yml'),
                    '--tags', 'deploy,restart,verify', '-e', '@' + str(work / 'vars.json'),
                ], env={**os.environ,
                        'ANSIBLE_CALLBACK_PLUGINS': str(ROOT / 'provisioning/ansible/callback_plugins'),
                        'ANSIBLE_CALLBACKS_ENABLED': 'deploy_report',
                        'ISUREAL_DEPLOY_REPORT': str(work / 'report.json')}, capture_output=True, text=True)
                assert result.returncode == expected_code, result.stdout + result.stderr
                report = json.loads((work / 'report.json').read_text())
                print(f'{label}: {report}', flush=True)
                return report

            run('initial update')
            assert (target / 'restarts').read_text().splitlines() == ['isureal-api.service', 'isureal-worker.service']
            assert not (target / 'frontend/obsolete.js').exists()
            assert (target / 'frontend/assets/new.js').read_text() == 'new asset'
            report = run('unchanged update')
            assert report['nodes']['app1']['changed'] == 0
            assert len((target / 'restarts').read_text().splitlines()) == 2
            variables.pop('deploy_frontend')
            (payload / 'bin/api').write_text('updated executable')
            run('backend only')
            assert (target / 'bin/api').read_text() == 'updated executable'
            assert len((target / 'restarts').read_text().splitlines()) == 4
            # Changed files before a copy failure still trigger recovery restarts.
            (payload / 'bin/api').write_text('partially updated executable')
            (payload / 'bin/seed').unlink()
            run('partial backend copy failure', expected_code=2)
            assert len((target / 'restarts').read_text().splitlines()) == 6
            variables.pop('deploy_backend')
            variables['deploy_frontend'] = str(payload)
            (payload / 'frontend/index.html').write_text('<!doctype html>frontend only')
            run('frontend only')
            assert (target / 'frontend/index.html').read_text() == '<!doctype html>frontend only'
            assert len((target / 'restarts').read_text().splitlines()) == 6
            # A failed HTTP check must remain an overall deployment failure.
            variables['isucon_topology']['clientForward']['hostPort'] = 0
            failed_playbook = rewrite(playbook, target)
            for task in failed_playbook[2]['tasks']:
                if 'ansible.builtin.uri' in task:
                    task['retries'] = 0
            (work / 'playbook.yml').write_text(yaml.safe_dump(failed_playbook))
            report = run('HTTP readiness failure', expected_code=2)
            assert report['nodes']['app1']['failures'] == 1
            print('Local deployment control-flow verification: PASS', flush=True)
    finally:
        server.shutdown()
        server.server_close()


if __name__ == '__main__':
    main()
