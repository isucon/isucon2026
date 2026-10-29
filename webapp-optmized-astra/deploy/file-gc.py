"""Delete only unreferenced immutable files after all producers have stopped.

Invoked by asset-gc.py using its validated metadata snapshot. This helper never
infers publication from file age and validates all live references before unlink.
"""
import json
import os
from pathlib import Path
import re
import sys
import uuid


def canonical(value):
    try:
        return str(uuid.UUID(value)) == value
    except (ValueError, AttributeError):
        return False


def archive_reference(key):
    match = re.fullmatch(r'pack:([0-9a-f]{32}):(0|[1-9][0-9]*):([1-9][0-9]*)', key)
    if not match:
        raise ValueError('Invalid archive reference')
    archive,offset,length=match.groups();offset=int(offset);length=int(length)
    if length>32*1024**2 or offset+length>8*1024**3:
        raise ValueError('Invalid archive range')
    return archive,offset+length


def plan(root, keys):
    root = Path(root)
    if root.is_symlink() or not root.is_dir():
        raise ValueError('Invalid file image root')
    if len(keys) != len(set(keys)):
        raise ValueError('Invalid file reference snapshot')
    live = set();packs = {}
    for key in keys:
        if key.startswith('pack:'):
            archive,end=archive_reference(key)
            packs[archive]=max(packs.get(archive,0),end)
        elif canonical(key):live.add(key)
        else:raise ValueError('Invalid file reference snapshot')
    for archive,end in packs.items():
        directory=root/'packs';path=directory/archive
        if directory.is_symlink() or path.is_symlink() or not path.is_file() or not end<=path.stat().st_size<=8*1024**3:
            raise ValueError('A published archive is missing or truncated; abort cleanup')
    for key in live:
        bucket = root / key[:2]
        path = bucket / key
        if bucket.is_symlink() or path.is_symlink() or not path.is_file() or path.stat().st_size == 0:
            raise ValueError('A published image file is missing or invalid; abort cleanup')
    remove = []
    for bucket in root.iterdir():
        if bucket.name == 'packs' and not bucket.is_symlink() and bucket.is_dir():
            for path in bucket.iterdir():
                if path.is_symlink() or not path.is_file():
                    raise ValueError('Unexpected archive file; abort cleanup')
                if re.fullmatch('[0-9a-f]{32}',path.name):
                    if path.name not in packs:remove.append(path)
                elif re.fullmatch(r'\.[0-9a-f]{32}\.tmp',path.name):remove.append(path)
                else:raise ValueError('Unexpected archive filename; abort cleanup')
            continue
        if bucket.is_symlink() or not bucket.is_dir() or not re.fullmatch('[0-9a-f]{2}', bucket.name):
            raise ValueError('Unexpected image directory; abort cleanup')
        for path in bucket.iterdir():
            if path.is_symlink() or not path.is_file():
                raise ValueError('Unexpected image file; abort cleanup')
            if canonical(path.name) and path.name[:2] == bucket.name:
                if path.name not in live:
                    remove.append(path)
            elif path.name.startswith('.') and path.name.endswith('.tmp') and canonical(path.name[1:-4]):
                remove.append(path)
            else:
                raise ValueError('Unexpected image filename; abort cleanup')
    return remove


def collect(root, keys, apply):
    remove = plan(root, keys)
    total = sum(path.stat().st_size for path in remove)
    if apply:
        buckets = set()
        for path in remove:
            path.unlink()
            buckets.add(path.parent)
        for bucket in buckets:
            fd = os.open(bucket, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
    return {'retained': len(keys), 'orphan_files': len(remove), 'orphan_bytes': total, 'applied': apply}


if __name__ == '__main__':
    mode, refs, expected = sys.argv[1:]
    assert mode in ('check', 'apply')
    assert re.fullmatch('/var/lib/astra-maint/[0-9a-f]{32}/file-refs', refs)
    keys = Path(refs).read_text().splitlines()
    assert len(keys) == int(expected), 'File reference count mismatch'
    print(json.dumps(collect('/var/lib/astra-images', keys, mode == 'apply')))
