"""File GC keeps all published images and rejects incomplete snapshots."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
import uuid

folder = Path(__file__).resolve().parents[1] / 'deploy'

def module(name, file):
    spec = importlib.util.spec_from_file_location(name, folder / file)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded

files = module('file_gc', 'file-gc.py')
gc = module('asset_gc', 'asset-gc.py')


class FileCleanup(unittest.TestCase):
    def test_archives_are_retained_until_the_last_published_range_is_removed(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);packs=root/'packs';packs.mkdir()
            live,orphan=[uuid.uuid4().hex for _ in range(2)]
            (packs/live).write_bytes(b'firstsecond');(packs/orphan).write_bytes(b'orphan')
            refs=[f'pack:{live}:0:5',f'pack:{live}:5:6']
            result=files.collect(root,refs,True)
            self.assertEqual(result['orphan_files'],1)
            self.assertEqual((packs/live).read_bytes(),b'firstsecond')
            files.collect(root,refs[1:],True)
            self.assertTrue((packs/live).exists())
            files.collect(root,[],True)
            self.assertFalse((packs/live).exists())

    def test_truncated_archive_aborts_without_removing_orphans(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);packs=root/'packs';packs.mkdir()
            live,orphan=[uuid.uuid4().hex for _ in range(2)]
            (packs/live).write_bytes(b'short');(packs/orphan).write_bytes(b'orphan')
            with self.assertRaisesRegex(ValueError,'published archive'):
                files.collect(root,[f'pack:{live}:4:2'],True)
            self.assertTrue((packs/orphan).exists())

    def test_archive_snapshots_require_all_regions_and_valid_ranges(self):
        key=f'pack:{uuid.uuid4().hex}:0:1'
        regions=['asia','europe','america']
        grouped=gc.split_references([key],regions,regions,True)
        self.assertEqual(grouped['pack'],[key])
        with self.assertRaises(ValueError):gc.split_references([key],regions,regions)
        for invalid in [key+':extra',key[:-1]+'0',key.replace(':0:',':8589934592:')]:
            with self.assertRaises(ValueError):gc.split_references([invalid],regions,regions,True)

    def test_mixed_references_have_distinct_storage_namespaces(self):
        key = str(uuid.uuid4())
        result = gc.split_references([key, 'europe:'+key, 'fs:asia:'+key, 'fs:europe:'+key], ['asia','europe'], ['asia','europe'])
        self.assertEqual(result, {'asia':[key], 'europe':[key], 'fs:asia':[key], 'fs:europe':[key]})
        for value in ['fs:unknown:'+key, 'fs:asia:invalid', 'fs:asia:'+key+':extra']:
            with self.assertRaises(ValueError):
                gc.split_references([value], ['asia'], ['asia'])
        with self.assertRaises(ValueError):
            gc.split_references(['fs:asia:'+key], ['asia'])

    def test_published_files_remain_and_orphans_and_temporary_files_are_removed(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            keep,orphan=[str(uuid.uuid4()) for _ in range(2)]
            for key,data in [(keep,b'published'),(orphan,b'orphan')]:
                (root/key[:2]).mkdir(exist_ok=True)
                (root/key[:2]/key).write_bytes(data)
            temporary=root/keep[:2]/('.'+str(uuid.uuid4())+'.tmp')
            temporary.write_bytes(b'incomplete')
            preview=files.collect(root,[keep],False)
            self.assertEqual(preview['orphan_files'],2)
            self.assertTrue(temporary.exists())
            result=files.collect(root,[keep],True)
            self.assertEqual(result['retained'],1)
            self.assertEqual((root/keep[:2]/keep).read_bytes(),b'published')
            self.assertFalse((root/orphan[:2]/orphan).exists())
            self.assertFalse(temporary.exists())

    def test_missing_published_file_aborts_before_any_deletion(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);key=str(uuid.uuid4());(root/key[:2]).mkdir();path=root/key[:2]/key;path.write_bytes(b'orphan')
            with self.assertRaisesRegex(ValueError,'published image file'):
                files.collect(root,[str(uuid.uuid4())],True)
            self.assertEqual(path.read_bytes(),b'orphan')

    def test_symlinks_and_unknown_files_abort_before_any_deletion(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);key=str(uuid.uuid4());bucket=root/key[:2];bucket.mkdir();path=bucket/key;path.write_bytes(b'orphan')
            foreign=bucket/'unknown';foreign.write_bytes(b'unrelated')
            with self.assertRaisesRegex(ValueError,'Unexpected image filename'):
                files.collect(root,[],True)
            self.assertTrue(path.exists());foreign.unlink()
            foreign=bucket/str(uuid.uuid4());foreign.symlink_to(path)
            with self.assertRaisesRegex(ValueError,'Unexpected image file'):
                files.collect(root,[],True)
            self.assertTrue(path.exists())


if __name__ == '__main__':
    unittest.main()
