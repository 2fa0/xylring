# Xylring v0.7.0

Release date: 2026-11-09

## 更新内容

- **代码改动搜索**：在历史搜索框输入 `code:`、`diff:` 或 `content:`，按文本在代码中的增删查找提交；支持中文前缀与搜索建议。
- **Diff 与文件内查找**：Diff 和完整文件视图增加查找按钮；`⌘F` / `Ctrl+F` 优先查找当前内容，`⌘P` / `Ctrl+P` 和 `⌘K` / `Ctrl+K` 打开历史搜索。
- **仓库分组排序**：拖动顶层仓库调整分组顺序，子仓库随所属分组移动，重启后保留顺序。
- **文件树布局**：缩小行距和缩进，在侧栏中显示更多文件。
- **子仓库列表修复**：导入其他目录不再覆盖原有子仓库列表；会话保存完整列表，重启后仍显示未打开过的子仓库。导入失败时保留已有列表。

升级提示：旧版会话没有保存未打开的子仓库；升级后请重新导入一次缺失的目录，新版会保存完整列表。

## Changes

- Search code additions and removals with `code:`, `diff:` or `content:` in history search, with prefix-aware suggestions.
- Find within diffs and full-file views using the toolbar or `⌘F` / `Ctrl+F`. Use `⌘P` / `Ctrl+P` or `⌘K` / `Ctrl+K` for history search.
- Drag top-level repository groups to reorder them together with their children; retain the order across restarts.
- Reduce file-tree row spacing and indentation to show more files in the sidebar.
- Keep repositories from earlier imports when importing another directory. Restore unopened repositories in the sidebar across restarts, and preserve the list if an import fails.

Upgrade note: older sessions did not save unopened repositories. Reimport any missing directory once after upgrading.

## 安装包 / Packages

| Platform | Architecture | File |
| --- | --- | --- |
| macOS | Apple Silicon | `xylring-0.7.0-arm64.dmg` |
| macOS | Intel x86_64 | `xylring-0.7.0-amd64.dmg` |
| Windows 10 / 11 | x64 installer | `xylring-setup-0.7.0-amd64.exe` |
| Windows 10 / 11 | x64 portable | `Xylring 0.7.0 windows-x64.zip` |

SHA-256 hashes are provided in `checksums.txt` alongside the packages.

Install Git before running Xylring. macOS builds are not notarized; Windows builds are not code signed.
