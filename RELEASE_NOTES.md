# Xylring v0.8.2

Release date: 2026-11-11

## 更新内容

- 删除 `.git` 后明确显示“仓库不可用”，清除旧图谱与差异缓存，暂停仓库操作并保留提交说明草稿。
- 恢复 `.git` 或重新初始化后自动重新加载仓库，清除旧分支筛选；`git init` 不会恢复已删除的提交历史。
- 阻止已打开仓库的 Git 命令回退到父仓库，保留首次打开目录和终端子目录命令的仓库查找能力。
- 修复从右键菜单进入文件历史时崩溃、返回后丢失对比列表的问题。
- 将获取、拉取和推送整合到工具栏的同步菜单，并显示分支领先和落后数量。
- 兼容不同 Git 版本对失效关联工作区的错误提示，及时清除旧仓库状态。
- 修复 Windows 上仓库会话恢复、仓库替换检测及文件差异比较的问题。

## Changes

- Show an unavailable state when Git metadata disappears, discard stale history and diffs, and pause repository actions while preserving the commit message draft.
- Reload restored or reinitialized repositories and reset stale branch filters. Running `git init` does not recover deleted history.
- Prevent commands on opened repositories from falling back to a parent repository, while preserving initial directory discovery and CLI commands from subdirectories.
- Fix a crash when opening file history from a row context menu and the comparison lost after returning.
- Combine fetch, pull, and push in the toolbar sync menu, with ahead and behind counts.
- Recognize invalid linked worktrees across Git versions and clear stale repository state.
- Fix repository session restoration, replacement detection, and file comparisons on Windows.

## 安装包 / Packages

| Platform | Architecture | File |
| --- | --- | --- |
| macOS | Apple Silicon | `xylring-0.8.2-arm64.dmg` |
| macOS | Intel x86_64 | `xylring-0.8.2-amd64.dmg` |
| Windows 10 / 11 | x64 installer | `xylring-setup-0.8.2-amd64.exe` |
| Windows 10 / 11 | x64 portable | `xylring-0.8.2-windows-x64.zip` |

SHA-256 hashes are provided in `checksums.txt` alongside the packages.

Install Git before running Xylring. macOS builds are not notarized; Windows builds are not code signed.
