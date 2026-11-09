# 年轮：Agent 与模型溯源

提交列表、图谱外环、详情支持读取标准 `AI-Agent`、`AI-Model`、`AI-Attribution` Trailer。
可用 `agent:Cindy`、`model:GPT-5 Codex` 搜索，或右键徽章筛选。
模型别名仅用于展示与搜索，历史原文不变。无溯源的提交按人工提交展示。

## GUI

展开提交面板，点击「记录 AI 协同…」填写 Agent 和确知的模型名。
默认不选中任何组合，最近使用最多保留 8 项，仅供点击选择。
印章作为建议，点击后才应用。修改上次提交时回填年轮字段；清空年轮会移除 `AI-*`。
「修改说明…」也可编辑这些字段。GUI 提交始终设置 `XYLRING_GUI=1` 跳过自动盖章。

仓库侧栏和提交面板里的年轮入口管理同一个仓库的 Hook、印章与规则片段。
开启前需确认会修改 `.git/hooks`，且 Trailer 随推送公开。
关闭会还原备份的原 Hook；原 Hook 非零退出仍阻止提交。

## 终端

已安装旧版启动器时，先重新执行 `xylring install-cli`。

```sh
xylring ring install
xylring stamp --agent Cindy --model '确知的模型名'
xylring stamp --show
xylring stamp --clear
xylring ring status
xylring ring uninstall
```

印章默认有效 30 分钟，覆盖有效期内多次终端提交；`--ttl 10m` 可修改期限，
`--once` 表示单次消费。Linked worktree 各自持有印章。
只有显式安装 Hook 才会自动盖章，默认不推断进程、IDE、环境变量或模型。
Hook 为独立 POSIX sh 脚本，不依赖 Xylring 二进制。

### Git 的 Amend 边界

Git 在 `--amend --no-edit` 时给 Hook 的 source 是 `commit`，自动跳过；
但 `--amend -m` / `--amend -F` 的 source 是 `message`，与普通提交无法区分。
使用仍生效的印章时，请明确跳过：

```sh
XYLRING_RING=0 git commit --amend -m '修订说明'
```

GUI 的 Amend 不受这个限制。merge、squash source 也跳过。
Hook 写入失败会静默放行，不阻止 Git 提交。

### 自定义 Hook 路径

已设置 `core.hooksPath` 时，本版保守采用手动集成，包含仓库外个人目录。
通过设置窗口复制脚本，或运行 `xylring ring script`，保存为独立脚本，
再在现有 `prepare-commit-msg` 中用相同参数调用：

```sh
sh /absolute/path/to/xylring-ring-hook "$@"
```

### Agent 规则

`xylring ring snippet` 输出规则片段。也可在 GUI 复制。
GUI 支持预览并确认写入或移除 `AGENTS.md`、`CLAUDE.md`、
`.cursor/rules/xylring-provenance.mdc` 中的标记片段。
这些文件通常受版本控制，会影响协作者；不会自动改动全局配置。
若需要全局配置，请自行粘贴到 Agent 文档指定的全局规则文件。

## 当前验证范围

- 原生签名当前只启用有本仓库真实提交样本的 `Assisted-by: Cindy (https://cindy.app)`。
  Claude Code、Aider、Copilot、Cursor 及通用签名需补充真实样本后再启用；可先用显式 `AI-*`。
- 启动时探测实际 Git pretty-format 能力，不支持时降级为普通历史；详情仍可读取消息。
- 过期印章被忽略且不主动删除，以避免与其他进程刚写入的新印章发生竞争。
- 实验性推断和年轮统计（Phase 3）尚未实现。

## 性能验收记录（2026-11-09）

本机合成仓库含 10,000 条提交，其中 50% 带年轮；预热后连续三轮、每轮交替读取五次：
旧格式平均约 46–51ms，白名单 Trailer 格式约 55–60ms，增加约 17–18%。
提交结构估算内存增量为 0 字节（`uint16` 利用了原结构的对齐空隙），溯源表另行计量。
因此内存结构检查通过，**方案的读取耗时增幅 <5% 目标尚未达成**；该项仍需优化和进一步基准验证。
Windows 当前仅通过交叉编译，真实 Git Bash 测试由新增 CI 工作流执行，尚无本次运行回执。
