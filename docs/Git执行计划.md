# Git 执行计划

本文是 `docs/演进计划.md` 的模块 B 的执行计划：Git 调用层的两件事——
**命令日志**和**每仓库读写分道**。起因是对照一个更完整的桌面客户端，看它怎么管 Git 调用。

现状一句话：我们所有的 git 调用共用一个容量 4 的全局信号量，失败只写一条应用日志，
用户和我们都拿不到「刚才到底跑了哪条命令」。

## 一、现状

| 事实 | 位置 |
| :--- | :--- |
| 唯一入口：`runGit` → `runGitLimit`（16MB 上限）→ `runGitExtra` | `git.go:100` / `129` / `133` |
| 长流入口：`streamLog`，用 `gitLease` 占槽直到流读完 | `git.go:370` / `git.go:355` |
| 并发：`gitSlots` 容量 4，`acquireGit` 不区分读/写，也不区分仓库 | `git.go:36` / `git.go:38` |
| 输出上限：`limitedBuffer` 按字节截断并记 `overflow`；stderr 上限 64KB | `git.go:104` |
| 失败记录：`logGitFailure` 只记 `repo` / `operation` / `error`，且用 `gitFailureLogAllowed` 去重 | `git.go:456` / `git.go:470` |
| 环境清洗：`mergeGitEnv` 固定 `LC_ALL=C`、`GIT_TERMINAL_PROMPT=0`、`GIT_PAGER=cat` | `git.go:67` |

写操作散在几个文件里，而且同一个文件里读写混排，所以**按文件判断读写不成立**
（例：`write.go:142` 的 `log -1 --format=%B` 是读，`write.go:234` 的 `reset --soft` 是写）：

- `write.go`：标签、分支、说明、checkout、reset、rebase
- `operations.go`：stash、远程、初始化、克隆
- `sync.go`：fetch / pull / push

## 二、抄什么，不抄什么

### 抄

| 项 | 做到什么程度 |
| :--- | :--- |
| 命令日志 + 脱敏 | 会话内 200 条 / 8MB，每条记命令、耗时、退出码、输出摘要；URL 与 token 类参数脱敏；二进制输出只留字节数 |
| 每仓库读写分道 | per-repo 车道：只读并行、非只读独占、FIFO 防饿死、取消摘队；全局槽保留 |

### 不抄

| 项 | 为什么不做 |
| :--- | :--- |
| 错误对象里塞 `commandLogID` | 会改用户可见的错误文案。面板按「最近的失败」定位就够了 |
| 命令日志落盘 | 崩溃诊断已有 `internal/crashlog`；命令日志的价值在「当下这一小时」，落盘还要处理清理和隐私 |
| 重试 / 超时策略 | `runGit` 现在没有重试，加进来会改变所有调用点的语义，不在本模块 |
| 记录流式输出内容 | `streamLog` 可能几百 MB。只记命令、耗时、退出码，面板里标注「流式输出不记录」 |
| 队列深度上限 | 同一仓库的并发读本来就受全局 4 槽限制，再加一层上限没有收益 |

## 三、两步落地

### 第 1 步：Git 命令日志

**可见结果**：菜单「显示 → Git 日志…」打开一个面板，按时间倒序列出本会话最近 200 条 git 调用，
每条显示命令、耗时、退出码，失败行标红并可展开看输出。错误横幅旁多一个「查看 Git 日志」，
点开面板并选中最近一条失败。

**改动**

