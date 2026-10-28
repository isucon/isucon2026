"""Regional GC integrity tests; SQL cases use an isolated local Docker schema."""
import importlib.util
import os
from pathlib import Path
import shlex
import subprocess
import unittest
import uuid

spec = importlib.util.spec_from_file_location('asset_gc', Path(__file__).resolve().parents[1] / 'deploy/asset-gc.py')
gc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gc)
KEY = '00000000-0000-0000-0000-000000000001'
ORPHAN = '00000000-0000-0000-0000-000000000002'


class References(unittest.TestCase):
    def test_same_uuid_in_different_regions_is_not_aliased(self):
        self.assertEqual(gc.split_references([KEY, 'europe:'+KEY, 'america:'+KEY], ['asia','europe','america']),
                         dict.fromkeys(['asia','europe','america'], [KEY]))

    def test_unknown_disabled_and_invalid_stores_abort(self):
        for keys in [[KEY, 'europe:'+KEY], ['unknown:'+KEY], ['america:bad'], [KEY,KEY], [KEY,'asia:'+KEY]]:
            with self.assertRaises(ValueError):
                gc.split_references(keys, ['asia'])
        with self.assertRaises(ValueError):
            gc.split_references([KEY,'asia:'+KEY], ['asia','europe','america'])

    def test_empty_store_and_snapshot(self):
        self.assertEqual(gc.split_references([KEY], ['asia','europe']), {'asia':[KEY],'europe':[]})
        self.assertEqual(gc.split_references([], ['asia']), {'asia':[]})


@unittest.skipUnless(os.environ.get('ASTRA_GC_TEST_CONTAINER'), 'set a local PostgreSQL Docker container')
class CleanupSQL(unittest.TestCase):
    def setUp(self):
        self.container = os.environ['ASTRA_GC_TEST_CONTAINER']
        assert self.container.startswith('astra-local-')
        self.schema = 'gc_test_'+uuid.uuid4().hex
        self.folder = '/var/lib/astra-maint/'+uuid.uuid4().hex
        subprocess.run(['docker','exec',self.container,'mkdir','-p',self.folder], check=True)
        self.sql('CREATE SCHEMA '+self.schema+'; CREATE TABLE astra_image_data(id varchar(36) PRIMARY KEY,data bytea);'
                 f"INSERT INTO astra_image_data VALUES ('{KEY}','\\x010203'),('{ORPHAN}','\\x040506');")

    def sql(self, sql, check=True):
        return subprocess.run(['docker','exec','-i','-e','PGOPTIONS=-csearch_path='+self.schema,
                               self.container,'psql','-XAt','-U','postgres','-v','ON_ERROR_STOP=1'],
                              input=sql, text=True, capture_output=True, check=check)

    def cleanup(self, keys, expected=None):
        path=self.folder+'/refs'
        subprocess.run(['docker','exec','-i',self.container,'sh','-c','cat > '+shlex.quote(path)],
                       input=''.join(key+'\n' for key in keys), text=True, check=True)
        return self.sql(gc.cleanup_sql(path, len(keys) if expected is None else expected),check=False)

    def test_published_bytes_survive_and_orphan_is_removed(self):
        result=self.cleanup([KEY])
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(self.sql("SELECT id,encode(data,'hex') FROM astra_image_data").stdout.strip(),KEY+'|010203')

    def test_missing_published_image_aborts_without_deletion(self):
        result=self.cleanup(['00000000-0000-0000-0000-000000000003'])
        self.assertNotEqual(result.returncode,0)
        self.assertIn('a published image is missing',result.stderr)
        self.assertEqual(self.sql('SELECT count(*) FROM astra_image_data').stdout.strip(),'2')

    def test_snapshot_count_mismatch_aborts_without_deletion(self):
        self.assertNotEqual(self.cleanup([KEY],2).returncode,0)
        self.assertEqual(self.sql('SELECT count(*) FROM astra_image_data').stdout.strip(),'2')

    def test_no_published_references_removes_only_asset_rows(self):
        self.assertEqual(self.cleanup([]).returncode,0)
        self.assertEqual(self.sql('SELECT count(*) FROM astra_image_data').stdout.strip(),'0')

    def tearDown(self):
        self.sql('DROP SCHEMA '+self.schema+' CASCADE;')
        subprocess.run(['docker','exec',self.container,'rm','-rf',self.folder],check=True)


if __name__ == '__main__':
    unittest.main()
