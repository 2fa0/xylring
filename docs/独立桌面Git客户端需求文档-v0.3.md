# 独立桌面 Git 客户端需求文档（草案 v0.3）

> 目标：用 **MyGo** 框架做一个独立的桌面 Git 客户端，覆盖 VS Code 扩展 **Git Graph** 和 **GitHD (Git History Diff)** 的主要功能，不再依赖 VS Code。
> v0.2 更新（2026-10-17）：已确认的决定——界面采用 MyGo 的 Go 原生 GPU 绘制 UI，**低内存占用列为首要目标**；暂存与提交界面纳入范围（P1）；多仓库采用“单窗口多标签，每个标签一个仓库”。
> v0.3 更新（2026-10-17）：新增 **M19 提交来源 / AI 标注**（3.19 节），用于区分哪个 AI 代理、哪个模型写了哪个提交。这个想法来自你的另一个助手，已获你同意加入。
> 本文只讲需求和技术调研，不含代码。所有功能点都来自两个扩展的 README、CHANGELOG、package.json 和部分源码（调研时间：2026-10-17），链接见文末“参考资料”。

---

## 1. 背景与目标

### 1.1 背景
- 日常常用两个 VS Code 扩展：
  - **Git Graph**（mhutchie.git-graph，v1.30.0）：提交图谱 + 在图上右键做各种 Git 操作。仓库最后一次推送是 2023-07，可以认为已停止维护。
  - **GitHD / Git History Diff**（huizhou.githd，v2.5.7）：分支、文件、文件夹、行的历史，blame，stash，分支或提交之间的 diff，提交统计图表。GitHub 仓库实际在 `huizhougit/githd`（任务里给的 `huizhou/githd` 不存在），2025-12 仍有提交。
- 现在已经很少打开 VS Code，所以想要一个独立客户端，把这两个扩展的常用功能合到一起。
- 你同时使用多个 AI 编码代理，事后**分不清哪个提交是哪个代理、哪个模型写的**。新客户端要能显示和筛选提交来源（M19）。
- **要解决的核心痛点是 VS Code 的内存占用**：为了看图谱、查历史而开一个完整的 VS Code，代价太大。所以新客户端必须明显更省内存，而不只是“功能一样”。

### 1.2 目标
1. **【首要目标】低内存占用**：常驻内存要明显低于为同样用途打开的 VS Code。内存占用是设计和技术选型的第一约束；功能和内存冲突时，优先保证内存（例如懒加载、限制缓存）。具体目标值见第 7 节（待原型验证）。
2. 一个窗口里看提交图谱，并直接执行分支、标签、提交、stash、远程等操作（对应 Git Graph）。
3. 方便地查看分支、文件、文件夹、单行的历史，以及 blame 和任意两个 ref 之间的 diff（对应 GitHD）。
4. 能完成日常的暂存和提交（两个扩展都没有，原来依赖 VS Code 的源代码管理视图），P1 实现。
5. 启动快，大仓库（几万到几十万个提交）也能流畅滚动。
6. 单窗口多标签管理多个仓库。
6a. 能看出每个提交来自哪个 AI 代理/模型还是人工，并按来源筛选和统计（M19，P1/P2）。
7. 支持 macOS / Windows / Linux。
8. 界面使用 MyGo 的 Go 原生 UI（GPU 绘制、不用 webview）；webview 前端只作为原生 UI 不够用时的后备或后续扩展。

### 1.3 非目标（第一阶段不做）
- 不做完整的 IDE 或代码编辑器（只需要查看文件和 diff，最多做轻量编辑）。
- MVP 不含暂存和提交界面：已确认要做，但放在 P1（见 3.18 节 M18）。
- 不做 PR 审阅、Issue 管理等托管平台的深度集成（只保留“打开链接”这一级）。

### 1.4 许可注意
- Git Graph 的 LICENSE **不是标准开源许可**：明确写了 “Permission is NOT GRANTED to publish, distribute, sublicense, and/or sell derivative works of the Software”。**可以参考它的功能设计，但不能复制或移植它的代码和图标资源**。
- GitHD 是 MIT 许可，可以参考代码，但需要保留版权声明。
- MyGo 是 MIT 许可。

---

## 2. 两个扩展的功能对比

图例：✅ 有；— 没有；◐ 部分有或形式不同

| 功能领域 | 具体功能 | Git Graph | GitHD | 说明 |
|---|---|---|---|---|
| **提交列表/图谱** | 图形化分支图谱（彩色线条、节点） | ✅ | — | GitHD 是文本化的历史列表，没有图 |
| | 提交列表（描述/日期/作者/哈希列） | ✅ 列可调宽、可隐藏 | ✅ 文本视图，显示日期、作者、哈希、ref、改动统计 | |
| | 分页 / 加载更多 | ✅ 首屏 300，之后每次 100，可自动加载 | ✅ 每页 300（`commitsCount`），后续页继续加载 | |
| | 显示未提交更改 | ✅ | — | |
| | 显示 stash 在图谱中 | ✅ | — | GitHD 有单独的 stash 列表 |
| | 显示标签、远程分支、remote HEAD | ✅ 可分别开关 | ◐ 显示 ref | |
| | 提交排序（date / author-date / topo） | ✅ | ◐ 固定 `--date-order` | |
| | 只跟随第一父提交（--first-parent） | ✅ | — | |
| | 包含仅 reflog 引用的提交 | ✅ | — | |
| | 弱化显示合并提交 / 非 HEAD 祖先提交 | ✅ | — | |
| | 快速模式（不计算改动统计，加载更快） | — | ✅ Express Mode | |
| **筛选与搜索** | 分支筛选（全部 / 多选 / 自定义 glob） | ✅ | ◐ 选一个分支看历史，或看全部历史 | |
| | 按作者筛选历史 | — | ✅ | GitHD 用 `git shortlog -se` 列出作者 |
| | 查找提交（消息/日期/作者/哈希/分支/标签名） | ✅ Find 组件 | — | |
| | 输入任意 ref / SHA 查看 | ◐ 提交正文里的哈希可以点击 | ✅ Input Ref | |
| **提交详情** | 点击提交查看详情与文件列表 | ✅ | ✅ | 两边都有 |
| | 文件列表：树 / 平铺两种视图，紧凑文件夹 | ✅ | ✅ 树/列表切换，展开/折叠全部 | |
| | 每个文件的改动统计 | ◐ | ✅ Show/Hide File Stats | |
| | 查看文件 diff | ✅ | ✅ | |
| | 查看该版本的文件 | ✅ | ◐ | |
| | 与工作区文件比较 | ✅ | ✅（未提交文件 vs 指定提交） | |
| | 打开当前文件 | ✅ | ✅ | |
| | 复制文件绝对/相对路径 | ✅ | — | |
| | 把文件重置为该版本 | ✅ | — | |
| | 上一个 / 下一个提交导航 | ✅ 方向键，以及 Ctrl+↑/↓ 按父子关系导航 | ✅ Previous/Next Commit | |
| | 点击父提交跳转 | ✅ | — | |
| | 视图前进 / 后退历史 | — | ✅ 历史视图和文件视图都有 | |
| | 提交正文中 URL、哈希、Issue 号变成链接 | ✅ | ◐ 识别 GitHub PR 链接 | |
| | Markdown（行内）渲染、emoji 短码 | ✅ | — | |
| | 签名状态（GPG / X.509） | ✅ | — | |
| | 作者头像（Gravatar） | ✅ | — | |
| | 作者日期和提交日期都显示 | ✅ | ◐ | |
| | 用外部 difftool 做目录 diff | ✅ | — | |
| **比较** | 任意两个提交比较（Ctrl 点击第二个） | ✅ | ✅（输入 `SHA1 .. SHA2`） | 两边都有 |
| | 当前分支与选定分支/ref 比较（全部文件） | ◐ 可以选两个提交 | ✅ View Branch Diff | |
| | 某个文件/文件夹在两个分支间的 diff | — | ✅ | |
| | 未提交更改与任意提交比较 | ✅ | ✅（单文件） | |
| **历史** | 分支历史（不切换分支） | ✅ 筛选分支 | ✅ | |
| | 文件历史（跟随重命名） | — | ✅ `--follow` | |
| | 文件夹历史 | — | ✅ | |
| | 行历史（`git log -L`）+ 行 diff | — | ✅ | |
| | Blame（行尾提示 + 悬浮详情 + 点哈希看提交） | — | ✅ 三档：disabled / blame / detail | |
| **统计** | 提交统计图表（与历史视图联动） | — | ✅ Stats chart，桶数可配（默认 91） | |
| **分支操作** | 新建 / 切换 / 重命名 / 删除（含强制删除） | ✅ | — | |
| | 合并（no-ff / squash / no-commit） | ✅ | — | |
| | 变基（含交互式变基，在终端里打开） | ✅ | — | |
| | 推送（多个远程、set-upstream、force-with-lease） | ✅ | — | |
| | 拉取（no-ff / squash） | ✅ | — | |
| | Fetch 到本地分支（含强制） | ✅ | — | |
| | 检出远程分支（本地已存在时“检出并拉取”） | ✅ | — | |
| | 打包分支/标签为 zip 或 tar | ✅ | — | |
| | 创建 PR（GitHub / GitLab / Bitbucket / 自定义） | ✅ | — | |
| | 复制分支名 | ✅ | — | |
| **提交操作** | 检出（分离 HEAD）、Cherry-pick（-x / no-commit）、Revert、Drop、Merge、Rebase、Reset（soft/mixed/hard） | ✅ | — | |
| | 在该提交上打标签、建分支 | ✅ | — | |
| | 复制哈希 / 主题 | ✅ | ✅ 复制哈希 | |
| **标签** | 添加（附注/轻量、可签名、可推送）、删除、推送、查看附注标签详情 | ✅ | — | |
| **Stash** | 查看 stash 列表及文件 diff | ✅ 在图谱中 | ✅ 单独视图 | 两边都有 |
| | 新建（含未跟踪文件）、apply、pop（可恢复 index）、drop、从 stash 建分支 | ✅ | — | |
| **未提交更改** | stash / reset（mixed/hard）/ clean | ✅ | — | |
| **远程** | 查看、添加、编辑、删除、fetch、prune 远程 | ✅ 仓库设置面板 | — | |
| | 顶栏 Fetch（可 prune、prune tags） | ✅ | — | |
| **仓库** | 多仓库切换、自动发现子仓库（深度可配）、手动添加/移除 | ✅ | ✅ 多根工作区、Set Git Repository | |
| | 仓库自定义名称 | ✅ | — | |
| | 仓库设置导出为可提交文件 | ✅ | — | |
| | 配置 user.name / email（本地/全局） | ✅ | — | |
| | Issue 链接规则（正则 + URL 模板） | ✅ | — | |
| | 在仓库目录打开终端 | ✅ | — | |
| **代码审查** | 标记文件已审/未审，跨会话保存，90 天不活动自动关闭 | ✅ | — | |
| **通用** | 快捷键可配置 | ✅ | — | |
| | 对话框默认选项可配置 | ✅ | — | |
| | .mailmap 支持 | ✅ | — | |
| | 文件编码配置 | ✅ | — | |
| | 自定义 git 路径 | ✅（读 `git.path`） | ✅ 优先使用设置里的 git 路径 | |
| | 缓存（.git 变化时更新） | ◐ | ✅ `cacheEnabled` | |
| | 颜色/主题可配置 | ✅ 图谱颜色、圆角/折角风格 | ✅ 历史视图各元素颜色 | |
| | 无障碍（A/M/D/R/U 文字标记） | ✅ | — | |

