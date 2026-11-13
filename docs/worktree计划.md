# linked worktree 计划

本文是 `docs/演进计划.md` 的模块 C 的执行计划：把 `git worktree`（linked worktree，下称「工作树」）
接进侧栏。起因是对照一个更完整的桌面客户端的工作树管理。

**命名先说死**：本仓库里 `worktree.go` 指的是图谱顶部那一行「未提交改动」，和 `git worktree` 无关。
本文一律写「工作树」，代码里用 `repoWorktree` / `linkedWorktree`，不再新增裸 `worktree` 标识符。

## 一、现状

| 事实 | 位置 |
| :--- | :--- |
| 没有任何 `git worktree` 命令 | `grep '"worktree"'` 只命中 rev 字面量（`compare.go:1554`、`file_tabs.go:601`）和 `settings.go:203`、`settings_git.go:108` 的 config scope 判断 |
| 打开一个工作树目录今天就能用：`isGitWorkTree` 接受 `.git` 为文件 | `git.go:248` |
| 打开入口：`openRepo`（带 `repoRoot` 归一）与 `openPath` | `repo.go:128` / `repo.go:149` |
| 侧栏分层：`repoItems` 给出仓库列表，`repoNestLevel` 按路径包含算缩进 | `main.go:695` / `model.go:238` |
| 仓库状态（分支、脏）已能按路径查：`repoStatus` | `repo_status.go:72` |

所以缺的不是「能不能打开」，而是**归属关系**：今天一个工作树在侧栏里是一条独立仓库，看不出它属于谁，
也看不出同一个仓库有几个工作树。

## 二、抄什么，不抄什么

### 抄（只读部分）

| 项 | 做到什么程度 |
| :--- | :--- |
| 工作树列表 | `git worktree list --porcelain -z`，显示分支名（或分离头指针短哈希）、锁定、可清理 |
| 归属展示 | 挂在对应仓库下面一层，点和普通仓库一样打开 |
| 脏状态 | 复用 `repoStatus`，不新增取数逻辑 |

### 不抄（本轮）

| 项 | 为什么不做 |
| :--- | :--- |
| 创建 / 删除 / prune | 都是写操作，`docs/代码组织.md` 的写操作范围里没有它们；删除还必须先有安全判定 |
| Finish（合并回目标分支并清理） | 依赖历史编辑计划 |
| 移动（`git worktree move`） | 用的频率极低，且要处理打开中的标签 |
| bare 仓库的工作树管理 | 只标注 `bare`，不打开、不管理 |
| 在图谱里区分「这条提交属于哪个工作树」 | 工作树不是分支，混进图谱会让行含义变模糊 |

## 三、两步落地

### 第 1 步：列表与打开（排期内的那一步）

**可见结果**：侧栏里，有工作树的仓库下面多一层：每个工作树一行，显示分支名（分离头指针显示短哈希）
和脏标记，锁定的带锁图标；点一下就在新标签里打开那个目录。主工作树不重复出现。

**改动**

1. `cmd/gitclient/git.go`：`listWorktrees(ctx, dir string) ([]repoWorktree, error)`

   ```sh
   git worktree list --porcelain -z
   ```

   按 `\0` 分段解析：`worktree <path>`、`HEAD <sha>`、`branch refs/heads/<name>`、
   `detached`、`locked [<reason>]`、`prunable [<reason>]`、`bare`。
   路径 `filepath.Clean` 成绝对路径，用 `canonicalRepoDir`（模块 B 的 `docs/Git执行计划.md` 第 2 步引入）归一后再比较是否为主工作树。
   如果模块 B 还没落地，第 1 步先在本文件里放一份同名函数，合并时不冲突。

2. `cmd/gitclient/model.go`：

   ```go
   type repoWorktree struct {
       path, branch string
       head         [20]byte
       detached     bool
       locked       string // 非空即锁定，值是可选的 reason
       prunable     string
       bare         bool
   }
   ```

   `repoTab` 加 `worktrees []repoWorktree`；`repoMeta` 同样加一个字段。

