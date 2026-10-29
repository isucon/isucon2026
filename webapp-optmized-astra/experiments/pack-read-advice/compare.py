"""Compare reopening small random ranges from an owned temporary pack on Linux."""
import hashlib
import json
import os
from pathlib import Path
import random
import tempfile
import time


def read_bytes():
    values = dict(line.split(': ', 1) for line in Path('/proc/self/io').read_text().splitlines())
    return int(values['read_bytes'])


size = 256 * 1024 * 1024
length = 8192
rng = random.Random(47)
offsets = [rng.randrange(size - length) for _ in range(4096)]
results = []
with tempfile.TemporaryDirectory(prefix='astra-pack-read-') as directory:
    path = Path(directory) / 'owned-test-pack'
    chunk = rng.randbytes(1024 * 1024)
    with path.open('wb') as stream:
        for _ in range(size // len(chunk)):
            stream.write(chunk)
        stream.flush()
        os.fsync(stream.fileno())
    expected = None
    for advice in (os.POSIX_FADV_NORMAL, os.POSIX_FADV_RANDOM, os.POSIX_FADV_RANDOM, os.POSIX_FADV_NORMAL):
        with path.open('rb') as stream:
            os.posix_fadvise(stream.fileno(), 0, 0, os.POSIX_FADV_DONTNEED)
        start_bytes = read_bytes()
        started = time.perf_counter()
        digest = hashlib.sha256()
        for offset in offsets:
            fd = os.open(path, os.O_RDONLY)
            try:
                if advice == os.POSIX_FADV_RANDOM:
                    os.posix_fadvise(fd, 0, 0, advice)
                data = os.pread(fd, length, offset)
                assert len(data) == length
                digest.update(data)
            finally:
                os.close(fd)
        elapsed = time.perf_counter() - started
        checksum = digest.hexdigest()
        if expected is None:
            expected = checksum
        assert checksum == expected
        results.append(dict(advice='random' if advice == os.POSIX_FADV_RANDOM else 'normal', seconds=elapsed,
                            storage_read_bytes=read_bytes() - start_bytes, checksum=checksum))
print(json.dumps(dict(file_bytes=size, ranges=len(offsets), range_bytes=length, results=results), indent=2))