**小结：**
- **两边重叠**：提交历史列表、提交详情和文件 diff、两个提交比较、未提交更改与提交比较、stash 查看、多仓库、复制哈希、自定义 git 路径。
- **只有 Git Graph**：图谱渲染、所有写操作（分支/标签/提交/stash/远程/未提交更改）、Find、分支筛选、代码审查、PR 创建、Issue 链接、签名、头像、仓库设置。
- **只有 GitHD**：文件/文件夹/行历史、blame、按作者筛选、分支间整体或单文件/文件夹 diff、Input Ref、统计图表、快速模式、视图前进/后退。

---

## 3. 新客户端功能清单（按模块和优先级）

优先级：**P0** = MVP，必须有；**P1** = 第二阶段；**P2** = 以后再说。
来源：GG = Git Graph，HD = GitHD，两者 = 两个扩展都有，新 = 两个扩展都没有、因为脱离 VS Code 而必须新增。

### 3.1 仓库管理（M1）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M1-1 | 打开仓库 | 选择本地目录，通过 `rev-parse --show-toplevel` 识别仓库根目录 | P0 | 新（原来依赖 VS Code 工作区） |
| M1-2 | 多仓库标签页 | **单窗口多标签，每个标签一个仓库**：新建标签打开仓库、关闭标签、拖动排序、切换标签；重启后恢复上次打开的标签。后台标签要释放大块数据（见 6.3 节） | P0 | 新（已确认；对应 GG/HD 的多仓库切换） |
| M1-2a | 最近仓库列表 | 在“新建标签”时快速选择最近打开的仓库，排序方式可选（名称/完整路径/最近使用） | P0 | GG / HD |
| M1-3 | 自动发现子仓库 | 扫描指定深度以内的子目录 | P2 | GG |
| M1-4 | 仓库自定义显示名 | 区分同名仓库 | P2 | GG |
| M1-5 | 文件系统监听自动刷新 | 监听 `.git` 和工作区变化，刷新图谱和缓存 | P1 | GG / HD（HD 缓存会随 .git 变化更新） |
| M1-6 | 在仓库目录打开系统终端 / 在文件管理器中显示 | | P1 | GG（终端）/ 新 |
| M1-7 | 指定 git 可执行文件路径 | | P0 | 两者 |

### 3.2 提交图谱与列表（M2）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M2-1 | 图谱渲染 | 彩色分支线 + 提交节点，圆角/折角两种风格，颜色可配 | P0 | GG |
| M2-2 | 列表列 | 图谱 / 描述（含 ref 标签）/ 日期 / 作者 / 哈希；可调列宽、显示/隐藏列 | P0 | GG（HD 为文本形式） |
| M2-3 | 分页加载 | 首屏 N 条（默认 300），滚到底自动加载更多（默认 100） | P0 | 两者 |
| M2-4 | 虚拟化滚动 | 只渲染可见行，保证大仓库流畅 | P0 | 新（性能要求） |
| M2-5 | ref 标签 | 本地分支、远程分支（可与本地合并显示）、标签、remote HEAD、当前 HEAD 标记 | P0 | GG |
| M2-6 | 未提交更改行 | 图谱顶部显示“未提交更改”节点，可开关是否包含未跟踪文件 | P0 | GG |
| M2-7 | stash 节点 | 在图谱中显示 stash，可开关 | P1 | GG |
| M2-8 | 节点悬浮提示 | 是否包含在 HEAD 中，以及哪些分支/标签/stash 包含该提交 | P1 | GG |
| M2-9 | 排序选项 | date / author-date / topo，可按仓库覆盖 | P1 | GG |
| M2-10 | 显示选项 | 只跟随第一父提交、包含 reflog 提交、显示仅被标签引用的提交、显示远程分支/标签 | P1 | GG |
| M2-11 | 弱化显示 | 合并提交、非 HEAD 祖先提交用淡色显示 | P2 | GG |
| M2-12 | 日期格式 | 日期时间 / 仅日期 / ISO / 相对时间；作者日期或提交日期 | P1 | GG |
| M2-13 | 快速定位 | 跳到 HEAD（并闪烁）、跳到上一个/下一个 stash | P1 | GG |
| M2-14 | 快速模式 | 不计算每个提交的改动统计，提高大仓库加载速度 | P1 | HD |
| M2-15 | 每个提交的改动统计 | 历史列表显示 shortstat（文件数、增删行数） | P1 | HD |
| M2-16 | 作者头像 | Gravatar 头像，带缓存和清除缓存 | P2 | GG |
| M2-17 | emoji 短码 / gitmoji 替换、行内 Markdown | | P2 | GG |
| M2-18 | .mailmap 支持 | | P2 | GG |

### 3.3 筛选与搜索（M3）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M3-1 | 分支筛选 | 全部分支 / 多选分支 / 当前分支；切换分支查看历史**不会**检出 | P0 | GG / HD |
| M3-2 | 查找提交 | 按消息、日期、作者、哈希、分支名、标签名查找，高亮并逐个跳转；可选“跳转时自动打开详情” | P0 | GG |
| M3-3 | 按作者筛选 | 从作者列表（shortlog）中选择 | P1 | HD |
| M3-3a | 按 AI 代理 / 模型筛选 | 在作者筛选的基础上增加“来源”维度，详见 M19-3 | P1 | 新（扩展 HD 的按作者筛选） |
| M3-4 | 输入 ref / SHA | 直接打开任意提交的详情 | P0 | HD（Input Ref） |
| M3-5 | 自定义 glob 分支组 | 例如 `heads/feature/*` | P2 | GG |
| M3-6 | 打开仓库时默认显示的分支 | 当前分支 / 指定分支列表 | P2 | GG |

