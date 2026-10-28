import hashlib,hmac,json,struct,uuid
from pathlib import Path
p=Path("var/astra/signatures.bin")
assert p.stat().st_mode & 0o077 == 0
with p.open("rb") as f:
 header=f.read(16)
 assert header[:8]==b"ASTRAS01"
 expires=struct.unpack("<q",header[8:])[0]
 rows=(p.stat().st_size-16)//80
 indexes=sorted({0,rows-1,*[rows*i//128 for i in range(128)]})
 for i in indexes:
  f.seek(16+i*80);record=f.read(80)
  key=record[:32];image=str(uuid.UUID(bytes=record[32:48]))
  signature=hmac.digest(key,f"{expires}:{image}".encode(),"sha256")
  for _ in range(999):signature=hmac.digest(key,signature,"sha256")
  assert signature==record[48:]
with p.open("rb") as f:digest=hashlib.file_digest(f,"sha256").hexdigest()
print(json.dumps(dict(rows=rows,bytes=p.stat().st_size,expires=expires,sha256=digest,independent_hmac_samples=len(indexes),mode=oct(p.stat().st_mode&0o777))))
