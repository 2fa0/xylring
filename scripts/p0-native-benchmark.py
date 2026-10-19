#!/usr/bin/env python3
"""Measure the native macOS app with a disposable 100k-commit repository."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import hashlib
import platform

ROOT = Path(__file__).resolve().parent.parent
BUILD_FLAGS = [
    "-trimpath", "-tags", "p0bench,mygo_noinspector", "-ldflags",
    "-s -w -X github.com/egoist/mygo.production=1",
]


def git(repo, *args):
    return subprocess.run(["git", "-C", str(repo), *args], check=True,
                          stdout=subprocess.PIPE, text=True).stdout.strip()


def create_repository(repo, temp):
    repo.mkdir()
    git(repo, "init", "-q", "-b", "main")
    for key, value in {
        "user.name": "P0 benchmark",
        "user.email": "bench@example.invalid",
        "commit.gpgsign": "false",
        "core.hooksPath": str(temp / "no-hooks"),
    }.items():
        git(repo, "config", key, value)
    with subprocess.Popen(["git", "-C", str(repo), "fast-import", "--quiet"],
                          stdin=subprocess.PIPE) as proc:
        for i in range(100000):
            body = f"commit {i}\n".encode()
            proc.stdin.write(
                f"commit refs/heads/main\n"
                f"committer Benchmark <bench@example.invalid> {1700000000+i} +0000\n"
                f"data {len(body)}\n".encode() + body)
            if i in (0, 99999):
                word = "updated" if i else "initial"
                text = "".join(f"line {n}: {word}\n" for n in range(10000)).encode()
                proc.stdin.write(f"M 100644 inline large.txt\ndata {len(text)}\n".encode() + text)
            proc.stdin.write(b"\n")
        proc.stdin.close()
        if proc.wait():
            raise RuntimeError("git fast-import 失败")
    git(repo, "reset", "--hard", "-q")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--soak-minutes", type=int, default=0)
    parser.add_argument("--output", type=Path, default=Path("/tmp/gitclient-p0-native.json"))
    args = parser.parse_args()
    if sys.platform != "darwin":
        parser.error("此脚本使用 macOS 的 vmmap；请在 Mac 上运行。")
    if args.soak_minutes < 0:
        parser.error("--soak-minutes 不能为负数")
    output = args.output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    revision = git(ROOT, "rev-parse", "HEAD")
    dirty = bool(git(ROOT, "status", "--porcelain"))
    source_hash = hashlib.sha256()
    sources = list((ROOT / "prototype").glob("*.go")) + [ROOT / "prototype" / "go.mod"]
    sources += list((ROOT / "third_party" / "mygo-editor").glob("*.go"))
    for path in sorted(sources):
        source_hash.update(str(path.relative_to(ROOT)).encode())
        source_hash.update(path.read_bytes())
    with tempfile.TemporaryDirectory(prefix="gitclient-p0-") as directory:
        temp = Path(directory)
        repo = temp / "repo"
        create_repository(repo, temp)
        binary = temp / "gitclient-bench"
        subprocess.run(["go", "build", *BUILD_FLAGS, "-o", str(binary), "."],
                       cwd=ROOT / "prototype", check=True)
        env = dict(os.environ, P0_SOAK_MINUTES=str(args.soak_minutes))
        app = subprocess.Popen([str(binary), "--p0-bench", str(repo), str(output)], env=env)
        awake = subprocess.Popen(["/usr/bin/caffeinate", "-d", "-i", "-u", "-w", str(app.pid)])
        try:
            if app.wait():
                raise RuntimeError("原生应用运行失败")
        finally:
            if app.poll() is None:
                app.terminate()
                app.wait()
            awake.terminate()
            awake.wait()
        report = json.loads(output.read_text())
        report.update(SourceRevision=revision, WorkingTreeDirty=dirty, BuildFlags=BUILD_FLAGS,
                      SourceSHA256=source_hash.hexdigest(), System=platform.platform(),
                      GitVersion=subprocess.check_output(["git", "--version"], text=True).strip())
        output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(output.read_text())
        if not report.get("Completed"):
            raise RuntimeError("原生测试未完成；保留中断数据，不计入验收")
        if report.get("Error"):
            raise RuntimeError("原生测试未通过：" + report["Error"])


if __name__ == "__main__":
    main()