### 3.4 提交详情（M4）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M4-1 | 详情面板 | 哈希、父提交（可点击跳转）、作者/提交者（姓名、邮箱、日期）、完整消息 | P0 | 两者 |
| M4-2 | 改动文件列表 | 树/平铺切换、紧凑文件夹、A/M/D/R/U 状态标记（可加文字标记，方便色弱用户）、重命名显示旧路径 | P0 | 两者 |
| M4-3 | 文件改动统计 | 每个文件的增删行数，可显示/隐藏 | P1 | HD |
| M4-4 | 文件右键菜单 | 查看 diff、查看该版本文件、与工作区文件比较、打开当前文件、复制绝对/相对路径、把文件重置为该版本 | P0（前 4 项）/ P1（其余） | GG（HD 有部分） |
| M4-5 | 键盘导航 | ↑/↓ 打开相邻提交；Ctrl+↑/↓ 按父子关系导航，加 Shift 时走另一分支；Esc 关闭 | P1 | GG |
| M4-6 | 上一个 / 下一个提交按钮 | | P1 | HD |
| M4-7 | 视图前进 / 后退 | 历史视图和详情视图都有导航历史 | P1 | HD |
| M4-8 | 链接识别 | 消息中的 URL、提交哈希、Issue 号（正则规则可配）、GitHub PR 号 | P1 | GG / HD |
| M4-9 | 签名状态显示 | GPG/X.509 签名状态及悬浮详情 | P2 | GG |
| M4-10 | 详情面板位置 | 内联展开在列表中，或停靠在底部/右侧 | P1 | GG |
| M4-11 | 用外部 difftool 打开目录 diff | 读取 `diff.tool` / `diff.guitool` | P2 | GG |

### 3.5 Diff 查看（M5）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M5-1 | 内置 diff 查看器 | 并排 / 统一两种视图，语法高亮，词级高亮，折叠未改动的行 | P0 | 两者（原来用 VS Code 的 diff） |
| M5-2 | 文件内容查看器 | 查看某个版本的文件，只读 | P0 | GG |
| M5-3 | 二进制和大文件处理 | 显示“二进制文件”或给出大小提示 | P1 | 新 |
| M5-4 | 文件编码 | 可配置读取历史版本时使用的编码 | P2 | GG |

### 3.6 比较（M6）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M6-1 | 任意两个提交比较 | 选中一个提交后 Ctrl/Cmd 点击另一个，显示改动文件列表和 diff | P0 | 两者 |
| M6-2 | 当前分支 vs 选定分支/ref | 整体 diff | P0 | HD |
| M6-3 | 两个分支/ref 比较 | 包括本地与远程分支；也可输入 `SHA1..SHA2` | P0 | HD |
| M6-4 | 文件/文件夹在两个 ref 间比较 | | P1 | HD |
| M6-5 | 未提交更改 vs 任意提交 | 整个工作区或单个文件 | P1 | 两者 |

### 3.7 历史与 Blame（M7）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M7-1 | 文件历史 | 跟随重命名（`--follow`），点击某条记录看该提交中该文件的 diff | P0 | HD |
| M7-2 | 文件夹历史 | | P1 | HD |
| M7-3 | 行历史 | 选中某一行（或行范围），用 `git log -L` 查看历史和每次的行 diff | P1 | HD |
| M7-4 | Blame 视图 | 文件查看器中显示每行的最近一次提交（作者、相对时间、摘要），悬浮显示详情，点击哈希打开提交详情；可切换 关闭 / 简要 / 详细 三档 | P1 | HD |
| M7-5 | 文件浏览入口 | 独立客户端没有 VS Code 资源管理器，需要一个工作区文件树（或文件搜索）作为文件/文件夹历史、blame、文件 diff 的入口 | P0（简易文件树）| 新 |
| M7-6 | 从提交详情的文件进入文件/文件夹历史 | | P1 | HD |

### 3.8 分支操作（M8）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M8-1 | 切换分支 | 包括检出远程分支（建立本地跟踪分支；本地已有同名分支时可以“检出并拉取”） | P0 | GG |
| M8-2 | 新建分支 | 从任意提交创建，可选创建后检出；名称冲突时提示是否覆盖 | P0 | GG |
| M8-3 | 删除分支 | 本地删除（未合并时提示强制删除）、删除远程分支 | P0 | GG |
| M8-4 | 重命名分支 | | P1 | GG |
| M8-5 | 合并到当前分支 | 选项：no-ff（默认开）、squash、no-commit | P0 | GG |
| M8-6 | 变基当前分支到该分支/提交 | 选项：ignore-date；交互式变基（在外部终端打开） | P1 | GG |
| M8-7 | 推送 | 选择一个或多个远程，set-upstream，force / force-with-lease | P0 | GG |
| M8-8 | 拉取 | 选项：no-ff、squash | P0 | GG |
| M8-9 | Fetch 远程分支到本地分支 | 可强制 | P1 | GG |
| M8-10 | 打包为 zip / tar | | P2 | GG |
| M8-11 | 创建 PR | 按提供方模板打开浏览器（GitHub / GitLab / Bitbucket / 自定义） | P2 | GG |
| M8-12 | 复制分支名；在筛选中选中/取消选中该分支 | | P1 | GG |

### 3.9 提交操作（M9）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M9-1 | 复制哈希 / 复制主题 | | P0 | 两者 |
| M9-2 | 检出提交（分离 HEAD） | | P0 | GG |
| M9-3 | Cherry-pick | 选项：-x（记录来源）、no-commit；合并提交需选父提交 | P0 | GG |
| M9-4 | Revert | 合并提交需选父提交 | P0 | GG |
| M9-5 | Reset 当前分支到此提交 | soft / mixed / hard，hard 需二次确认 | P0 | GG |
| M9-6 | 合并此提交 / 变基到此提交 | 同 M8-5 / M8-6 | P1 | GG |
| M9-7 | Drop 提交 | 仅在拓扑允许时可用（实质是变基） | P2 | GG |
| M9-8 | 提交和标签签名 | 用 GPG/X.509 签名本客户端创建的提交和标签 | P2 | GG |

### 3.10 标签（M10）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M10-1 | 添加标签 | 附注/轻量，附注标签可填消息，可选立即推送；提示最近的标签名 | P0 | GG |
| M10-2 | 删除标签 | 本地，可选同时删除远程 | P0 | GG |
| M10-3 | 推送标签 | 可推送到多个远程 | P0 | GG |
| M10-4 | 查看附注标签详情 | 打标签人、邮箱、日期、消息、签名 | P1 | GG |
| M10-5 | 打包标签为 zip/tar、复制标签名 | | P2 | GG |

### 3.11 Stash（M11）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M11-1 | Stash 列表 | 单独的列表视图，加上图谱中的节点 | P0 | 两者 |
| M11-2 | 查看 stash 的改动文件和 diff | 包括已暂存的文件 | P0 | 两者 |
| M11-3 | 新建 stash | 可填消息，可包含未跟踪文件 | P0 | GG |
| M11-4 | Apply / Pop | 可选恢复 index | P0 | GG |
| M11-5 | Drop | | P0 | GG |
| M11-6 | 从 stash 建分支 | | P1 | GG |
| M11-7 | 复制 stash 名或哈希 | | P2 | GG |

### 3.12 未提交更改（M12）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M12-1 | 查看未提交更改的文件列表和 diff | | P0 | GG |
| M12-2 | Stash 未提交更改 | 同 M11-3 | P0 | GG |
| M12-3 | Reset 未提交更改 | mixed / hard | P1 | GG |
| M12-4 | Clean 未跟踪文件 | 可选包含目录；危险操作，需确认 | P1 | GG |
| M12-5 | 暂存 / 取消暂存 / 提交 | 已确认要做，P1，详见 3.18 节 M18 | P1 | 新 |

### 3.13 远程（M13）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M13-1 | 顶栏 Fetch | fetch 所有远程，可选 prune、prune tags | P0 | GG |
| M13-2 | 远程管理 | 查看、添加、编辑（fetch/push URL）、删除、单独 fetch、prune | P1 | GG |
| M13-3 | 推送默认远程 | 读取 `remote.pushDefault`、`branch.<name>.pushRemote` 等配置 | P1 | GG |
| M13-4 | 认证 | 依赖系统 git 的 credential helper / SSH agent；需要时弹出输入框 | P0 | 新（原来由 VS Code 的 git 处理） |

### 3.14 仓库设置与集成（M14）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M14-1 | user.name / user.email | 本地 / 全局配置 | P1 | GG |
| M14-2 | Issue 链接规则 | 正则 + URL 模板，支持 `$1`、`$2` 等占位符 | P2 | GG |
| M14-3 | PR 提供方配置 | 内置 GitHub / GitLab / Bitbucket，可自定义 | P2 | GG |
| M14-4 | 仓库配置导出为文件 | 可提交到仓库，与他人共享 | P2 | GG |
| M14-5 | 仓库级显示设置覆盖 | 显示 stash/标签/远程分支、排序等 | P1 | GG |
| M14-6 | AI 来源标注设置 | 选择存储方式（trailer / git notes）、代理名称别名映射、一键安装 hook，详见 M19-5 / M19-6 | P1（显示设置）/ P2（hook 安装） | 新 |

### 3.15 代码审查（M15）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M15-1 | 开始审查 | 在单个提交或两个提交的比较上开始，未审文件加粗，看过 diff 后取消加粗 | P2 | GG |
| M15-2 | 手动标记已审 / 未审 | | P2 | GG |
| M15-3 | 持久化、恢复、结束审查 | 90 天不活动自动关闭 | P2 | GG |

