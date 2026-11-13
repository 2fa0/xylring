## 0.8.4 - 2026-11-13

### 更新内容

- 源码开源，改为 Apache 2.0 许可：https://github.com/2fa0/xylring。
- 安装包和更新文件改在源码仓库的 Release 发布，后续版本从 `2fa0/xylring` 获取。
- 修好一批只在 Windows 上失败的测试，Windows 版现在有 CI 覆盖。

### Changes

- The source is public, under the Apache License 2.0: https://github.com/2fa0/xylring.
- Installers and update files are published from the source repository from now on.
- Fix the tests that failed only on Windows; the Windows build is now covered by CI.

### 升级说明 / Upgrading

- 0.8.3 的客户端仍在旧仓库检查更新，看不到这一版，需要手动下载安装一次；装好之后自动更新恢复正常。
- The updater in 0.8.3 still points at the old release repository, so install this version by hand once. Automatic updates work again afterwards.

## 0.8.3 - 2026-11-13

### 更新内容

- 新增自动更新：启动后不久检查一次，之后每天一次；发现新版本时弹出更新窗口，可以安装并重启，也可以稍后提醒或跳过这一版。
- 新增「检查更新…」菜单项，随时手动检查，窗口里显示这一版的发布说明。
- 设置 → 通用 新增「更新」：可以关掉自动检查，查看当前版本和上次检查时间。
- 从磁盘镜像里直接运行、或安装目录不可写时，设置页会说明不能自动更新，而不是给出按下去没反应的开关。
- 安装包改在 `xylring-release` 仓库发布。

### Changes

- Check for updates shortly after launch and once a day, then install the new version from the update window and restart.
- Add "Check for Updates…" to the menu, and an Updates section in Settings showing the current version and turning automatic checks off.
- Explain in Settings when the app cannot update itself, such as from the disk image or a folder it cannot write.
- Publish installers from the `xylring-release` repository.

### 升级说明 / Upgrading

- 0.8.2 及更早的版本没有更新器，需要手动安装这一版一次；从 0.8.3 起的版本之间可以自动更新。
- Versions up to 0.8.2 have no updater: install this release by hand once. Updates work from 0.8.3 on.

## 0.8.2 - 2026-11-11

### 更新内容

- 删除 `.git` 后明确显示“仓库不可用”，清除旧图谱与差异缓存，暂停仓库操作并保留提交说明草稿。
- 恢复 `.git` 或重新初始化后自动重新加载仓库，清除旧分支筛选；`git init` 不会恢复已删除的提交历史。
- 阻止已打开仓库的 Git 命令回退到父仓库，保留首次打开目录和终端子目录命令的仓库查找能力。
- 修复从右键菜单进入文件历史时崩溃、返回后丢失对比列表的问题。
- 将获取、拉取和推送整合到工具栏的同步菜单，并显示分支领先和落后数量。
- 兼容不同 Git 版本对失效关联工作区的错误提示，及时清除旧仓库状态。
- 修复 Windows 上仓库会话恢复、仓库替换检测及文件差异比较的问题。

### Changes

- Show an unavailable state when Git metadata disappears, discard stale history and diffs, and pause repository actions while preserving the commit message draft.
- Reload restored or reinitialized repositories and reset stale branch filters. Running `git init` does not recover deleted history.
- Prevent commands on opened repositories from falling back to a parent repository, while preserving initial directory discovery and CLI commands from subdirectories.
- Fix a crash when opening file history from a row context menu and the comparison lost after returning.
- Combine fetch, pull, and push in the toolbar sync menu, with ahead and behind counts.
- Recognize invalid linked worktrees across Git versions and clear stale repository state.
- Fix repository session restoration, replacement detection, and file comparisons on Windows.

### 安装包 / Packages

| Platform | Architecture | File |
| --- | --- | --- |
| macOS | Apple Silicon | `xylring-0.8.2-arm64.dmg` |
| macOS | Intel x86_64 | `xylring-0.8.2-amd64.dmg` |
| Windows 10 / 11 | x64 installer | `xylring-setup-0.8.2-amd64.exe` |
| Windows 10 / 11 | x64 portable | `xylring-0.8.2-windows-x64.zip` |

SHA-256 hashes are provided in `checksums.txt` alongside the packages.

Install Git before running Xylring. macOS builds are not notarized; Windows builds are not code signed.
