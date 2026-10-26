#!/usr/bin/env python3
"""配布するソースの tar.gz から、テストと [作問] コメントを取り除く。

prepare.yml が作ったアーカイブをその場で書き換える。取り除いたあとも「作問」が残るファイルがあれば、
アーカイブを変えずに失敗する。
"""

import fnmatch
import io
import os
import re
import subprocess
import sys
import tarfile
import tempfile
from pathlib import PurePosixPath

MARKER = "作問"

# 各言語の実装の慣習（Go、Ruby、Rust、Python、PHP、Perl、Node.js）。ディレクトリは階層を問わず中身ごと除く
TEST_DIRS = {"test", "tests", "testdata", "t", "spec", "__tests__"}
TEST_FILES = (
    "*_test.go",
    "*_test.rb",
    "*_spec.rb",
    "test_*.py",
    "*_test.py",
    "conftest.py",
    "*Test.php",
    "*.t",
    "*.test.ts",
    "*.test.js",
    "*.spec.ts",
    "*.spec.js",
)

# 行コメントの [作問] だけを扱う。// は Go・Rust・PHP・JS、# は Ruby・Python・Perl・PHP、-- は SQL
COMMENT = re.compile(r"(.*?)[ \t]*(?://|#|--)[ \t]*\[作問\].*")


def is_test(name, is_dir):
    parts = PurePosixPath(name).parts
    if not parts:
        return False
    if any(part in TEST_DIRS for part in parts[:-1]):
        return True
    if is_dir:
        return parts[-1] in TEST_DIRS
    return any(fnmatch.fnmatchcase(parts[-1], pattern) for pattern in TEST_FILES)


def strip_comments(text):
    """コメントだけの行は消し、コードの後ろのコメントは切り落とす。"""
    lines = []
    removed = False
    for line in text.split("\n"):
        body = line.rstrip("\r")
        match = COMMENT.fullmatch(body)
        if match:
            code = match.group(1)
            if code.strip():
                lines.append(code + line[len(body) :])
            removed = not code.strip()
            continue
        # 消したコメントの前後が空行なら 1 行にまとめ、消した跡を残さない
        if removed and not body.strip() and (not lines or not lines[-1].strip()):
            removed = False
            continue
        removed = False
        lines.append(line)
    return "\n".join(lines)


def sanitize_file(name, data):
    """(書き換えた中身, 「作問」が残る行番号) を返す。UTF-8 でなければ中身はそのまま。"""
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return data, [0] if MARKER.encode() in data else []
    if MARKER not in text:
        return data, []
    sanitized = strip_comments(text).encode("utf-8")
    # 構造体のフィールドの間の行を消すと揃えが崩れて、消した跡が gofmt で分かってしまう
    if name.endswith(".go") and sanitized != data:
        result = subprocess.run(["gofmt"], input=sanitized, capture_output=True)
        if result.returncode != 0:
            raise ValueError(f"{name}: [作問] コメントを消したあとの gofmt に失敗した\n{result.stderr.decode()}")
        sanitized = result.stdout
    text = sanitized.decode("utf-8")
    leftovers = [number for number, line in enumerate(text.split("\n"), 1) if MARKER in line]
    return sanitized, leftovers


def sanitize_archive(path):
    """テストを除いた数と、[作問] コメントを消したファイルの数を返す。"""
    removed_tests = 0
    stripped = 0
    leftovers = []
    fd, temporary = tempfile.mkstemp(dir=os.path.dirname(os.path.abspath(path)), suffix=".tar.gz")
    try:
        with os.fdopen(fd, "wb") as raw, tarfile.open(path, "r:*") as source, tarfile.open(
            fileobj=raw, mode="w:gz"
        ) as output:
            for member in source:
                if is_test(member.name, member.isdir()):
                    removed_tests += not member.isdir()
                    continue
                if MARKER in member.name:
                    leftovers.append(f"{member.name}: パスに「{MARKER}」を含む")
                if not member.isfile():
                    output.addfile(member)
                    continue
                data = source.extractfile(member).read()
                sanitized, lines = sanitize_file(member.name, data)
                leftovers.extend(f"{member.name}:{line}" for line in lines)
                stripped += sanitized != data
                member.size = len(sanitized)
                output.addfile(member, io.BytesIO(sanitized))
        if leftovers:
            raise ValueError(
                f"{path}: 行コメント以外の「{MARKER}」が残っている（0 行目はバイナリ）\n  " + "\n  ".join(leftovers)
            )
        os.replace(temporary, path)
    except BaseException:
        os.unlink(temporary)
        raise
    return removed_tests, stripped


def main(paths):
    if not paths:
        print("usage: sanitize_archive.py ARCHIVE...", file=sys.stderr)
        return 2
    for path in paths:
        try:
            removed_tests, stripped = sanitize_archive(path)
        except ValueError as error:
            print(error, file=sys.stderr)
            return 1
        print(f"{path}: テスト {removed_tests} ファイルを除き、{stripped} ファイルから [作問] コメントを消した")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