### 3.16 统计（M16）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M16-1 | 提交统计图表 | 按时间分桶统计当前历史视图里的提交（数量/改动），桶数可配；快速模式下不显示 | P2 | HD |
| M16-2 | 按 AI 代理 / 模型统计 | 按来源分组统计提交数、增删行数，可与时间分桶叠加，详见 M19-4 | P2 | 新 |

### 3.17 通用与设置（M17）
| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M17-1 | 设置界面 | 集中管理第 3 节提到的各项配置；用户级 + 仓库级 | P0（基础项）| 两者 |
| M17-2 | 对话框默认选项 | 各操作对话框的默认勾选项可配置（参考 Git Graph 的 `dialog.*`） | P1 | GG |
| M17-3 | 快捷键 | Ctrl/Cmd+F 查找、+R 刷新、+H 回到 HEAD、+S / Shift+S 跳到 stash，均可重新绑定 | P1 | GG |
| M17-4 | 右键菜单项可见性配置 | | P2 | GG |
| M17-5 | 引用名输入中空格自动替换为 `-` 或 `_` | | P2 | GG |
| M17-6 | 深色/浅色主题，跟随系统 | | P1 | 新 |
| M17-7 | 操作日志 / 错误输出 | 显示执行的 git 命令和错误，方便排查 | P1 | HD（traceLevel） |
| M17-8 | 缓存 | 缓存 ref、提交等数据，`.git` 变化时失效 | P1 | HD |
| M17-9 | AI 来源全局设置 | 是否显示来源徽标；代理/模型名称的别名和颜色；识别 Co-authored-by 的匹配规则（用户可配置）；M18 提交时是否自动加 `AI-Agent: human`（默认开） | P1 | 新 |

### 3.18 暂存与提交（M18，P1）
两个扩展都没有这项功能（Git Graph 的“未提交更改”菜单里只有 stash / reset / clean / 打开 VS Code 源代码管理视图）。因为开发者经常需要手动提交，已确认纳入范围，**优先级 P1（不进 MVP）**。入口：点击图谱顶部的“未提交更改”节点，或使用标签页内的“提交”面板（快捷键可配）。

| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M18-1 | 工作区状态列表 | 分成“已暂存”和“未暂存”（含未跟踪、冲突）两组，树/平铺切换，显示 A/M/D/R/U 状态 | P1 | 新 |
| M18-2 | 暂存 / 取消暂存文件 | 单个、多选、全部；支持拖拽或双击 | P1 | 新 |
| M18-3 | 按块（hunk）暂存 / 取消暂存 | 在 diff 查看器中对单个 hunk 操作 | P1 | 新 |
| M18-4 | 按行暂存 / 取消暂存 | 选中若干行只暂存这几行（生成部分补丁） | P2 | 新 |
| M18-5 | 丢弃工作区修改 | 单个文件、hunk 或全部；危险操作，需确认；未跟踪文件可以移到回收站（mygo 提供 `Shell.TrashItem`） | P1 | 新（GG 的 reset/clean 是整体操作） |
| M18-6 | 提交信息编辑 | 主题 + 正文的多行输入；显示主题长度提示（例如 50/72 列参考线）；保存草稿，切换标签不丢 | P1 | 新 |
| M18-7 | 提交 | 提交已暂存内容；可选“全部提交”（等同 `-a`）；显示 hooks 的输出，失败时保留提交信息；默认自动添加 `AI-Agent: human` trailer（见 M19-2） | P1 | 新 |
| M18-8 | 修改上一次提交（amend） | 载入上次的提交信息；可以只改信息不改内容；如果上一次提交已推送，给出提醒 | P1 | 新 |
| M18-9 | 提交选项 | 签名（-S，沿用 M9-8 的配置）、Signed-off-by（-s）、跳过 hooks（--no-verify，需醒目提示）、修改作者 | P2 | 新 |
| M18-10 | 历史提交信息 | 从最近的提交信息中选择、复用 | P2 | 新 |
| M18-11 | 提交并推送 | 提交成功后直接推送到上游 | P2 | 新 |
| M18-12 | 冲突文件标记 | 合并或变基冲突时，列出冲突文件，提供“标记为已解决”（`git add`）和“继续/中止”（`--continue` / `--abort`） | P1 | 新 |

对应的 git 命令：

| 功能 | git 命令（示意） |
|---|---|
| 状态 | `git status --porcelain=v2 -z --branch [--untracked-files=all]` |
| 未暂存/已暂存 diff | `git diff [-- <path>]` / `git diff --cached [-- <path>]` |
| 暂存/取消暂存文件 | `git add -- <path>` / `git restore --staged -- <path>`（旧版 git：`git reset -q HEAD -- <path>`）；新仓库没有 HEAD 时用 `git rm --cached` |
| 按 hunk / 行暂存 | 生成补丁后 `git apply --cached [--recount] [--unidiff-zero] -`（通过 stdin 传入）；取消暂存用 `git apply --cached -R -` |
| 丢弃修改 | `git restore -- <path>`（旧版：`git checkout -- <path>`）；hunk 级：`git apply -R -`；未跟踪文件：`git clean -f -- <path>` 或移到回收站 |
| 提交 | `git commit -F <消息文件或 ->` [`-a`] [`-S`] [`-s`] [`--no-verify`] [`--author=...`] |
| Amend | `git commit --amend [-F -]`；只改信息：`git commit --amend --only -F -`；载入上次信息：`git log -1 --format=%B` |
| 冲突处理 | `git diff --name-only --diff-filter=U`，`git add -- <path>`，`git merge\|rebase\|cherry-pick\|revert --continue\|--abort` |

> 说明：按 hunk 暂存的补丁格式细节（例如是否需要 `--unidiff-zero`、`--recount`）在实现时按实际生成的补丁决定，上表只是示意。

### 3.19 提交来源 / AI 标注（M19）

> 这个功能的想法来自你的另一个助手，已获你同意加入本文档。

**背景**：你同时使用多个 AI 编码代理，事后分不清哪个提交是哪个代理、哪个模型写的。

**关键设计前提**：AI 代理（例如 Claude Code）是通过它们自己的命令行执行 `git commit` 的，**不经过本客户端**。所以客户端既不知道是哪个代理提交的，也没法替它们写入标注。分工是：

- **写入**靠 git hooks（以及代理自身的配置）在提交时完成；
- **客户端主要负责读取、显示、筛选和统计**；只有通过客户端自己的提交界面（M18）提交时，客户端才会直接写入标注。

#### 约定格式

**首选：commit trailer**（写在提交信息末尾，git 原生支持解析）

```
feat: 支持标签页

正文……

AI-Agent: <代理名，例如 claude-code>
AI-Model: <模型名>
AI-Session: <会话 ID，可选>
```

- 人工提交写 `AI-Agent: human`（不写 AI-Model）。
- 没有任何标注的历史提交显示为“未知”，不猜测。
- 键名大小写按 git trailer 规则处理，客户端解析时不区分大小写。代理名和模型名统一规范成小写，并通过别名映射合并不同写法（M17-9）。

**备选：git notes**（不想修改提交信息时使用）

- 把同样的 `AI-Agent:` / `AI-Model:` / `AI-Session:` 行写进 notes（建议使用单独的 ref，例如 `refs/notes/ai`，避免和其他 notes 混在一起）。
- 优点：不改动提交信息和哈希，也可以事后给旧提交补标注。
- 缺点：notes 默认**不会**随 push/fetch 同步，需要额外配置 `refs/notes/*` 的 refspec；默认也不会随 rebase / amend 复制到新提交（需要配置 `notes.rewriteRef`）；托管平台的网页上通常看不到。
- 客户端两种来源都读；同一个提交两边都有时，以 trailer 为准。

#### 功能清单

| ID | 功能 | 描述 | 优先级 | 来源 |
|---|---|---|---|---|
| M19-1 | 来源徽标 | 在图谱列表的描述列和提交详情中显示来源徽标（代理名，悬浮显示模型和会话 ID；人工和未知用不同样式）。数据来自：① `AI-*` trailer；② 可选的 git notes；③ 识别提交信息中 AI 代理添加的 `Co-authored-by` 行（**按用户可配置的规则匹配**，例如邮箱或名称中的关键字；本文不预设任何代理的具体格式，内置规则须在实现时按各代理官方文档核实后再加入） | P1 | 新 |
| M19-2 | M18 提交自动标注 | 通过客户端自己的提交界面（M18）提交时，自动添加 `AI-Agent: human`；提交信息里已有 `AI-Agent` 时不重复添加；可在设置中关闭 | P1 | 新 |
| M19-3 | 按代理 / 模型筛选 | 扩展 GitHD 的按作者筛选（M3-3）：可以按代理、模型、“人工”、“未知”筛选，可以与作者、分支筛选组合 | P1 | 新（扩展 HD） |
| M19-4 | 按代理 / 模型统计 | 放进现有统计模块（M16-2）：按来源统计提交数、增删行数，可按时间分桶查看趋势；快速模式下只统计提交数 | P2 | 新 |
| M19-5 | 一键安装 hook | 在仓库设置（M14-6）中一键生成 hook 并启用，见下方说明 | P2 | 新 |
| M19-6 | 标注检查（可选） | 安装 hook 时可以选择同时生成 `commit-msg` 检查：没有 `AI-Agent` trailer 的提交给出警告，或直接拒绝（可选） | P2 | 新 |
| M19-7 | 给已有提交补标注 | 在提交右键菜单中“添加来源标注”，写入 git notes（不改历史） | P2 | 新 |

