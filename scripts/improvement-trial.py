#!/usr/bin/env python3
"""Export native content screenshots and record view-build smoothness proxies.

This is trial evidence, not acceptance. Memory measurement stays on the
other script and must keep screenshots off.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent
BUILD_FLAGS = [
    "-trimpath", "-tags", "p0bench,mygo_noinspector", "-ldflags",
    "-s -w -X github.com/egoist/mygo.production=1",
]


def git(repo, *args, check=True):
    return subprocess.run(["git", "-C", str(repo), *args], check=check,
                          stdout=subprocess.PIPE, text=True).stdout.strip()


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def make_repo(repo):
    if repo.exists():
        shutil.rmtree(repo)
    repo.mkdir()
    git(repo, "init", "-q", "-b", "main")
    for key, value in {
        "user.name": "Trial",
        "user.email": "trial@example.invalid",
        "commit.gpgsign": "false",
    }.items():
        git(repo, "config", key, value)
    files = repo / "files"
    files.mkdir()
    for i in range(32):
        lines = [f"package files\n"]
        lines += [f"func value{i:02d}_{n:03d}() int {{ return {n} }}\n" for n in range(200)]
        (files / f"f{i:02d}.go").write_text("".join(lines))
    history = ["package history\n"]
    history += [f"func line{n:03d}() string {{ return \"before {n:03d}\" }}\n" for n in range(80)]
    (repo / "src").mkdir()
    (repo / "src" / "history.go").write_text("".join(history))
    git(repo, "add", ".")
    git(repo, "commit", "-q", "-m", "initial files")
    for i in range(38):
        note = repo / "notes" / f"{i:02d}.txt"
        note.parent.mkdir(exist_ok=True)
        note.write_text(f"note {i}\n")
        git(repo, "add", ".")
        git(repo, "commit", "-q", "-m", f"note {i}")
    for i in range(32):
        path = files / f"f{i:02d}.go"
        path.write_text(path.read_text().replace("return 1 }", "return 2 }", 1))
    history[40] = "func line039() string { return \"after 039\" }\n"
    (repo / "src" / "history.go").write_text("".join(history))
    git(repo, "add", ".")
    git(repo, "commit", "-q", "-m", "看清这次改动，主题保持单行")
    other = repo.parent / (repo.name + "-other")
    if other.exists():
        shutil.rmtree(other)
    git(repo, "clone", "-q", str(repo), str(other))
    return other


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path,
                        default=ROOT / "docs" / "validation" / "improvement" / "native")
    args = parser.parse_args()
    if sys.platform != "darwin":
        parser.error("原生截图和顺滑测量需要在 macOS 上跑。")
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="gitclient-trial-") as directory:
        temp = Path(directory)
        repo = temp / "repo"
        other = make_repo(repo)
        binary = temp / "gitclient-trial"
        subprocess.run(["go", "build", *BUILD_FLAGS, "-o", str(binary), "."],
                       cwd=ROOT / "cmd" / "gitclient", check=True)
        env = dict(os.environ)
        capture = subprocess.run([str(binary), "--ui-capture", str(repo), str(out)], env=env)
        smooth_path = out / "smooth.json"
        smooth = subprocess.run(
            [str(binary), "--smooth-probe", str(repo), str(other), str(smooth_path)], env=env)
        summary = {
            "captureExit": capture.returncode,
            "smoothExit": smooth.returncode,
            "repositoryCommit": git(repo, "rev-parse", "HEAD"),
            "sourceRevision": git(ROOT, "rev-parse", "HEAD"),
            "workingTreeDirty": bool(git(ROOT, "status", "--porcelain")),
        }
        manifest = out / "manifest.json"
        if manifest.exists():
            summary["capture"] = json.loads(manifest.read_text())
        if smooth_path.exists():
            summary["smooth"] = json.loads(smooth_path.read_text())
        (out / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps({
            "captureExit": summary["captureExit"],
            "smoothExit": summary["smoothExit"],
            "shots": [shot.get("Name") for shot in summary.get("capture", {}).get("Shots", [])],
            "selectionP95": summary.get("smooth", {}).get("Selection", {}).get("P95MS"),
            "tabP95": summary.get("smooth", {}).get("TabSwitch", {}).get("P95MS"),
            "diffFirstP95": summary.get("smooth", {}).get("DiffFirst", {}).get("P95MS"),
            "diffRepeatP95": summary.get("smooth", {}).get("DiffRepeat", {}).get("P95MS"),
            "scrollP95": summary.get("smooth", {}).get("Scroll", {}).get("IntervalP95MS"),
            "idleFrames": summary.get("smooth", {}).get("IdleFrames"),
        }, ensure_ascii=False, indent=2))
        if capture.returncode or smooth.returncode:
            raise SystemExit("试用证据没有跑完，不能把它写成通过")


if __name__ == "__main__":
    main()
