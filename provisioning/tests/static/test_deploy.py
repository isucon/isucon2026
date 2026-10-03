from __future__ import annotations

import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import yaml

from provisioning.scripts import deploy
from support import PROVISIONING_DIR


class DeployTest(unittest.TestCase):
    def test_cache_reuses_verified_output_and_rebuilds_corruption(self):
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory)

            def compile_artifact(*args, **kwargs):
                payload = Path(kwargs['env']['ISUREAL_BUILD_ROOT'])
                payload.mkdir(parents=True, exist_ok=True)
                (payload / 'api').write_bytes(b'compiled')

            with patch.object(deploy, 'fingerprint', return_value='source-hash'), patch.object(
                deploy.subprocess, 'run', side_effect=compile_artifact
            ) as compiler:
                payload, cached = deploy.build('backend', cache, {})
                self.assertFalse(cached)
                self.assertTrue(deploy.build('backend', cache, {})[1])
                self.assertEqual(compiler.call_count, 1)
                (payload / 'api').write_bytes(b'corrupt')
                self.assertFalse(deploy.build('backend', cache, {})[1])
                self.assertEqual(compiler.call_count, 2)

    def test_failed_build_is_not_cached(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(
            deploy, 'fingerprint', return_value='source-hash'
        ), patch.object(deploy.subprocess, 'run', side_effect=subprocess.CalledProcessError(1, 'build')):
            with self.assertRaises(subprocess.CalledProcessError):
                deploy.build('frontend', Path(directory), {})
            self.assertFalse((Path(directory) / 'frontend/source-hash/complete.json').exists())

    def simulate(self, component='app', fail_phase=None):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        state = Path(temporary.name)
        calls = []
        payload = state / 'payload'
        (payload / 'frontend').mkdir(parents=True)
        (payload / 'frontend/index.html').write_text('<!doctype html>')

        def command(args, **kwargs):
            if '--tags' in args:
                phase = args[args.index('--tags') + 1]
                calls.append(phase)
                if phase == fail_phase:
                    raise subprocess.CalledProcessError(2, args)
                Path(kwargs['env']['ISUREAL_DEPLOY_REPORT']).write_text('{}')

        def build(part, *args):
            calls.append('build-' + part)
            if fail_phase == 'build':
                raise subprocess.CalledProcessError(1, 'build')
            return payload, False

        with patch.object(deploy.subprocess, 'run', side_effect=command), patch.object(deploy, 'build', side_effect=build):
            if fail_phase:
                with self.assertRaises(subprocess.CalledProcessError):
                    deploy.deploy_locked(component, state, {})
            else:
                self.assertEqual(0, deploy.deploy_locked(component, state, {}))
        report = json.loads(next((state / 'deploy').glob('*/report.json')).read_text())
        return calls, report

    def test_migration_or_unreachable_failure_prevents_build_and_deploy(self):
        calls, report = self.simulate(fail_phase='precheck')
        self.assertEqual(['precheck'], calls)
        self.assertEqual('failed', report['outcome'])

    def test_build_failure_prevents_deploy(self):
        calls, report = self.simulate(fail_phase='build')
        self.assertEqual(['precheck', 'build-backend'], calls)
        self.assertEqual('failed', report['outcome'])

    def test_frontend_only_and_failed_readiness(self):
        calls, report = self.simulate(component='frontend', fail_phase='deploy,restart,verify')
        self.assertEqual(['precheck', 'build-frontend', 'deploy,restart,verify'], calls)
        self.assertEqual('failed', report['outcome'])

    def test_success_records_artifacts(self):
        calls, report = self.simulate()
        self.assertEqual(['precheck', 'build-backend', 'build-frontend', 'deploy,restart,verify'], calls)
        self.assertEqual('passed', report['outcome'])
        self.assertEqual({'backend', 'frontend'}, set(report['artifacts']))

    def test_prechecks_cannot_mutate_database_and_deploy_targets_only_apps(self):
        plays = yaml.safe_load((PROVISIONING_DIR / 'ansible/playbooks/deploy.yml').read_text())
        self.assertEqual(['problem_servers', 'routers', 'app_servers'], [p['hosts'] for p in plays])
        for play in plays[:2]:
            self.assertTrue(play['any_errors_fatal'])
            for task in play['tasks']:
                self.assertTrue('ansible.builtin.command' in task or 'ansible.builtin.assert' in task)
                if 'ansible.builtin.command' in task:
                    self.assertFalse(task['changed_when'])
        backend = plays[2]['tasks'][0]
        self.assertEqual('deploy_backend is defined', backend['when'])
        restart = backend['always'][0]
        self.assertEqual(['isureal-api.service', 'isureal-worker.service'], restart['loop'])
        self.assertIn('is changed', restart['when'])


if __name__ == '__main__':
    unittest.main()