#### 一键安装 hook（M19-5 / M19-6）
- 在仓库里生成 `.githooks/prepare-commit-msg`（可以提交到仓库，团队共享），逻辑：
  1. 如果环境变量 `AI_AGENT` / `AI_MODEL`（以及可选的 `AI_SESSION`）有值，就追加对应的 trailer；
  2. 否则，如果提交信息中有匹配用户规则的 `Co-authored-by` 行，就根据规则推断出代理名并追加 `AI-Agent`；
  3. 已经有 `AI-Agent` 时不重复添加（使用 `git interpret-trailers --if-exists doNothing`），这样 amend 时也不会叠加。
  - 环境变量需要你在各个代理的启动环境或配置里设置（例如在启动代理的脚本或 shell 别名里 `export AI_AGENT=...`）。客户端只负责生成 hook，不负责修改代理的配置。
- 可选生成 `.githooks/commit-msg`，用于 M19-6 的检查。
- 执行 `git config core.hooksPath .githooks` 启用。
- 安装前要检查并提示：
  - `core.hooksPath` 已经被设置过（例如其他 hook 管理工具设的），或 `.git/hooks/` 下已有 hook 时，需要提示用户。设置 `core.hooksPath` 后，git **不再执行** `.git/hooks/` 里原有的 hook。可以提供“把原有 hook 串联调用”的选项，或者让用户手动合并。
  - 生成的脚本在安装前先给用户预览，安装后在仓库设置里显示“已安装 / 版本 / 卸载”。
  - Windows 下 hook 由 Git for Windows 自带的 sh 执行，脚本要用 POSIX sh 写，并注意换行符（LF）。

#### 局限（需要在界面中说明）
- `git commit --no-verify` 会跳过 `pre-commit` 和 `commit-msg` 这两个 hook，所以 M19-6 的本地检查可以被绕过。`prepare-commit-msg` 不受 `--no-verify` 影响（见 githooks 文档），但也可以通过 `git -c core.hooksPath=... commit` 临时换掉 hook 目录，或者直接修改、删除 hook 来绕过。另外 hook 只在装了它的本地仓库里生效，别处的克隆不受约束。**真正强制要求标注，需要在 CI 或服务端（例如 pre-receive hook、托管平台的检查）实现，这不在本客户端的范围内。**
- trailer 是自愿约定：代理没设环境变量、提交信息里也没有 `Co-authored-by` 时，只能显示“未知”。
- squash 合并或改写提交信息的操作可能丢失或合并 trailer。

#### 对应的 git 命令

| 功能 | git 命令（示意） |
|---|---|
| 列表中读取来源（随分页一起取） | 在图谱的分页 `git log` 格式中加入 `%(trailers:key=AI-Agent,valueonly,separator=%x2C)`、`%(trailers:key=AI-Model,valueonly)`、`%(trailers:key=AI-Session,valueonly)`、`%(trailers:key=Co-authored-by,valueonly)`（`key=` / `valueonly` 选项需要 Git 2.22+） |
| 读取 notes | `git log --notes=ai --format=...%N...`，或 `git notes --ref=ai show <sha>` |
| 按来源筛选（全历史） | `git log -E -i --grep='^AI-Agent: <name>$'`；匹配 notes 时加 `--notes=ai`（启用 notes 后，`--grep` 也会匹配 notes 内容） |
| 写入 trailer（M18 / hook） | `git interpret-trailers --in-place --if-exists doNothing --trailer 'AI-Agent: human' <msgfile>`；M18 也可以直接用 `git commit --trailer 'AI-Agent: human'`（需要 Git 2.32+，旧版本退回 interpret-trailers） |
| 写入 / 补写 notes | `git notes --ref=ai add -m 'AI-Agent: ...' <sha>`（追加用 `append`） |
| 同步 notes | `git push <remote> refs/notes/ai`；`git fetch <remote> 'refs/notes/*:refs/notes/*'` |
| 启用 hook | `git config core.hooksPath .githooks`；安装前读取 `git config --get core.hooksPath` 检查是否已被设置 |
| 统计 | `git log --format=<哈希+来源 trailer> --numstat`，按来源聚合（流式） |

> 以上 trailer 格式选项、`--grep` 匹配 notes、`interpret-trailers --if-exists doNothing` 都在本机 git 2.47 上实测过；版本要求取自 git 2.22 / 2.32 的发行说明。

#### 低内存要求
- **只解析已加载页的 trailer**：来源字段放在分页 `git log` 的同一个 `--format` 里一起流式读取，不额外遍历全历史，也不额外调用 `git show`。
- 每条提交只存来源 ID（代理、模型各用一个小整数，指向驻留的字符串表），不存完整 trailer 文本；AI-Session 只在打开详情时读取。
- 全历史的来源筛选交给 `git log --grep` 在子进程里完成，客户端只接收匹配结果的分页。
- 统计（M19-4）流式聚合：边读边累加到“来源 × 时间桶”的计数表，不保留逐条明细。
- notes 是可选数据源，只有在仓库设置中启用后才读取。

### 3.20 MVP（P0）范围小结
1. 打开仓库、多标签（每个标签一个仓库）、配置 git 路径；满足第 7 节的内存目标（原型阶段验证）。
2. 提交图谱：虚拟滚动、分页加载、ref 标签、未提交更改节点。
3. 分支筛选、查找提交、输入 SHA 跳转。
4. 提交详情、文件列表和内置 diff 查看器（并排/统一）。
5. 两个提交比较、分支 vs 分支比较。
6. 文件历史，加上简易文件树入口。
7. 常用写操作：切换/新建/删除/合并分支，推送、拉取、Fetch；cherry-pick、revert、reset、检出提交；标签增删推；stash 增、查、apply、pop、drop。
8. 紧接 MVP 的 P1 重点：暂存与提交（M18）、提交来源徽标和按代理/模型筛选（M19-1～M19-3）、行历史和 Blame、远程管理、快捷键。
9. P2 里与 AI 来源相关的：按代理/模型统计（M19-4 / M16-2）、一键安装 hook 和标注检查（M19-5 / M19-6）、用 notes 补标注（M19-7）。

---

## 4. 关键交互与界面布局

### 4.1 主窗口布局（建议）
```
┌──────────────────────────────────────────────────────────────────────┐
│ 标签栏：[ repo-a ×] [ repo-b ×] [ repo-c ×] [+]                        │
├──────────────────────────────────────────────────────────────────────┤
│ 顶栏：[分支筛选 ▾] [☑远程分支] [Fetch] [刷新] [查找🔍] [提交] [⚙]      │
├───────────┬──────────────────────────────────────────────────────────┤
│ 侧栏      │ 提交列表（图谱 | 描述+ref标签 | 日期 | 作者 | 哈希）       │
│ ・分支    │   ○ 未提交更改 (3)                                        │
│ ・远程    │   ● [main][origin/main] fix: ...                          │
│ ・标签    │   │●  feat: ...                                           │
│ ・Stash   │   ●┘ Merge ...                                            │
│ ・文件树  │   ...（滚到底自动加载更多）                                │
│ ・作者    ├──────────────────────────────────────────────────────────┤
│           │ 详情面板：提交信息 | 改动文件（树/平铺） | Diff 查看器       │
└───────────┴──────────────────────────────────────────────────────────┘
│ 状态栏：当前分支 / HEAD、后台任务进度、快速模式开关                     │
└──────────────────────────────────────────────────────────────────────┘
```
- **单窗口多标签**：每个标签是一个独立仓库，各自有侧栏、图谱、详情和筛选状态；切换标签时保留轻量状态（滚动位置、选中提交、筛选条件、提交信息草稿），释放大块数据（见 6.3 节）。`+` 打开最近仓库列表或目录选择对话框。
- 顶栏的“提交”按钮（P1）打开暂存与提交面板（M18），也可以通过点击“未提交更改”节点进入。
- 侧栏把 GitHD 的“入口型”功能（文件树 → 文件/文件夹历史、blame；作者 → 按作者筛选；Stash 列表）和 Git Graph 的 ref 操作合在一起。
- 详情面板可以停靠在底部或右侧，也可以内联展开（对应 Git Graph 的 Inline / Docked to Bottom）。
- Diff 可以在详情面板里打开，也可以在新标签页或新窗口里打开。

