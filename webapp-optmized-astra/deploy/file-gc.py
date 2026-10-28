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


def plan(root, keys):
    root = Path(root)
    if root.is_symlink() or not root.is_dir():
        raise ValueError('Invalid file image root')
    if len(keys) != len(set(keys)) or any(not canonical(key) for key in keys):
        raise ValueError('Invalid file reference snapshot')
    live = set(keys)
    for key in live:
        bucket = root / key[:2]
        path = bucket / key
        if bucket.is_symlink() or path.is_symlink() or not path.is_file() or path.stat().st_size == 0:
            raise ValueError('A published image file is missing or invalid; abort cleanup')
    remove = []
    for bucket in root.iterdir():
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