1. 新文件 `cmd/gitclient/gitlog.go`

   ```go
   type gitCallOutput struct {
       text      string // 脱敏、截断后的前缀
       bytes     int    // 真实字节数
       truncated bool
       binary    bool
   }

   type gitCall struct {
       at       time.Time
       id       uint64
       dir      string
       command  string
       ms       int64
       exit     int
       stream   bool // streamLog 的输出不记录
       status   gitCallStatus // running / ok / failed / cancelled
       out, err gitCallOutput
   }
   ```

   - `gitLog.begin(dir string, args []string) uint64` / `gitLog.finish(id, status, dur, stdout, stderr []byte)`；
     `entries(dir) []gitCall` 返回副本。
   - 上限：200 条 / 8MB 双计；`trim()` 只从**已结束**的条目开始丢，运行中的条目永不丢。
   - `redactGitArgs(args []string) string`、`redactGitText(s string) string`：四条规则——URL 里的 `user:pass@`、`authorization:` / `proxy-authorization:`、
     `token=` / `--token` / `--access-token` / `private-token=`、`http.extraheader`。
   - `summarizeGitOutput(b []byte, limit int) gitCallOutput`：`utf8.Valid` 失败或含 `\x00` → `binary=true`，
     只留字节数；否则留前 32KB 并标 `truncated`。`limit` 参数让 `streamLog` 传 0（不记内容）。

2. `git.go`：在 `runGitExtra` 和 `streamLog` 里各加三行——`acquireGit` 之后 `begin`，
   `cmd.Run()` / 扫描结束之后 `finish`。`limitedBuffer` 的 `Bytes()` 直接喂给 `summarizeGitOutput`，
   不复制整份输出。取消和超时也 `finish`，状态分别记 `cancelled` / `failed`。
   `logGitFailure` 保持原样（应用日志 + crashlog breadcrumb），不改错误文案。

3. 面板：新文件 `cmd/gitclient/gitlog_ui.go`，用 `ui.List` + `listTheme`，顶部一个「清空」。
   展开行显示输出与错误，各带「复制」。菜单项加在 `menu.go` 的「显示」子菜单：

   ```go
   {ID: "git-command-log", Label: tr("Git 日志…"), Click: ...},
   ```

4. 横幅：`setBanner`（`repo.go:27`）的渲染处加一个「查看 Git 日志」按钮，
   只在最近有一条失败时出现，点击打开面板并选中它。

5. `cmd/gitclient/locales/en.json`：`Git 日志…`、`Git 日志`、`命令`、`耗时`、`退出码`、`输出`、
   `流式输出不记录`、`清空`、`复制`、`没有记录`、`查看 Git 日志`。

**不做**：不落盘；不进错误文案；不加搜索/过滤（就 200 条）；不记 `streamLog` 的输出内容。

**验收**
- 打开仓库、翻一页历史、切一次分支：面板里出现 `log`、`symbolic-ref`、`checkout` 三类条目，耗时不全为 0。
- 对没有网络的远程执行 fetch：出现一条失败记录，横幅的「查看 Git 日志」跳到它。
- `go test ./cmd/gitclient/ -run 'TestGitLog'`：四条脱敏规则、二进制抑制、双上限裁剪（第 201 条丢最老的已结束条目）、
  取消也留一条 `cancelled`。

### 第 2 步：每仓库读写分道

**可见结果**：界面没有变化。保证同一仓库的写操作不会和历史读取交错，写操作也不会被后台刷新饿死。

**改动**

1. 新文件 `cmd/gitclient/gitlane.go`

   ```go
   type gitWaiter struct {
       write bool
       ready chan struct{}
   }

   type gitLane struct {
       readers int
       writing bool
       queue   []*gitWaiter
   }
   ```

   - `gitLanes map[string]*gitLane` + 一把互斥锁；键是 `canonicalRepoDir(dir)`。
   - `acquireGitLane(ctx, key string, write bool) (release func())`：能立刻拿就直接拿；
     否则入队等 `ready`，`ctx.Done()` 时从队列里摘掉自己。`grant()` 按 FIFO 放行：
     队首是读且没有写者 → 放行（并可以连续放行后续的读）；队首是写 → 必须等 `readers == 0`。
   - `canonicalRepoDir(dir)`：把 `repoGitEnv`（`git.go:56`）里那段 `Abs` + `EvalSymlinks` 提出来，
     日志和车道共用同一个键。

