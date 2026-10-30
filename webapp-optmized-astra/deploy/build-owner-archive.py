"""Create a verified, optional immutable layout without modifying SQL or sources."""
import hashlib,json,os,struct,sys,uuid
from pathlib import Path
RECORD=struct.Struct('<16sQI16sQ')
LIMIT=2*1024**3

def digest(path):
 with path.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()

def build(source,original,root,reserve=4*1024**3):
 source=Path(source);original=Path(original);root=Path(root)
 input_sha=digest(source)
 rows=[];seen=set();previous=None;total=0;owners=set()
 for line in source.read_text().splitlines():
  owner,key=line.split('\t');assert str(uuid.UUID(owner))==owner
  prefix,archive,offset,length=key.split(':');assert prefix=='pack' and uuid.UUID(archive).hex==archive
  offset=int(offset);length=int(length);assert 0<length<=32*1024**2 and 0<=offset and offset+length<=8*1024**3
  assert key==f'pack:{archive}:{offset}:{length}' and key not in seen
  assert previous is None or previous<(owner,key)
  previous=(owner,key);seen.add(key);owners.add(owner);total+=length;rows.append((owner,archive,offset,length))
 assert rows
 root.mkdir(parents=True,exist_ok=True)
 report_path=root/'report.json'
 if report_path.exists():
  report=json.loads(report_path.read_text());assert report['source_sha256']==input_sha
  assert digest(root/'index.bin')==report['index_sha256']
  for p in report['packs']:assert (root/p['name']).stat().st_size==p['bytes'] and digest(root/p['name'])==p['sha256']
  return report
 assert not list(root.iterdir()),'output must be empty or already fully verified'
 free=os.statvfs(root);assert free.f_bavail*free.f_frsize>=total+len(rows)*52+reserve,'insufficient free space'
 descriptors={a:os.open(original/a,os.O_RDONLY) for a in {r[1] for r in rows}}
 index=[];packs=[];out=None;position=0;last_owner=None;hasher=None;name=None
 def finish():
  if out is None:return
  out.flush();os.fsync(out.fileno());out.close()
  path=root/(name+'.tmp');expected=hasher.hexdigest();assert digest(path)==expected,'copied archive hash mismatch'
  os.rename(path,root/name);packs.append(dict(name=name,bytes=position,sha256=expected))
 try:
  for owner,archive,offset,length in rows:
   if out is None or (position>=LIMIT and owner!=last_owner):
    finish();name=hashlib.sha256((input_sha+':'+str(len(packs))).encode()).hexdigest()[:32]
    out=(root/(name+'.tmp')).open('xb');os.chmod(root/(name+'.tmp'),0o444);position=0;hasher=hashlib.sha256()
   data=os.pread(descriptors[archive],length,offset);assert len(data)==length,'truncated original archive'
   assert position+length<=8*1024**3
   out.write(data);hasher.update(data)
   index.append((uuid.UUID(archive).bytes,offset,length,uuid.UUID(name).bytes,position))
   position+=length;last_owner=owner
  finish();out=None
 finally:
  if out is not None and not out.closed:out.close()
  for fd in descriptors.values():os.close(fd)
 index.sort(key=lambda r:r[:3]);payload=b'ASTRAOA1'+struct.pack('<Q',len(index))+b''.join(RECORD.pack(*r) for r in index)
 temporary=root/'index.bin.tmp'
 with temporary.open('xb') as f:f.write(payload);f.flush();os.fsync(f.fileno())
 os.chmod(temporary,0o444);os.rename(temporary,root/'index.bin')
 report=dict(source_sha256=input_sha,index_sha256=hashlib.sha256(payload).hexdigest(),images=len(rows),owners=len(owners),bytes=total,source_archives=len(descriptors),packs=packs)
 with (root/'report.json.tmp').open('x') as f:json.dump(report,f,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())
 os.rename(root/'report.json.tmp',report_path);fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY);os.fsync(fd);os.close(fd)
 return report
if __name__=='__main__':print(json.dumps(build(*sys.argv[1:])))
