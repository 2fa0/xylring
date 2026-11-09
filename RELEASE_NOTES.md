# Xylring v0.8.0

Release date: 2026-11-09

## 更新内容

- **年轮溯源**：在提交列表、图谱和详情中展示 Agent 与模型，支持筛选、搜索及 GUI 编辑。
- **终端印章**：提供仓库级 Hook、限时印章和 Agent 规则片段；支持单次消费和 Linked worktree。
- **严格校验**：可选的 `commit-msg` Hook 与 PR 检查要求 AI 提交同时填写唯一、非空的 `AI-Agent` 和 `AI-Model`；完全人工提交使用 `AI-Agent: human`，不填写模型。
- **年轮入口**：采用紧凑的圆角信息栏、年轮图标和两行状态文字，适配浅色与深色模式。
- **查找栏修复**：Diff 和文件查看器使用浮层查找栏，避免遮挡分隔线或折叠内容。

升级提示：重新运行 `xylring ring install` 和 `xylring ring strict install`，同步已安装的 Hook。AI 提交和印章现在必须提供真实模型名；未知时先确认，不能留空或猜测。历史提交保持不变。

## Changes

- Display, search, filter and edit Agent and model provenance in commit history, the graph and commit details.
- Add repository-scoped terminal hooks, expiring stamps, single-use stamps and Agent rule snippets, with linked worktree support.
- Require both a unique, nonempty `AI-Agent` and `AI-Model` for AI commits in optional strict hooks and PR checks. Human-only commits use `AI-Agent: human` without a model.
- Introduce a compact rounded provenance control with a tree-ring icon and two-line status in light and dark modes.
- Overlay find bars in diff and file viewers to avoid occlusion around separators and collapsed content.

Upgrade note: rerun `xylring ring install` and `xylring ring strict install` to update installed hooks. AI commits and stamps now require an actual model name; confirm it if unknown. Existing history is unchanged.

## 安装包 / Packages

| Platform | Architecture | File |
| --- | --- | --- |
| macOS | Apple Silicon | `xylring-0.8.0-arm64.dmg` |
| macOS | Intel x86_64 | `xylring-0.8.0-amd64.dmg` |
| Windows 10 / 11 | x64 installer | `xylring-setup-0.8.0-amd64.exe` |
| Windows 10 / 11 | x64 portable | `Xylring 0.8.0 windows-x64.zip` |

SHA-256 hashes are provided in `checksums.txt` alongside the packages.

Install Git before running Xylring. macOS builds are not notarized; Windows builds are not code signed.
