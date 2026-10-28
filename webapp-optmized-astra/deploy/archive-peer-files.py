"""Owned db1 archive files: immutable copies, publication checks, and HTTP ranges."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sys
import tempfile
import urllib.request

ROOT = Path('/var/lib/astra-images/packs')
MANIFEST = Path('/var/lib/astra-images/.archive-peer-manifest.json')
STAGED = Path('/home/isucon/astra-deploy/archive-peer-manifest.json')
SOURCE = 'http://192.168.145.10:8082/internal/astra/packs/'
PEER = 'http://192.168.148.10:8082/internal/astra/packs/'


def validate_manifest(document):
    archives = document['archives']
    assert isinstance(archives, list) and 0 < len(archives) <= 256
    names = set()
    for item in archives:
        name = item['archive']
        assert re.fullmatch('[0-9a-f]{32}', name) and name not in names
        names.add(name)
        assert type(item['bytes']) is int and 0 < item['bytes'] <= 8 * 1024**3
        assert re.fullmatch('[0-9a-f]{64}', item['sha256'])
    return archives


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def matches(path, item):
    if not path.exists():
        return False
    assert path.is_file() and not path.is_symlink()
    assert path.stat().st_size == item['bytes']
    assert path.stat().st_mode & 0o777 == 0o444
    with path.open('rb') as file:
        assert hashlib.file_digest(file, 'sha256').hexdigest() == item['sha256']
    return True


def copy_archive(root, source, item):
    destination = root / item['archive']
    if matches(destination, item):
        return 'already verified'
    temporary = None
    try:
        request = urllib.request.Request(source + item['archive'], headers={'Accept-Encoding': 'identity'})
        with urllib.request.urlopen(request, timeout=30) as response:
            assert response.status == 200 and int(response.headers['Content-Length']) == item['bytes']
            with tempfile.NamedTemporaryFile(dir=root, prefix='.archive-copy-', delete=False) as file:
                temporary = Path(file.name)
                digest = hashlib.sha256()
                count = 0
                while chunk := response.read(1024 * 1024):
                    count += len(chunk)
                    assert count <= item['bytes']
                    digest.update(chunk)
                    file.write(chunk)
                assert count == item['bytes'] and digest.hexdigest() == item['sha256']
                file.flush()
                os.fchmod(file.fileno(), 0o444)
                os.fsync(file.fileno())
        # Refuse to replace any already published immutable archive.
        os.link(temporary, destination)
        temporary.unlink()
        temporary = None
        sync_directory(root)
        return 'copied and synced'
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def install_manifest(staged, destination):
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=destination.parent, prefix='.archive-manifest-', delete=False) as file:
            temporary = Path(file.name)
            file.write(staged.read_bytes())
            file.flush()
            os.fchmod(file.fileno(), 0o644)
            os.fsync(file.fileno())
        os.replace(temporary, destination)
        temporary = None
        sync_directory(destination.parent)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def verify_files(root, archives, peer):
    checked = 0
    for item in archives:
        path = root / item['archive']
        assert path.is_file() and not path.is_symlink()
        assert path.stat().st_size == item['bytes'] and path.stat().st_mode & 0o777 == 0o444
        length = min(item['bytes'], 4096)
        for offset in sorted({0, (item['bytes'] - length) // 2, item['bytes'] - length}):
            last = offset + length - 1
            request = urllib.request.Request(peer + item['archive'], headers={'Range': f'bytes={offset}-{last}', 'Accept-Encoding': 'identity'})
            with urllib.request.urlopen(request, timeout=10) as response:
                assert response.status == 206
                assert response.headers['Content-Range'] == f'bytes {offset}-{last}/{item["bytes"]}'
                actual = response.read(length + 1)
            with path.open('rb') as file:
                file.seek(offset)
                assert actual == file.read(length) and len(actual) == length
            checked += 1
    return checked


def main():
    assert sys.argv[1:] in (['copy'], ['verify'])
    staged = json.loads(STAGED.read_text())
    archives = validate_manifest(staged)
    assert ROOT.is_dir() and not ROOT.is_symlink()
    if sys.argv[1] == 'copy':
        missing_bytes = sum(item['bytes'] for item in archives if not (ROOT / item['archive']).exists())
        assert shutil.disk_usage(ROOT).free > missing_bytes + 5 * 1024**3
        for item in archives:
            status = copy_archive(ROOT, SOURCE, item)
            print(json.dumps({'archive': item['archive'], 'bytes': item['bytes'], 'status': status}), flush=True)
        install_manifest(STAGED, MANIFEST)
    assert json.loads(MANIFEST.read_text()) == staged
    ranges = verify_files(ROOT, archives, PEER)
    print(json.dumps({'archives': len(archives), 'bytes': sum(item['bytes'] for item in archives), 'verified_ranges': ranges, 'manifest_sha256': hashlib.sha256(STAGED.read_bytes()).hexdigest()}), flush=True)


if __name__ == '__main__':
    main()
