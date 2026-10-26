"""sanitize_archive.py がテストと [作問] コメントだけを取り除くことを確かめる。"""

import io
import pathlib
import shutil
import sys
import tarfile
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1] / "scripts"))

import sanitize_archive  # noqa: E402


def write_archive(path, files):
    with tarfile.open(path, "w:gz") as archive:
        for name, content in files.items():
            if content is None:
                member = tarfile.TarInfo(name)
                member.type = tarfile.DIRTYPE
                member.mode = 0o755
                archive.addfile(member)
                continue
            member = tarfile.TarInfo(name)
            member.size = len(content)
            member.mode = 0o755 if name.endswith(".sh") else 0o644
            member.mtime = 1_700_000_000
            archive.addfile(member, io.BytesIO(content))


def read_archive(path):
    with tarfile.open(path, "r:gz") as archive:
        return {
            member.name: (archive.extractfile(member).read() if member.isfile() else None, member.mode)
            for member in archive
        }


class StripCommentsTest(unittest.TestCase):
    def test_removes_comment_lines_of_each_language(self):
        cases = {
            "go": "\tx := 1\n\t// [作問] 説明\n\ty := 2\n",
            "rust": "    let x = 1;\n    // [作問] 説明\n    let y = 2;\n",
            "ruby": "      x = 1\n      # [作問] 説明\n      y = 2\n",
            "python": "    x = 1\n    # [作問] 説明\n    y = 2\n",
            "php": "        $x = 1;\n        // [作問] 説明\n        $y = 2;\n",
            "perl": "    my $x = 1;\n    # [作問] 説明\n    my $y = 2;\n",
            "sql": "x;\n-- [作問] 説明\n-- [作問] 説明の続き\ny;\n",
        }
        for language, source in cases.items():
            with self.subTest(language=language):
                result = sanitize_archive.strip_comments(source)
                self.assertNotIn("作問", result)
                self.assertEqual(result.count("\n"), source.count("\n") - source.count("[作問]"))

    def test_cuts_trailing_comment(self):
        source = "\tRegion string `json:\"region\"` // [作問] 説明\n"
        self.assertEqual(sanitize_archive.strip_comments(source), "\tRegion string `json:\"region\"`\n")

    def test_collapses_blank_lines_left_by_removed_block(self):
        source = "a;\n\n-- [作問] 1\n-- [作問] 2\n\n-- 残すコメント\nb;\n"
        self.assertEqual(sanitize_archive.strip_comments(source), "a;\n\n-- 残すコメント\nb;\n")

    def test_keeps_blank_line_after_code(self):
        source = "a;\n-- [作問] 1\n\nb;\n"
        self.assertEqual(sanitize_archive.strip_comments(source), "a;\n\nb;\n")

    def test_keeps_crlf(self):
        source = "a\r\n# [作問] 1\r\nb # [作問] 2\r\n"
        self.assertEqual(sanitize_archive.strip_comments(source), "a\r\nb\r\n")

    def test_keeps_other_comments(self):
        source = "// 作問とは関係ない [作問]\n# [作問者]\ni-- // [作問]\n"
        self.assertEqual(sanitize_archive.strip_comments(source), "// 作問とは関係ない [作問]\n# [作問者]\ni--\n")


class SanitizeFileTest(unittest.TestCase):
    @unittest.skipUnless(shutil.which("gofmt"), "gofmt が要る")
    def test_realigns_go_struct_fields(self):
        source = "package main\n\ntype r struct {\n\tUsername string\n\t// [作問] 説明\n\tID int\n}\n"
        self.assertEqual(
            sanitize_archive.sanitize_file("go/a.go", source.encode()),
            (b"package main\n\ntype r struct {\n\tUsername string\n\tID       int\n}\n", []),
        )

    def test_does_not_format_other_languages(self):
        source = "x  =  1 # [作問] 説明\n"
        self.assertEqual(sanitize_archive.sanitize_file("ruby/a.rb", source.encode()), (b"x  =  1\n", []))


class IsTestTest(unittest.TestCase):
    def test_detects_tests_of_each_language(self):
        for name in [
            "go/user_api_test.go",
            "go/testdata/fixture.json",
            "./guard_test.go",
            "ruby/test/test_helper.rb",
            "ruby/lib/user_test.rb",
            "rust/tests/action_log_test.rs",
            "python/tests/conftest.py",
            "python/isureal/test_config.py",
            "php/tests/Unit/RegionTest.php",
            "perl/t/00_compile.t",
            "nodejs/test/api.test.ts",
        ]:
            with self.subTest(name=name):
                self.assertTrue(sanitize_archive.is_test(name, is_dir=False))
        for name in ["rust/tests", "perl/t", "ruby/test"]:
            with self.subTest(name=name):
                self.assertTrue(sanitize_archive.is_test(name, is_dir=True))

    def test_keeps_application_files(self):
        for name in [
            "go/server.go",
            "go/testdata.go",
            "rust/src/lib.rs",
            "python/isureal/contest.py",
            "php/src/Handler/TestHandler.php",
            "perl/lib/Isureal/Controller/Account.pm",
            "sql/001-schema.sql",
        ]:
            with self.subTest(name=name):
                self.assertFalse(sanitize_archive.is_test(name, is_dir=False))
        for name in [".", "go", "rust/src"]:
            with self.subTest(name=name):
                self.assertFalse(sanitize_archive.is_test(name, is_dir=True))


class SanitizeArchiveTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.path = pathlib.Path(self.directory.name) / "webapp.tar.gz"

    def tearDown(self):
        self.directory.cleanup()

    def test_rewrites_archive(self):
        image = b"\xff\xd8\xff\xe0binary"
        write_archive(
            self.path,
            {
                "go": None,
                "go/user_api.go": "package main\n\n// [作問] 説明\nfunc f() {}\n".encode(),
                "go/user_api_test.go": b"package main\n",
                "go/run.sh": b"#!/bin/sh\n",
                "go/image.jpg": image,
                "rust/tests": None,
                "rust/tests/a_test.rs": b"",
                "sql/001-schema.sql": "-- [作問] 説明\nCREATE TABLE t ();\n".encode(),
            },
        )
        self.assertEqual(sanitize_archive.sanitize_archive(self.path), (2, 2))
        self.assertEqual(
            read_archive(self.path),
            {
                "go": (None, 0o755),
                "go/user_api.go": (b"package main\n\nfunc f() {}\n", 0o644),
                "go/run.sh": (b"#!/bin/sh\n", 0o755),
                "go/image.jpg": (image, 0o644),
                "sql/001-schema.sql": (b"CREATE TABLE t ();\n", 0o644),
            },
        )

    def test_fails_on_leftover_without_touching_archive(self):
        write_archive(self.path, {"go/a.go": "/* [作問] 説明 */\n".encode()})
        original = self.path.read_bytes()
        with self.assertRaisesRegex(ValueError, "go/a.go:1"):
            sanitize_archive.sanitize_archive(self.path)
        self.assertEqual(self.path.read_bytes(), original)
        self.assertEqual(list(self.path.parent.iterdir()), [self.path])

    def test_fails_on_marker_in_binary(self):
        write_archive(self.path, {"go/a.bin": b"\xff" + "作問".encode()})
        with self.assertRaisesRegex(ValueError, "go/a.bin:0"):
            sanitize_archive.sanitize_archive(self.path)


if __name__ == "__main__":
    unittest.main()
