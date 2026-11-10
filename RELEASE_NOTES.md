# Xylring v0.8.1

Release date: 2026-11-10

## 更新内容

- 删除 `.git` 后明确显示“仓库不可用”，清除旧图谱与差异缓存，暂停仓库操作并保留提交说明草稿。
- 恢复 `.git` 或重新初始化后自动重新加载仓库，清除旧分支筛选；`git init` 不会恢复已删除的提交历史。
- 阻止已打开仓库的 Git 命令回退到父仓库，保留首次打开目录和终端子目录命令的仓库查找能力。

## Changes

- Show an unavailable state when Git metadata disappears, discard stale history and diffs, and pause repository actions while preserving the commit message draft.
- Reload restored or reinitialized repositories and reset stale branch filters. Running `git init` does not recover deleted history.
- Prevent commands on opened repositories from falling back to a parent repository, while preserving initial directory discovery and CLI commands from subdirectories.

## 安装包 / Packages

| Platform | Architecture | File |
| --- | --- | --- |
| macOS | Apple Silicon | `xylring-0.8.1-arm64.dmg` |
| macOS | Intel x86_64 | `xylring-0.8.1-amd64.dmg` |
| Windows 10 / 11 | x64 installer | `xylring-setup-0.8.1-amd64.exe` |
| Windows 10 / 11 | x64 portable | `Xylring 0.8.1 windows-x64.zip` |

SHA-256 hashes are provided in `checksums.txt` alongside the packages.

Install Git before running Xylring. macOS builds are not notarized; Windows builds are not code signed.