2. 读写分类：`func gitCommandWrites(args []string) bool`，按**子命令**判断，不看调用文件。

   | 读 | 写 |
   | :--- | :--- |
   | `log`、`rev-list`、`rev-parse`、`for-each-ref`、`symbolic-ref`、`show`、`diff`、`ls-tree`、`cat-file`、`merge-base`、`var`、`version`、`status`、`worktree list`、`branch --list`、`tag --list`、`remote -v`、`config --get` / `--get-regexp` / `--list` | `checkout`、`switch`、`branch`（其他用法）、`tag`（其他用法）、`reset`、`rebase`、`merge`、`commit`、`stash`、`update-ref`、`worktree`（除 `list`）、`remote`（除 `-v`）、`config`（其他用法）、`fetch`、`pull`、`push`、`restore`、`gc`、`prune` |

   - `status` 能算读，前提是先让它在环境里不抢锁：`mergeGitEnv` 加 `GIT_OPTIONAL_LOCKS=0`
     （它只关掉 status / diff 的**可选**锁，写操作的强制锁不受影响）。否则 `status` 会写 index，
     只能当写处理，那就会让侧栏每次刷新去和读抢车道。
   - 参数里可能带全局选项（`gitArgs` 固定加 `--no-pager` 与四条 `-c`，调用方也可能传 `-c`、`--git-dir`），
     分类前先跳过这些「带值的选项 + 值」，取第一个真正的子命令。
   - **未知子命令按写处理**（保守），`TestGitCommandWrites` 逐条钉住这张表，新增子命令时不补表就会失败。

3. `runGitExtra` / `streamLog`：先拿车道，再拿全局槽，`defer` 逆序释放。

   ```go
   releaseLane := acquireGitLane(ctx, canonicalRepoDir(dir), gitCommandWrites(args))
   defer releaseLane()
   if err := acquireGit(ctx); err != nil { return nil, err }
   defer func() { <-gitSlots }()
   ```

   全局 `gitSlots` 仍是 4。

**不做**：不改全局槽容量；不动 `historyPager` 的流生命周期和 `historyTips` 的固定快照；
不做跨仓库串行；不做重试。

**验收**
- `TestGitLane`：两个读者同时进行；写者独占（写期间读者等待）；FIFO（写者排队后，后来的读者不能插队）；
  取消摘队；不同键并行。
- `TestGitCommandWrites`：上表逐条，外加一条未知子命令 → 写。
- 手动：一边连按 `⌘R` 刷新，一边连打标签 20 次；标签全部成功，刷新不超时。

## 四、顺序与时间盒

| 顺序 | 内容 | 时间盒 |
| :--- | :--- | :--- |
| 1 | 命令日志 | 1 天 |
| 2 | 每仓库读写分道 | 0.5 天 |

先做日志：车道的行为肉眼看不见，出问题时日志是唯一能证明「谁挡了谁」的证据。

## 五、风险

| 风险 | 处理 |
| :--- | :--- |
| 脱敏漏项，凭据进了面板 | 四条正则 + `TestGitLog` 覆盖；面板只在本机内存，不落盘，也不进崩溃日志 |
| 日志复制一份大输出，内存翻倍 | `summarizeGitOutput` 只取前 32KB，直接读 `limitedBuffer` 的结果 |
| 车道键算错（软链接、大小写、末尾斜杠） | 键统一走 `canonicalRepoDir`，日志与车道共用；测试用「真目录 + 指向它的符号链接」两条路径断言同键 |
| 写子命令漏分类 | 未知子命令默认按写；`TestGitCommandWrites` 钉住清单，新增子命令时必须补表 |
| `status` 归读后仍有隐藏写入 | 靠 `GIT_OPTIONAL_LOCKS=0`；`TestGitCommandWrites` 里把 `status` 归读、`TestGitLane` 里确认 `status` 与读者并行 |
| 车道和全局槽顺序颠倒导致死锁 | 固定「先车道后槽」，`defer` 逆序释放；`TestGitLane` 里加一个「持槽再要车道」的反例注释，禁止这种写法 |
