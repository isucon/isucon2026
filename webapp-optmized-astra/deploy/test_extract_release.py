import io
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

SCRIPT=Path(__file__).with_name('extract-release.sh')

class ExtractReleaseTest(unittest.TestCase):
    def test_valid_release_replaces_files_after_full_extraction(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);archive=root/'release.tar.gz';destination=root/'app'
            with tarfile.open(archive,'w:gz') as output:
                for name in ['rust/isureal','rust/archive-images','sql/init.sh']:
                    member=tarfile.TarInfo(name);member.size=3;member.mode=0o755
                    output.addfile(member,io.BytesIO(b'new'))
            (destination/'rust').mkdir(parents=True)
            (destination/'rust/isureal').write_text('old')
            subprocess.run(['bash',str(SCRIPT),str(archive),str(destination)],check=True)
            self.assertEqual((destination/'rust/isureal').read_text(),'new')
            self.assertEqual(list(destination.glob('.release.*')),[])

    def test_incomplete_archive_leaves_running_release_untouched(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);archive=root/'release.tar.gz';destination=root/'app'
            with tarfile.open(archive,'w:gz') as output:
                member=tarfile.TarInfo('rust/isureal');member.size=3;member.mode=0o755
                output.addfile(member,io.BytesIO(b'new'))
            (destination/'rust').mkdir(parents=True)
            (destination/'rust/isureal').write_text('old')
            result=subprocess.run(['bash',str(SCRIPT),str(archive),str(destination)],capture_output=True)
            self.assertNotEqual(result.returncode,0)
            self.assertEqual((destination/'rust/isureal').read_text(),'old')
            self.assertEqual(list(destination.glob('.release.*')),[])

    def test_invalid_staged_executable_leaves_previous_release_untouched(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);archive=root/'release.tar.gz';destination=root/'app'
            with tarfile.open(archive,'w:gz') as output:
                for name in ['rust/isureal','rust/archive-images','sql/init.sh']:
                    member=tarfile.TarInfo(name);member.size=3
                    member.mode=0o644 if name=='rust/archive-images' else 0o755
                    output.addfile(member,io.BytesIO(b'new'))
            (destination/'rust').mkdir(parents=True)
            (destination/'rust/isureal').write_text('old')
            result=subprocess.run(['bash',str(SCRIPT),str(archive),str(destination)],capture_output=True)
            self.assertNotEqual(result.returncode,0)
            self.assertEqual((destination/'rust/isureal').read_text(),'old')
            self.assertEqual(list(destination.glob('.release.*')),[])

if __name__=='__main__':unittest.main()
