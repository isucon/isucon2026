"""Ensure routing cannot expose an unfenced asynchronous Asia standby."""
import copy
import json
from pathlib import Path
import unittest
from runtime_topology import metadata_replicas, read_settings, read_scope, synchronous_standby_names, asia_synchronous


class ReplicaRoutingTest(unittest.TestCase):
    def setUp(self):
        self.runtime = json.loads(Path(__file__).with_name('runtime.json').read_text())
        self.runtime['asiaReplicaSynchronous'] = False
        self.asia = next(a for a in self.runtime['applications'] if a['region'] == 'asia')

    def test_stage_before_routing_and_rollback_preserve_remote_barrier(self):
        runtime = copy.deepcopy(self.runtime)
        runtime.update(asiaReplicaEnabled=False, asiaReplicaReads=False)
        self.assertEqual(read_settings(runtime, self.asia), ('', False))
        self.assertEqual(len(metadata_replicas(runtime)), 2)
        runtime['asiaReplicaEnabled'] = True
        self.assertEqual(read_settings(runtime, self.asia), ('', False))
        replicas = metadata_replicas(runtime)
        self.assertEqual({r['replicaName'] for r in replicas},
                         {'astra_europe', 'astra_america', 'astra_asia'})
        for synchronous in (True, False):
            for regional_fence in (True, False):
                runtime.update(asiaReplicaReads=True, replicasSynchronous=synchronous,
                               readFenceEnabled=regional_fence)
                self.assertEqual(read_settings(runtime, self.asia), ('127.0.0.1', True))
                self.assertEqual(read_settings(runtime, self.asia, writer=True), ('', False))
        runtime['asiaReplicaReads'] = False
        self.assertEqual(read_settings(runtime, self.asia), ('', False))
        self.assertEqual(len(metadata_replicas(runtime)), 3)

    def test_selective_scope_is_limited_to_enabled_asia_reader(self):
        self.runtime.update(asiaReplicaEnabled=True, asiaReplicaReads=True,
                            asiaReplicaReadScope='feed_search')
        self.assertEqual(read_scope(self.runtime, self.asia), 'feed_search')
        self.assertEqual(read_settings(self.runtime, self.asia), ('127.0.0.1', True))
        self.assertEqual(read_scope(self.runtime, self.asia, writer=True), 'all')
        for app in self.runtime['applications']:
            if app['region'] != 'asia':
                self.assertEqual(read_scope(self.runtime, app), 'all')
        self.runtime['asiaReplicaReads'] = False
        self.assertEqual(read_scope(self.runtime, self.asia), 'all')

    def test_reads_cannot_be_enabled_before_standby(self):
        self.runtime.update(asiaReplicaEnabled=False, asiaReplicaReads=True)
        with self.assertRaises(AssertionError):
            read_settings(self.runtime, self.asia)

    def test_three_native_targets_allow_asia_reads_without_per_read_barrier(self):
        self.runtime.update(asiaReplicaEnabled=True, asiaReplicaReads=True,
                            asiaReplicaSynchronous=True, asiaReplicaReadScope='all',
                            writeFenceEnabled=True, writeFenceMode='native')
        self.assertEqual(synchronous_standby_names(self.runtime),
                         'FIRST 3 (astra_europe, astra_america, astra_asia)')
        self.assertEqual(read_settings(self.runtime, self.asia), ('127.0.0.1', False))
        self.assertEqual(read_settings(self.runtime, self.asia, writer=True), ('', False))
        self.assertEqual(read_scope(self.runtime, self.asia), 'all')
        self.runtime['asiaReplicaSynchronous'] = False
        self.assertEqual(read_settings(self.runtime, self.asia), ('127.0.0.1', True))
        self.assertEqual(synchronous_standby_names(self.runtime),
                         'FIRST 2 (astra_europe, astra_america)')

    def test_polling_or_missing_standby_cannot_remove_asia_read_fence(self):
        self.runtime.update(asiaReplicaSynchronous=True, asiaReplicaReadScope='all')
        for changes in ({'writeFenceMode': 'poll'}, {'writeFenceEnabled': False},
                        {'asiaReplicaEnabled': False}, {'replicasEnabled': False},
                        {'asiaReplicaReadScope': 'feed_search'}):
            runtime = {**self.runtime, **changes}
            with self.assertRaises(AssertionError):
                asia_synchronous(runtime)


if __name__ == '__main__':
    unittest.main()
