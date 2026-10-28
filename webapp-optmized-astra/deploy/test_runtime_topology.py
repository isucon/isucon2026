"""Ensure routing cannot expose an unfenced asynchronous Asia standby."""
import copy
import json
from pathlib import Path
import unittest
from runtime_topology import metadata_replicas, read_settings


class ReplicaRoutingTest(unittest.TestCase):
    def setUp(self):
        self.runtime = json.loads(Path(__file__).with_name('runtime.json').read_text())
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

    def test_reads_cannot_be_enabled_before_standby(self):
        self.runtime.update(asiaReplicaEnabled=False, asiaReplicaReads=True)
        with self.assertRaises(AssertionError):
            read_settings(self.runtime, self.asia)


if __name__ == '__main__':
    unittest.main()
