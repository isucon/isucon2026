import importlib.util,tempfile,uuid,struct,unittest
from pathlib import Path
spec=importlib.util.spec_from_file_location('builder',Path(__file__).with_name('build-owner-archive.py'));builder=importlib.util.module_from_spec(spec);spec.loader.exec_module(builder)
class OwnerArchiveTest(unittest.TestCase):
 def test_exact_bytes_and_owner_contiguity_across_source_archives(self):
  with tempfile.TemporaryDirectory() as temporary:
   root=Path(temporary);original=root/'original';original.mkdir();target=root/'target';source=root/'source.tsv'
   a=uuid.UUID(int=1).hex;b=uuid.UUID(int=2).hex
   (original/a).write_bytes(b'ABCdefgh');(original/b).write_bytes(b'123456789')
   first=str(uuid.UUID(int=11));second=str(uuid.UUID(int=22))
   source.write_text(f'{first}\tpack:{a}:0:3\n{first}\tpack:{b}:0:4\n{second}\tpack:{a}:3:5\n{second}\tpack:{b}:4:5\n')
   previous=builder.LIMIT;builder.LIMIT=3
   try:report=builder.build(source,original,target,reserve=0)
   finally:builder.LIMIT=previous
   self.assertEqual(report['images'],4);self.assertEqual(report['owners'],2);self.assertEqual(len(report['packs']),2)
   self.assertEqual((target/report['packs'][0]['name']).read_bytes(),b'ABC1234')
   self.assertEqual((target/report['packs'][1]['name']).read_bytes(),b'defgh56789')
   data=(target/'index.bin').read_bytes();self.assertEqual(data[:8],b'ASTRAOA1');self.assertEqual(struct.unpack('<Q',data[8:16])[0],4)
   for i in range(4):
    src,off,length,dst,new=builder.RECORD.unpack(data[16+52*i:16+52*(i+1)])
    self.assertEqual((original/uuid.UUID(bytes=src).hex).read_bytes()[off:off+length],(target/uuid.UUID(bytes=dst).hex).read_bytes()[new:new+length])
   self.assertEqual(builder.build(source,original,target,reserve=0),report)
   (target/report['packs'][0]['name']).chmod(0o644);(target/report['packs'][0]['name']).write_bytes(b'broken')
   with self.assertRaises(AssertionError):builder.build(source,original,target,reserve=0)
 def test_truncated_source_never_publishes_index(self):
  with tempfile.TemporaryDirectory() as temporary:
   root=Path(temporary);original=root/'original';original.mkdir();target=root/'target';source=root/'source.tsv';a=uuid.UUID(int=1).hex
   (original/a).write_bytes(b'a');source.write_text(f'{uuid.UUID(int=11)}\tpack:{a}:0:2\n')
   with self.assertRaises(AssertionError):builder.build(source,original,target,reserve=0)
   self.assertFalse((target/'index.bin').exists());self.assertEqual((original/a).read_bytes(),b'a')
if __name__=='__main__':unittest.main()
