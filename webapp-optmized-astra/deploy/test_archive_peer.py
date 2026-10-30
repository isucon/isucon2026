"""Exercise publication failures and byte ranges without AWS access."""
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
from pathlib import Path
import tempfile
import threading
import unittest

spec = importlib.util.spec_from_file_location('archive_peer_files', Path(__file__).with_name('archive-peer-files.py'))
peer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(peer)


class ArchivePeerTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.data = bytes(range(256)) * 64
        self.item = {'archive': 'a' * 32, 'bytes': len(self.data), 'sha256': hashlib.sha256(self.data).hexdigest()}
        self.mode = 'good'
        self.requests = 0
        test = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                test.requests += 1
                body = test.data
                range_header = self.headers.get('Range')
                if range_header:
                    start, end = map(int, range_header.removeprefix('bytes=').split('-'))
                    body = body[start:end + 1]
                    self.send_response(206)
                    self.send_header('Content-Range', f'bytes {start}-{end}/{len(test.data) + (test.mode == "wrong-range")}')
                else:
                    self.send_response(200)
                if test.mode == 'wrong-body':
                    body = b'x' * len(body)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body[:-1] if test.mode == 'truncated' else body)

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.stop_server)
        self.url = f'http://127.0.0.1:{self.server.server_port}/'

    def stop_server(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def test_manifest_rejects_invalid_entries(self):
        for field, value in [('archive', '../' + 'a' * 29), ('bytes', 0), ('bytes', True), ('bytes', 8 * 1024**3 + 1), ('sha256', 'x' * 64)]:
            with self.subTest(field=field, value=value), self.assertRaises(AssertionError):
                peer.validate_manifest({'archives': [dict(self.item, **{field: value})]})
        with self.assertRaises(AssertionError):
            peer.validate_manifest({'archives': [self.item, self.item]})
        self.assertEqual(peer.validate_manifest({'archives': [self.item]}), [self.item])

    def test_copy_is_complete_immutable_and_idempotent(self):
        self.assertEqual(peer.copy_archive(self.root, self.url, self.item), 'copied and synced')
        path = self.root / self.item['archive']
        self.assertEqual(path.read_bytes(), self.data)
        self.assertEqual(path.stat().st_mode & 0o777, 0o444)
        self.assertEqual(peer.copy_archive(self.root, self.url, self.item), 'already verified')
        self.assertEqual(self.requests, 1)
        self.assertEqual(peer.verify_files(self.root, [self.item], self.url), 3)

    def test_failed_copy_never_publishes_or_removes_other_files(self):
        unrelated = self.root / '.archive-copy-unrelated'
        unrelated.write_bytes(b'keep')
        for mode in ['wrong-body', 'truncated']:
            with self.subTest(mode=mode):
                self.mode = mode
                with self.assertRaises((AssertionError, OSError)):
                    peer.copy_archive(self.root, self.url, self.item)
                self.assertEqual(list(self.root.iterdir()), [unrelated])
                self.assertEqual(unrelated.read_bytes(), b'keep')

    def test_existing_bad_file_is_not_overwritten(self):
        path = self.root / self.item['archive']
        path.write_bytes(b'x' * len(self.data))
        path.chmod(0o444)
        with self.assertRaises(AssertionError):
            peer.copy_archive(self.root, self.url, self.item)
        self.assertEqual(path.read_bytes(), b'x' * len(self.data))
        self.assertEqual(self.requests, 0)

    def test_verification_rejects_wrong_range_or_bytes(self):
        peer.copy_archive(self.root, self.url, self.item)
        for mode in ['wrong-range', 'wrong-body']:
            with self.subTest(mode=mode):
                self.mode = mode
                with self.assertRaises(AssertionError):
                    peer.verify_files(self.root, [self.item], self.url)


if __name__ == '__main__':
    unittest.main()
