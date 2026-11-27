## 0.11.2 - 2026-11-27

### 更新内容

- 重构看板为并行 Agent 上下文墙：采用自适应多列瀑布流与状态聚焦胶囊，默认折叠已完成任务，支持任务置顶、按状态着色、卡片内勾选 Markdown 清单、最新提交跳图谱及一键复制 Agent 上下文。
- 看板新增快速新建任务、排序偏好持久化、外部更新提示与可见区虚拟化渲染，并修复并发勾选、归档范围、搜索与窄窗口工具栏适配等审计缺陷。
- 提交历史顶栏新增一键回顶按钮，回顶后保留列表焦点与当前选中项。
- 抽离 `internal/board`、`internal/i18n` 与 `internal/provenance` 子包并拆分大型 UI 文件，优化代码结构。

### Changes

- Redesign the board as a parallel Agent context wall with adaptive masonry columns, status focus pills, collapsed completed tasks, pinning, semantic status colors, inline Markdown checklist toggling, latest commit graph jump, and one-click Agent context copy.
- Add quick task creation, persisted board sort preferences, external update indicators, and visible-range masonry virtualization, while fixing concurrent checklist writes, archive scope, search state, and narrow toolbar layout.
- Add a scroll-to-top button to the commit history top bar while preserving list focus and selection.
- Extract `internal/board`, `internal/i18n`, and `internal/provenance` packages and split large UI files for cleaner structure.

## 0.11.1 - 2026-11-26

### 更新内容

- 重构设置界面：新增侧栏导航、项目设置页与内联提交引用支持。
- 修复仓库列表颜色筛选、排序及嵌套工作树归属，颜色标签与排序仅作用于顶层仓库分组。
- 增强工作树移除与失效记录清理的安全校验。
- 修复命令行启动时覆盖既有会话的问题，并支持配置目录增量迁移。
- 修复 Windows 外部编辑器启动及非工作区右键菜单文案。
- 完善发布产物校验与原子替换流程，更新 README 品牌名称与动态版本下载链接。

### Changes

- Refactor Settings with sidebar navigation, a project settings page, and inline commit reference support.
- Fix repository list color filtering, sorting, and nested worktree grouping so color tags and sorting apply only to top-level repository groups.
- Strengthen safety checks for worktree removal and stale record cleanup.
- Preserve existing sessions when launching from the CLI and support incremental config directory migration.
- Fix external editor launching on Windows and context menu labels outside workspaces.
- Improve release artifact verification and atomic replacement, and update README branding and dynamic download links.

## 0.11.0

### 更新内容

- 仓库可按颜色打标签、筛选和排序。
- 侧栏显示关联工作树，支持清理失效记录，以及在检查当前状态后移除工作树。
- 提交图谱增加独立引用列，保留折叠引用的操作入口，并改进并行分支的连线。
- 错误提示补充失败步骤和相关仓库，设置页新增项目主页入口。

### Changes

- Add repository color tags with filtering and sorting.
- Show linked worktrees in the sidebar and support stale record cleanup and worktree removal with safety checks.
- Add a dedicated refs column with actions for collapsed refs and improve parallel branch connections in the commit graph.
- Add failing steps and repository details to error messages and link to the project page from Settings.

## 0.10.0 - 2026-11-23

### 更新内容

- 新增 Windows WSL 仓库支持：可通过 `wsl.exe` 直接打开与运行 WSL 发行版中的 Git 仓库，支持 WSL 路径与 Linux 路径双向映射、UTF-16LE 输出解码以及通过文件管理器、终端和编辑器（VS Code / Cursor）打开 WSL 仓库。
- 优化 WSL 仓库扫描：默认定位 WSL 用户主目录，通过 WSL 内 `find -xdev` 快速发现仓库并跳过常见依赖/缓存目录与系统挂载点，同时对 `wsl.exe` / `find` 异常退出做准确分类与回退。
- 优化文件夹导入体验：打开非仓库目录时提供「初始化 Git 仓库」确认弹窗；打开包含多个 Git 仓库的目录时弹出列表供勾选导入（支持全选），取消后保持当前界面不变。
- 修复看板界面不显示全局告警横幅以及看板详情抽屉在 Windows 标题栏下顶部被遮挡的问题；当前未打开的仓库加载失败时，告警横幅也会在当前窗口正常提示。
- Git 失败日志与崩溃面包屑新增脱敏后的 stderr 摘要；新增 `build-dev.command` 用于快速构建开发测试包。
- 优化看板内存占用与写入效率：看板文件改为逐任务流式编码写入，加载时不再整份复制文件，离开看板时释放筛选后的列缓存。

### Changes