### 4.2 关键交互
- **单击提交**：打开详情。**Ctrl/Cmd + 单击另一个提交**：进入比较模式。
- **右键**提交、分支、远程分支、标签、stash、未提交更改、详情面板中的文件：弹出对应菜单（菜单项见第 3 节，最终以 Git Graph 的 `contextMenuActionsVisibility` 结构为准）：
  - 分支：checkout, rename, delete, merge, rebase, push, viewIssue, createPullRequest, createArchive, select/unselect in dropdown, copyName
  - 远程分支：checkout, delete, fetch, merge, pull, viewIssue, createPullRequest, createArchive, select/unselect, copyName
  - 提交：addTag, createBranch, checkout, cherrypick, revert, drop, merge, rebase, reset, copyHash, copySubject
  - 标签：viewDetails, delete, push, createArchive, copyName
  - Stash：apply, createBranch, pop, drop, copyName, copyHash
  - 未提交更改：stash, reset, clean, 打开暂存/提交面板（替代原来的 openSourceControlView，P1，见 M18）
  - 详情面板中的文件：viewDiff, viewFileAtThisRevision, viewDiffWithWorkingFile, openFile, markAsReviewed/NotReviewed, resetFileToThisRevision, copyAbsolute/RelativeFilePath
- **所有写操作都先弹对话框**：显示将要执行的操作和选项（默认值可配置），Enter 确认，Esc 取消。危险操作（reset --hard、clean、force push、删除远程分支）需要醒目提示。
- **长任务**（fetch / push / pull / clone）在后台执行，显示进度，可以取消；失败时显示 git 的原始错误输出。
- **冲突处理**：合并、变基、cherry-pick 出现冲突时，提示用户并提供“中止”（`--abort`）和“在外部工具/终端中解决”两个入口。两个扩展都没有内置冲突解决，MVP 也不做。
- **标签页**：Ctrl/Cmd+T 新建标签（打开仓库）、Ctrl/Cmd+W 关闭标签、Ctrl/Cmd+1…9 或 Ctrl+Tab 切换（建议默认值，可配置）。
- **暂存与提交（P1）**：在状态列表中勾选或双击暂存文件；在 diff 中对 hunk 点“暂存/取消暂存/丢弃”；提交信息框用 Ctrl/Cmd+Enter 提交；勾选“修改上一次提交”即为 amend。
- **文件 → 历史/Blame**：在文件树或详情面板的文件上右键 →“文件历史 / 文件夹历史 / Blame / 与某个分支比较”；在 Blame 视图选中行 →“行历史”。

---

## 5. 各功能对应的 git 命令

> 下表是实现参考。GitHD 的命令取自它的源码 `src/gitService.ts`，Git Graph 的取自它的功能描述和对话框选项。所有命令都应加 `-c core.quotepath=false`，解析用的命令要加 `--no-color`，并显式关闭 `log.showSignature`（Git Graph 在 #452 中就是这样处理的，保证解析结果稳定）。

| 功能 | git 命令（示意） |
|---|---|
| 识别仓库根目录 | `git rev-parse --show-toplevel` |
| 当前分支 | `git rev-parse --abbrev-ref HEAD` / `git symbolic-ref -q HEAD` |
| 列出 ref | `git for-each-ref --format='%(refname) %(objectname) %(objectname:short) %(*objectname)'`（`show-ref -d` 也可以） |
| 图谱数据（分页） | `git log --format=<含 %H %P %an %ae %at %cn %ce %ct %s 的自定义分隔格式> --date-order\|--author-date-order\|--topo-order [--branches --remotes --tags \| <选定分支>] [--first-parent] [--reflog] --skip=N --max-count=M`。由客户端根据 %P（父提交）自己计算图谱布局 |
| stash 列表（含在图谱中） | `git stash list --format=...`（或 `git reflog --format=... refs/stash`） |
| 未提交更改 | `git status --porcelain=v2 -z [--untracked-files=all\|no]`，`git diff --name-status`，`git diff --cached --name-status` |
| 提交改动统计（GitHD 风格） | `git log ... --shortstat`；快速模式下不加 |
| 提交详情 | `git show --no-patch --format=... <sha>`，`git show --format=%b --no-patch <sha>` |
| 提交改动文件 | `git show --format= --name-status -M <sha>`（合并提交用 `-m --first-parent` 或 `diff-tree`），统计用 `--numstat` |
| 签名状态 | `git log -1 --format='%G? %GS %GK' <sha>` 或 `git verify-commit` |
| 文件内容（某版本） | `git show <sha>:<path>` |
| 文件 diff | `git diff <a> <b> -- <path>`，或自己取两个版本的内容交给 diff 查看器 |
| 两个提交/分支比较 | `git diff --name-status -M <a>..<b>`；GitHD 用 `git diff --name-status <left>..<right>` |
| 与工作区比较 | `git diff <sha> -- <path>` |
| 按作者筛选 | `git shortlog -se HEAD`（作者列表），`git log --author=<name>` |
| 分支历史 | `git log <branch> --date-order --simplify-merges ...` |
| 文件历史 | `git log --follow -- <path>` |
| 文件夹历史 | `git log -- <dir>` |
| 行历史 | `git log -L <start>,<end>:<path>` |
| Blame | `git blame --incremental -L <n>,<n> --root <path>`（GitHD 按行查询）；整文件用 `git blame --porcelain <path>` |
| 提交总数 | `git rev-list --count <branch> [--author=...]` |
| 切换分支 / 检出提交 | `git checkout <branch>`，`git checkout -b <local> --track <remote>/<branch>`，`git checkout <sha>` |
| 新建分支 | `git branch <name> <sha>`（或 `checkout -b`） |
| 删除分支 | `git branch -d\|-D <name>`；远程：`git push <remote> --delete <branch>` |
| 重命名分支 | `git branch -m <old> <new>` |
| 合并 | `git merge [--no-ff] [--squash] [--no-commit] <ref>` |
| 变基 | `git rebase [--ignore-date] <ref>`；交互式：在外部终端中执行 `git rebase -i <ref>` |
| 推送 | `git push [--set-upstream] [--force\|--force-with-lease] <remote> <branch>` |
| 拉取 | `git pull [--no-ff] [--squash] <remote> <branch>` |
| Fetch 到本地分支 | `git fetch [-f] <remote> <remoteBranch>:<localBranch>` |
| Fetch 全部 | `git fetch --all [--prune] [--prune-tags]` |
| Cherry-pick | `git cherry-pick [-x] [--no-commit] [-m <parent>] <sha>` |
| Revert | `git revert --no-edit [-m <parent>] <sha>` |
| Reset | `git reset --soft\|--mixed\|--hard <sha>` |
| Drop 提交 | `git rebase --onto <sha>^ <sha>`（需检查拓扑） |
| 文件重置为某版本 | `git checkout <sha> -- <path>` |
| 标签 | `git tag [-a -m <msg>] [-s] <name> <sha>`，`git tag -d <name>`，`git push <remote> <tag>`，`git push <remote> --delete <tag>`；详情：`git for-each-ref refs/tags/<name> --format=...` |
| Stash | `git stash push [--include-untracked] [-m <msg>]`，`git stash apply\|pop [--index] stash@{n}`，`git stash drop stash@{n}`，`git stash branch <name> stash@{n}`，`git stash show --name-status stash@{n}`（GitHD 实现：在 show/diff 命令前加 `stash`） |
| Reset / Clean 未提交更改 | `git reset --mixed\|--hard HEAD`，`git clean -f [-d]` |
| 远程管理 | `git remote -v`，`git remote add/remove/set-url [--push]`，`git fetch <remote>`，`git remote prune <remote>` |
| 用户信息 | `git config [--global] user.name/user.email` |
| 打包 | `git archive --format=zip\|tar -o <file> <ref>` |
| 目录 diff（外部工具） | `git difftool --dir-diff [-g] <a> <b>` |
| Mailmap | `git log --use-mailmap` / `%aN %aE` 格式 |
| 暂存与提交（M18） | 见 3.18 节的命令表 |
| 提交来源 / AI 标注（M19） | 读取：分页 `git log` 格式中加 `%(trailers:key=AI-Agent,valueonly)` 等；筛选：`git log -E -i --grep='^AI-Agent: <name>$' [--notes=ai]`；写入：`git interpret-trailers` / `git commit --trailer`；notes：`git notes --ref=ai add/show`；hook：`git config core.hooksPath .githooks`。详见 3.19 节 |

---

## 6. 技术说明

### 6.1 “mygo” 框架调研结论

**结论：使用 egoist/mygo（MyGo）**（已确认，见第 8 节问题 1）。它是一个用 Go 开发桌面应用的框架；GitHub 上搜 “mygo” 时，它是唯一相关的高星项目（约 2.2k star），其他同名项目都不相关。

