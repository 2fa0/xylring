## 0.8.5 - 2026-11-16

### 更新内容

- 通用设置新增「默认编辑器」：内置 VS Code、Cursor、Zed、Windsurf、Trae、Antigravity、Sublime Text、GoLand、IntelliJ IDEA 预设，也可以填自己的命令；对比文件、文件标签页、侧栏和首页的右键菜单都按这个设置打开文件或仓库目录。
- 年轮会自动识别 `AGENTS.md`、`CLAUDE.md` 和 Cursor 规则文件是否已写入、是否已过期，规则预览的写入与移除更直接；侧栏的状态控件改用状态点加简短文案，完整说明放进悬停提示。
- 设置里取值固定的 Git 配置项（拉取方式、快进策略等）改成单个下拉选择；没覆盖时显示「继承全局设置」，选过之后显示可读的选项名，而不是原始配置值。
- 历史范围筛选、全局搜索和比较按钮并入顶部标题栏，宽窗口下一行放下；新增顶栏分支按钮，可以切换本地分支、新建分支和复制分支名。
- 全局搜索的分支、标签、Agent 与模型建议改为固定顺序，空查询或刷新时不再跳动；建议弹窗限制了高度，超出可滚动。
- 侧栏长分支名不再把改动和同步状态（`*`、`↑n ↓n`）挤出视野，分支名自己截断，状态标记保持完整。
- 界面图标改为直接引用 lobe-mygo 的 Lucide 图标，仓库里不再藏 37 个 SVG 文件；mygo 升级到 v0.9.1。

### Changes

- Add a default editor in General settings with presets for VS Code, Cursor, Zed, Windsurf, Trae, Antigravity, Sublime Text, GoLand, and IntelliJ IDEA, plus a custom command; file comparisons, file tabs, and the row menus in the sidebar and start page open files and repository folders with it.
- Detect whether `AGENTS.md`, `CLAUDE.md`, and Cursor rule files are written and up to date, and make previewing, writing, and removing the rules more direct. The sidebar control now shows a status dot with a short label and keeps the details in a tooltip.
- Fixed-value Git settings such as pull mode and fast-forward policy become a single dropdown; uncovered values read “Inherit global setting” and covered ones show the option name instead of the raw config value.
- Move the history range filter, global search, and compare button into the title bar so they fit on one line in wide windows, and add a branch button that switches local branches, creates one, or copies its name.
- Keep branch, tag, agent, and model suggestions in a fixed order so they no longer jump on an empty query or refresh, and cap the suggestion popup height with scrolling.
- Stop long branch names in the sidebar from crowding out the change and sync markers (`*`, `↑n ↓n`); the name truncates and the markers stay whole.
- Reference Lucide icons from lobe-mygo instead of 37 SVGs checked into the repository, and upgrade mygo to v0.9.1.

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