- Added Windows WSL repository support: open and run Git repositories inside WSL distributions via `wsl.exe`, with bidirectional UNC/Linux path mapping, UTF-16LE output decoding, and WSL integration for file manager, terminal, and editors (VS Code / Cursor).
- Improved WSL repository scanning: resolve the WSL user's home directory as the default root, discover repositories with `find -xdev` while pruning dependency/cache directories and root mount points, and classify `wsl.exe` / `find` failures cleanly.
- Improved folder import workflow: prompt to initialize a Git repository when opening a non-repository directory, and present a selectable repository list (with Select All) when opening a folder containing multiple repositories.
- Fixed the global alert banner not appearing on the board view and the board detail drawer overlapping custom Windows title bars; failed loads for repositories not yet in a tab now surface their banner properly.
- Added redacted stderr summaries to Git failure logs and crash breadcrumbs, and added `build-dev.command` for quick local dev builds.
- Reduced board memory footprint and write overhead: board persistence now streams tasks during encoding, loads without copying the whole file, and releases filtered column caches when leaving the board.

## 0.9.2 - 2026-11-19

### 更新内容

- 正在提交、推送、拉取、暂存或跑批量操作时，关窗口和 ⌘Q 会先问一句再退：可以留下等它跑完，也可以仍要退出；确认框里的操作名跟着实际进度变，操作跑完就改说「操作已完成。」
- 写操作记在应用级状态里而不是仓库标签上，关掉仓库不会停下已经发起的 Git，退出保护也不会跟着消失；只认真正改仓库或远端状态的调用，fetch、读历史、看 diff 这类能干净取消的不拦。
- 未提交改动只有一侧时不再显示分组标题，全部暂存 / 全部取消暂存移到文件列表行尾；是否分组只由仓库状态决定，筛选不会让标题和按钮跳位置，按钮提示写明作用于整个仓库。
- 取消暂存改用一条路径级 reset：还没有第一次提交的空仓库和有 HEAD 的仓库走同一条命令，后台不再读界面缓存的 HEAD，终端在别处切了分支也不会挑错命令。
- 分组标题不再抢键盘焦点，滚动时标题自己铺底色；行级笔记按「路径 + 侧」判断换行，同一文件两侧切换不再误清草稿；非重命名的行清掉旧路径，取消暂存不会去还原一个不存在的路径。
- 修掉一处数据竞争：读取仓库时后台线程复用了上一个回调正在读的变量。

### Changes

- Quitting with the window close button or ⌘Q now asks first when a commit, push, pull, staging or batch operation is running: stay and let it finish, or quit anyway. The dialog follows the actual progress and switches to "the operation has finished" once it completes.
- Running Git writes are tracked on the app rather than on the repository tab, so closing a repository does not drop the guard, and those writes keep going as before. Only calls that change the repository or a remote hold the window; fetch, history and diff reads, which cancel cleanly, do not.
- A worktree with changes on only one side no longer shows a group header, and Stage all / Unstage all move to the end of the file list row. Grouping now depends on the repository state alone, so filtering no longer moves the header or the buttons, and the button tooltip states that it covers the whole repository.
- Unstaging uses one path-level reset: a repository without its first commit and one with HEAD take the same command, the background no longer reads a cached HEAD from the UI, and switching branches in a terminal cannot pick the wrong command.
- Group headers no longer take keyboard focus and paint their own background while scrolling; line notes compare row identity (path and side) so switching between the two sides of one file keeps the draft; rows that are not renames drop their old path so unstaging cannot restore a path that no longer exists.
- Fixed a data race where reading a repository reused a variable the previous callback was still reading.

## 0.9.1 - 2026-11-18

### 更新内容

- 看板任务的编辑、拖拽、归档与删除改为按提交顺序在后台写入，操作不再等磁盘，界面也不再跟着卡一下。
- 看板列改为虚拟滚动，任务很多时滚动依然顺滑；关闭窗口前会先保存抽屉里还没提交的标题和描述。
- 提交列表首屏只统计可见条目的改动行数，仓库刚打开时更快出图；部分克隆的仓库跳过统计，不为了显示数字去联网。
- 仓库状态轮询跟着变化走：检测到改动后 2 秒复查，一直稳定时逐步退到 16 秒，后台更安静。
- 工作区文件指纹改为分块读取，大文件不再整份读进内存；文件标签页的暂存快照读取时校验长度，损坏的快照直接放弃，而不是显示错位内容。
- 图片预览先校验分辨率，超过约 840 万像素的图片直接提示超限，不再试着解码出几百 MB 的位图。
- 侧栏里其他仓库的跟踪摘要（领先 / 落后、上游分支）在窗口失焦后不再丢失。
- 升级到 mygo 0.10.0，并把 Metal 帧缓冲上限设为 2，图形内存占用更低。
- 中文名「木年轮」写进 README，README 改为中英双版并补上双语展示海报。