- 仓库：https://github.com/egoist/mygo ；官网：https://mygo.egoist.dev/ ；文档：https://mygo.egoist.dev/docs
- 以下只列已核实的事实（来自 README、官网和 docs 目录）：
  - **非常新**：仓库创建于 2026-09-26，调研时最新版本是 v0.5.5（2026-10-16），更新很频繁。MIT 许可。**API 可能还不稳定**，这是项目风险。
  - 支持 macOS 12+、Windows 10/11（x64/arm64）、Linux（x64/arm64）。开发需要 Go 1.27+；纯 Go，不需要 cgo，可以在任意机器上交叉编译（macOS 的签名和 dmg 需要在 Mac 上做）。
  - 每个窗口可以选两种界面之一，同一个应用里可以混用：
    1. **Native UI**：用 Go 的 `ui` 包编写，由 MyGo 自己用 GPU 绘制（macOS 用 Metal，Windows 用 Direct3D 11，Linux 用 OpenGL），没有 HTML/JS，也没有 webview。视图是“应用状态的 Go 函数”，每帧调用。
    2. **Web 前端**：使用系统自带的 webview（WKWebView / WebKitGTK / WebView2）。前端通过 MyGo 根据 Go 代码生成的 TypeScript 客户端调用 Go（类型化 IPC），支持 channel 流式传值和事件。
  - 文档中与本项目相关的能力（只列文档里明确写了的）：
    - `ui.List` / `ui.Table`：虚拟化列表，只构建可见行，文档写“百万行和十行一样快”，行高可以不同。适合做提交列表。
    - 绘图：元素可以设置 `Draw` 回调，用 `*ui.Painter` 画线、路径（`StrokePath` / `FillPath`）、圆角矩形、文字等。可以用来画分支图谱的线和节点。
    - `ui/editor`：代码编辑器（语法高亮、折叠、查找替换等，只渲染可见行），以及 `editor.DiffView` diff 查看器（并排 `DiffSplit` / 统一 `DiffUnified`，词级高亮，折叠未改动的行）。可以直接满足 M5。
    - `ui/chart`：图表包（比例尺、坐标轴、标记）。可以用于 M16 统计图表。
    - 菜单：菜单栏、**上下文菜单**、托盘、快捷键（accelerators）。
    - 桌面 API：文件/目录选择对话框（`mygo.Dialog.Open`，可以选目录）、剪贴板（`mygo.Clipboard.WriteText` 等）、`mygo.Shell.OpenExternal`（打开浏览器）/ `OpenPath` / `ShowItemInFolder`、通知、**文件监听 `mygo.WatchFiles`**（可递归，基于 fsnotify 思路）、深色模式、全局快捷键、Secrets（文档有此章节）、自动更新和安装包。
    - docs/ui 目录下还有这些组件的文档页：tree、split、tabs、sidebar、toolbar、find-bar、search-field、history-buttons（前进/后退）、tooltip、popover、dialog、alert-dialog、select、combobox、context-menu、markdown、toast、progress 等。具体 API 以文档为准，**本文不展开，也不推测这些组件的具体接口**。
  - 生态中已经有基于 MyGo Native UI 的应用，例如 egoist/gorex、missuo/motrix-mygo，以及第三方组件库 oneo-me/onevra、smallnest/uix。可以作为参考。
- **已确定的界面方案**：
  - 全部使用 **Native UI**（Go 原生、GPU 绘制）：提交列表用虚拟化 List/Table，图谱用 Painter 绘制，diff 用 `editor.DiffView`，标签页用 tabs 组件（docs/ui 中有 tabs 文档页，具体接口以文档为准）。不启动 webview，这对内存目标最有利。
  - Web 前端**只作为后备或后续扩展**：只有当原型证明 Native UI 在某个具体功能上做不到时，才在那个区域或窗口引入 webview，并且要单独评估它带来的内存增量（系统 webview 进程的占用也要算进去）。注意：Git Graph 的代码不能拿来用（见 1.4）。
  - 框架很新（v0.5.x），接口变动的风险由技术验证原型提前暴露，见 6.4 节。

### 6.2 Git 集成方式：调用 git 命令行 vs go-git

| 维度 | 调用系统 git 命令行（`os/exec`） | go-git 库（github.com/go-git/go-git） |
|---|---|---|
| 功能完整度 | 完整，和用户在终端里的行为一致 | 根据官方 COMPATIBILITY.md：**stash ❌、rebase ❌、revert ❌、reflog ❌、archive ❌**；merge 只支持快进；pull 只支持能快进的情况；cherry-pick 部分支持。blame、tag、fetch、push、clean、reset 支持 |
| 与用户环境兼容 | 自动使用用户的 `.gitconfig`、credential helper、SSH 配置、hooks、GPG 签名、LFS、include 配置 | 认证、hooks、签名、LFS 等要自己处理，行为可能和命令行不一致 |
| 性能 | 每次调用都要起一个进程，但 `git log` 等批量命令很快；大仓库表现稳定 | 进程内调用、没有 fork 开销；但在大仓库上遍历历史、算 diff 通常比 C 版 git 慢 |
| 依赖 | 用户必须安装 git（两个扩展本身也依赖 git） | 不依赖外部 git，单一二进制 |
| 实现成本 | 需要解析文本输出（用自定义分隔符和 `-z` / porcelain 格式可以降低难度）；需要处理不同 git 版本的差异（GitHD 曾修过“git 2.44 breaking changes”） | 类型化 API，不用解析文本；但缺失的功能还得退回命令行 |
| 错误信息 | 直接把 git 的原始错误显示给用户，用户熟悉 | 错误信息和 git 不同 |

**建议：以调用 git 命令行为主**（两个扩展都是这么做的）。可以选择性地用 go-git 做一些只读、高频的操作（例如读 ref、读对象），但不强制。理由：本项目的大部分写操作（stash、rebase、revert、cherry-pick、非快进合并和拉取）go-git 都不支持或只部分支持，而且需要复用用户已有的认证和签名配置。

实现要点：
- 封装一个 git 执行层：统一设置环境变量（如 `LC_ALL=C`、`GIT_TERMINAL_PROMPT=0` 或接入自己的认证弹窗）、超时、取消、并发限制和日志记录。
- 解析层使用 `--format` 自定义分隔符、`-z`、`--porcelain`，避免受用户语言设置和配置影响。
- 图谱布局算法由客户端根据父提交关系自己计算（Git Graph 就是这样做的，但它的代码不能用），需要支持增量（分页）加载。
- 缓存加文件监听：监听 `.git/HEAD`、`.git/refs`、`.git/packed-refs`、`.git/index` 等的变化，再刷新视图。
- 流式解析：`git log` 等输出量大的命令，通过管道边读边解析（逐条按分隔符切分），不把整个输出读成一个大字符串再处理；读够一页就可以停止或取消子进程。

### 6.3 低内存设计要点（对应首要目标）
1. **虚拟化渲染**：提交列表、文件列表、状态列表、blame 和 diff 只构建和绘制可见区域（MyGo 的 `ui.List` / `ui.Table` / `ui/editor` 文档都写明只布局和绘制可见部分）。
2. **不把完整历史放进内存**：提交按页加载（默认首屏 300 条、每次加 100 条）；每条提交只保存列表显示需要的精简字段（哈希、父提交、作者、时间、主题、ref 索引）。完整消息、文件列表、统计在打开详情时再按需加载。
3. **滑动窗口**：滚动很远以后，可以丢掉离视口很远的页的详细数据，只保留图谱布局需要的最少信息（哈希和父提交关系）；再滚回来时重新加载。这是否必要由原型实测决定。
4. **diff 和文件内容懒加载**：只有打开 diff 时才执行 `git show` / `git diff`；关闭后释放；超大文件或二进制文件只显示提示，用户确认后再加载。
5. **缓存有上限**：所有缓存（提交详情、diff、blame、头像）使用 LRU，按条目数和估算字节数双重限制，上限可配置；切到后台的标签只保留轻量状态（滚动位置、选中项、筛选条件、提交信息草稿），释放图谱数据、diff 和 blame，切回来时再加载。
6. **调用 git 命令行 + 流式解析**：繁重的历史遍历和 diff 计算交给 git 子进程完成，主进程只保存解析后的精简结构，不在进程内加载对象库；子进程输出边读边解析，用完即结束（见 6.2）。
7. **字符串和对象复用**：作者名、邮箱、ref 名等重复字符串做驻留（interning）；哈希用定长字节数组存储，不用 40 字节的十六进制字符串。
8. **后台任务和文件监听受控**：每个仓库标签只监听必要的 `.git` 路径，不递归监听整个大工作区（是否需要监听工作区以刷新“未提交更改”，由原型评估开销）；同时运行的 git 子进程数量有上限。
9. **AI 来源字段**：随分页 `git log` 一起流式解析，只为已加载的页解析 trailer，每条提交只存代理/模型的小整数 ID（见 3.19 节“低内存要求”）。
10. **可观测**：开发版内置内存面板或日志（Go `runtime.ReadMemStats`、`net/http/pprof` 或 `runtime/pprof` 的堆快照），方便定位泄漏和回归。