3. 读取时机：`loadMeta`（`git.go:638`）里跟 `for-each-ref` 同一批（同一个 goroutine，加一次进程）。
   `listWorktrees` 出错**不阻断** `loadMeta`——工作树只是附加信息，失败就当没有。
   `installHistory`（`repo.go:529`）落到 `repoTab`。

4. 侧栏：`sidebarRepoSection`（`main.go:725`）渲染到某个仓库时，若它有工作树，就在该行之后插子行：

   - 缩进沿用嵌套仓库的口径（`repoNestLevel` 的 `Space(4)`），同一层只用一种含义；
   - 子行主文案：`branch`，没有则 `分离头指针 <短哈希>`；
   - 锁定：`iconLock`（新增 `icons/lock.svg`，Lucide，ISC，与现有图标同一来源），tooltip `已锁定`；
   - 可清理：`prunable` 非空时同位置显示 `iconAlertCircle`，tooltip `可清理`；
   - 脏状态：复用 `repoStatus(path)` 的 `dirty`，与仓库行同一套显示；
   - 点击：`a.openRepo(worktreePath)`；右键沿用仓库行的菜单（访达、终端、复制路径）。

5. i18n：`工作树`、`已锁定`、`可清理`、`分离头指针`。

**不做**：不创建、不删除、不 prune；不在图谱里标注；不把工作树变成仓库分组的一种（分组另算）。

**关联项，不进验收**：`worktreeHash` 哨兵现在有 16 处判断（`grep -c worktreeHash`），
接工作树列表时行模型本来就要动（`rowData` / `commitCell` / `worktreeThrough` / `worktreeHead`），
顺手把它们收成一个「行是什么」的判别值。收敛范围仅限这 16 处，不做独立的行模型重构，
也不会因为没收干净而卡住本文第 1 步。

**验收**
- `initTestRepo` + `git worktree add ../wt -b side`，重开仓库：侧栏该仓库下出现 `side` 一行，点击打开，
  标题栏路径指向那个目录。
- `git worktree lock ../wt`：出现锁图标，tooltip `已锁定`。
- 在 `../wt` 里 `git checkout --detach`：子行变成 `分离头指针 <短哈希>`。
- 在 `../wt` 里改一个文件：子行带脏标记。
- `go test ./cmd/gitclient/ -run 'TestParseWorktreeList'`：普通 / 分离 / 锁定 / prunable / 空输出 / 含空格路径六种快照。

### 第 2 步：创建与清理（未排期，前置条件写死）

开工前必须先满足两条，缺一条都不动手：

1. `docs/代码组织.md` 的写操作范围补上「工作树的创建与删除」；
2. `docs/操作撤销计划.md` 的安全判定先落地（模块 E 负责，本节不重复实现）。

范围（写在这里，免得将来重新讨论）：

- 创建：`git worktree add <path> -b <branch>`；分支已存在时复用（不加 `-b`）；路径冲突直接报错，不自动改名。
- 删除：只允许「该工作树的提交已在目标分支 + 没有未提交改动 + 没有 ignored 文件」的，判定函数来自模块 E。
- prune：只清 `prunable` 的条目，先列出来再让用户确认。
- Finish：不做。

## 四、顺序与时间盒

第 1 步 1 天。第 2 步不估，等前置两件事。

## 五、风险

| 风险 | 处理 |
| :--- | :--- |
| porcelain 输出里含空格或换行的路径被截断 | 用 `-z` 按 `\0` 分段；测试覆盖含空格路径 |
| 工作树子行和「嵌套仓库」的缩进打架，同一层两种含义 | 两者共用 `repoNestLevel` 的层级口径；取消嵌套仓库与工作树的混排，工作树优先显示 |
| 主工作树重复出现 | 渲染时按「路径等于当前仓库」跳过，测试断言行数 |
| 每次 reload 多一次 git 进程 | 与 `for-each-ref` 合在同一个 goroutine；失败不阻断仓库加载 |
| 工作树目录被手动删掉，列表里还留着 | `prunable` 只标注、不自动清理；用户点开失败时走现有 `errRepoUnavailable` 提示 |