### Changes

- Board edits, drags, archive, and delete now write in the background in submission order, so actions no longer wait on disk or make the window stutter.
- Board columns use virtual scrolling and stay smooth with long task lists, and unsaved title and description drafts are saved before the window closes.
- The first page of history only counts changed lines for the visible rows, so a repository opens with a graph sooner; partial clones skip the count instead of fetching over the network just for a number.
- Repository polling follows activity: a detected change is rechecked after 2 seconds, and a stable repository backs off to 16 seconds.
- Worktree fingerprints read files in chunks, so large files are no longer loaded whole, and file-tab snapshots validate their length before use instead of showing garbled content.
- Image previews check resolution first and report anything above roughly 8.4 megapixels instead of trying to decode a bitmap hundreds of megabytes large.
- Tracking summaries for other repositories in the sidebar (ahead / behind, upstream branch) no longer disappear when the window loses focus.
- Upgrade to mygo 0.10.0 and cap the Metal drawable count at 2 for lower graphics memory use.
- Add the Chinese name 木年轮 to the README, ship English and Chinese READMEs, and include bilingual showcase posters.

## 0.9.0 - 2026-11-17

### 更新内容

- 新增跨项目 AI 协作看板：待办 / 进行中 / 待审阅 / 已完成四列主视图，卡片可跨列拖拽、圆形勾选完成，并在一行里显示所属项目、关联提交数、备注数与 Agent 来源。
- 看板顶栏并入项目筛选、搜索、已归档开关、Agent 规则与新建任务，看板不再单独占一行工具栏；支持 `Esc` 关闭与 `⌘F` 聚焦搜索。
- 任务支持优先级与截止日期（日历选择），详情以右侧浮层抽屉展示：文档式标题与描述、分段控件切换状态、`⌘↩` 直接提交备注。
- 任务可关联多仓库的多个提交，既能在看板里选仓库与近期提交，也能在提交图谱右键把提交挂到任务上，并支持跳转到其他仓库的提交。
- 备注按时间线排列，可拖拽排序、编辑和删除；抽屉里的草稿按任务暂存，切换任务、关闭抽屉或外部同步都不会丢掉正在输入的内容。
- 看板数据目录可以自定义，并提供自动备份快照、从快照恢复、JSON 导入导出（合并或替换、按需重映射仓库路径、支持试运行）。
- 删除任务、清空标题等破坏性操作改为先确认，说明关联提交与备注会一并删除。
- 新增 `xylring board` 命令行：`list`、`show`、`add`、`update`、`link`、`unlink`、`note`、`delete`、`export`、`import`、`backups`、`restore`、`snippet`，多端同时读写走同一套文件锁。
- 通用设置新增「看板」分组，可直接打开数据目录、切换目录、导出导入与恢复快照。
- 源码目录 `cmd/gitclient` 改名为 `cmd/xylring-main`，构建脚本、文档与测试路径同步更新。

### Changes

- Add a cross-project AI collaboration board with To do, In progress, Review, and Done columns. Cards drag between columns, complete from a round check, and show project, linked commits, notes, and agent source in one line.
- Fold the board's project filter, search, archived toggle, agent rules, and new-task action into the top toolbar, and support `Esc` to close and `⌘F` to focus search.
- Tasks take a priority and a due date picked from a calendar. Details open in a right-side floating drawer with document-style title and description, a segmented status control, and `⌘↩` to post a note.
- Link a task to several commits across repositories, either from the board's repository and recent-commit pickers or from the commit graph's context menu, and jump to a commit in another repository.
- Notes are a timeline that can be reordered by dragging, edited, and deleted. Drafts are kept per task, so switching tasks, closing the drawer, or an external sync no longer drops what you were typing.
- The board data directory is configurable, with automatic backup snapshots, restore from a snapshot, and JSON export/import (merge or replace, optional repository path remapping, dry run).
- Deleting a task and clearing a title now confirm first and spell out that linked commits and notes go with it.
- Add a `xylring board` command line with `list`, `show`, `add`, `update`, `link`, `unlink`, `note`, `delete`, `export`, `import`, `backups`, `restore`, and `snippet`, sharing one file lock across processes.
- Add a Board section to General settings for opening the data directory, switching it, exporting, importing, and restoring snapshots.
- Rename the source directory `cmd/gitclient` to `cmd/xylring-main`, updating build scripts, docs, and test paths.

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