### 6.4 技术验证原型计划
在开始 MVP 之前，先做一个最小原型，回答“MyGo Native UI 能否满足功能和内存目标”。

**范围：**
1. 打开一个大仓库（建议用公开的大仓库，例如 linux 内核，或至少 10 万提交的仓库），分页加载提交，显示虚拟化列表和图谱（Painter 绘制），滚动到底自动加载。
2. 点击提交显示文件列表，打开 `editor.DiffView` 查看 diff（包括一个几万行的大文件）。
3. 右键菜单、一个写操作（例如新建分支）、git 子进程流式解析。
4. 两到三个仓库标签页之间切换，验证后台标签释放数据的策略。

**内存测量方法：**
- 测量指标：进程常驻内存，macOS 用活动监视器的“内存”列或 `footprint` / `vmmap`，Linux 用 `/proc/<pid>/status` 的 VmRSS 或 PSS，Windows 用任务管理器或 Process Explorer 的 Working Set / Private Bytes。另外记录 Go 堆（`runtime.MemStats` 的 HeapInuse 等）和 GPU 相关占用（如果平台工具能看到）。
- 测量场景（每个场景记录峰值和稳定值）：
  1. 空窗口启动后；
  2. 打开 1 个大仓库、首屏加载完成；
  3. 连续滚动加载 1 万 / 5 万条提交后；
  4. 打开并关闭 10 个 diff（含一个大文件）后，内存是否回落；
  5. 打开 3 个仓库标签、来回切换后；
  6. 连续使用 30–60 分钟后，内存是否持续增长（判断泄漏）。
- 对照组：在同一台机器上，用 VS Code 打开同一仓库并打开 Git Graph 视图，按同样场景测量，作为“必须明显更低”的参照。
- 产出：一份测量表和结论，用来确认或修正第 7 节的目标值，并决定是否需要 6.3 节第 3 条的滑动窗口、是否需要在任何地方引入 webview。

**通过标准**：功能跑通，滚动流畅，第 7 节的内存目标基本达到（或有明确的优化路径）。不通过时，再评估局部引入 webview 或调整方案。

---

## 7. 非功能需求（建议）
- **内存（首要）**：以下是**拟定目标，需要在技术验证原型中实测确认或修正**，不是已验证的数据。以 macOS / Windows 上的进程常驻内存为准（Linux 用 RSS/PSS），测量方法见 6.4 节。

  | 场景 | 拟定目标（待验证） |
  |---|---|
  | 启动后空窗口（未打开仓库） | ≤ 60 MB |
  | 打开 1 个大仓库（≥10 万提交），首屏加载完成 | ≤ 120 MB |
  | 同一仓库滚动加载 5 万条提交后 | ≤ 200 MB |
  | 打开一个 1 万行文件的 diff 时的峰值增量 | ≤ +50 MB，关闭后回落到打开前 +10 MB 以内 |
  | 每多开 1 个仓库标签（后台标签） | ≤ +20 MB |
  | 连续使用 1 小时 | 无持续增长（同一场景前后相差 ≤ 10%） |
  | 对照 | 同一仓库、同一场景下，明显低于 VS Code + Git Graph（具体比例以原型实测为准） |

  如果实测发现某项目标不合理（偏高或偏低），在原型结论中调整，并更新本节。
- 性能：打开 10 万提交的仓库时，首屏不超过 1 秒（只加载前 300 条）；滚动保持 60fps；diff 查看器能打开几万行的文件。
- 稳定性：所有写操作执行前给出明确提示，执行失败时显示 git 的原始输出，不吞错误。
- 可移植：三大平台行为一致；路径、换行符、文件编码正确处理（包括中文路径和中文提交信息）。
- 国际化：界面至少支持简体中文和英文。

---

## 8. 开放问题（需要你确认；1、2、9 已解决）
1. ~~“mygo” 是不是 egoist/mygo？用 Native UI 还是 Web 前端？~~ **【已解决】** 使用 egoist/mygo 的 Go 原生 GPU 绘制 UI；webview 前端只在原生 UI 被证明不够用时作为后备或后续扩展。低内存占用列为首要目标（见 1.2、6.3、6.4、第 7 节）。v0.5.x 的接口变动风险通过技术验证原型提前暴露。
2. ~~要不要做暂存和提交界面？~~ **【已解决】** 要做，优先级 P1（不进 MVP），因为开发者经常需要手动提交。见 3.18 节 M18（文件/hunk 暂存、提交信息、amend 等）。
3. **要不要内置文件查看器和编辑器？** Blame、行历史、“打开文件”原来都依赖 VS Code 编辑器。新客户端是只做只读查看（加 blame 列），还是用外部编辑器打开（可配置命令，比如 `code -g file:line`）？
4. **冲突解决**：MVP 只提供“中止”和“用外部工具解决”可以吗？还是需要内置三方合并界面？
5. **是否需要 clone / init 仓库**？两个扩展都没有（VS Code 自带）。
6. **交互式变基**：Git Graph 是在终端里打开 `git rebase -i`。新客户端是打开系统终端，还是做一个可视化的交互式变基界面？
7. **托管平台集成**到什么程度？只需要“在浏览器中打开 commit / PR / Issue 链接”，还是需要 PR 列表、头像等？
8. **代码审查（Git Graph 的 Code Review）和统计图表（GitHD 的 Stats）** 你实际常用吗？目前都放在 P2。
9. ~~多窗口还是单窗口多标签？~~ **【已解决】** 单窗口多标签，每个标签一个仓库（见 M1-2、4.1 节）。
10. **git 依赖**：要求用户自己安装 git，还是希望在没有 git 时提示或自带 git？
11. 有没有你特别依赖、但本文漏掉的操作习惯或快捷键？
12. **AI 来源标注（M19）**：默认用 trailer 还是 git notes？你常用的代理是哪些（便于核实它们各自的 `Co-authored-by` 格式，或确认是否支持通过环境变量传入代理名和模型名）？是否需要 `AI-Session`？`commit-msg` 检查默认只警告还是直接拒绝？

---

## 9. 参考资料
- Git Graph
  - Marketplace：https://marketplace.visualstudio.com/items?itemName=mhutchie.git-graph
  - 仓库：https://github.com/mhutchie/vscode-git-graph
  - README：https://github.com/mhutchie/vscode-git-graph/blob/develop/README.md
  - CHANGELOG：https://github.com/mhutchie/vscode-git-graph/blob/develop/CHANGELOG.md
  - package.json（设置项、右键菜单项 `contextMenuActionsVisibility`）：https://github.com/mhutchie/vscode-git-graph/blob/develop/package.json
  - 设置项详细说明（Wiki）：https://github.com/mhutchie/vscode-git-graph/wiki/Extension-Settings
  - LICENSE：https://github.com/mhutchie/vscode-git-graph/blob/develop/LICENSE
- GitHD（Git History Diff）
  - Marketplace：https://marketplace.visualstudio.com/items?itemName=huizhou.githd
  - 仓库（实际地址）：https://github.com/huizhougit/githd
  - README：https://github.com/huizhougit/githd/blob/master/README.md
  - CHANGELOG：https://github.com/huizhougit/githd/blob/master/CHANGELOG.md
  - package.json：https://github.com/huizhougit/githd/blob/master/package.json
  - git 调用源码：https://github.com/huizhougit/githd/blob/master/src/gitService.ts
- MyGo
  - https://github.com/egoist/mygo
  - https://mygo.egoist.dev/docs
  - Native UI：https://mygo.egoist.dev/docs/ui
  - 桌面 API：https://github.com/egoist/mygo/blob/main/docs/native.md
  - 菜单：https://github.com/egoist/mygo/blob/main/docs/menus.md
  - 列表、表格、绘图、diff 查看器、代码编辑器、图表：https://github.com/egoist/mygo/tree/main/docs/ui （list.md / table.md / drawing.md / diff-viewer.md / code-editor.md / chart.md）
- git trailers：`git log --format` 的 `%(trailers)` 说明 https://git-scm.com/docs/git-log#Documentation/git-log.txt-trailersoptions ；`git interpret-trailers` https://git-scm.com/docs/git-interpret-trailers ；`git notes` https://git-scm.com/docs/git-notes ；hooks 与 `core.hooksPath` https://git-scm.com/docs/githooks
- Git 2.22 发行说明（`%(trailers)` 支持 key / valueonly）：https://github.com/git/git/blob/master/Documentation/RelNotes/2.22.0.adoc ；Git 2.32 发行说明（`git commit --trailer`）：https://github.com/git/git/blob/master/Documentation/RelNotes/2.32.0.adoc
- go-git 兼容性表：https://github.com/go-git/go-git/blob/main/COMPATIBILITY.md
- git log 提交排序：https://git-scm.com/docs/git-log#_commit_ordering
