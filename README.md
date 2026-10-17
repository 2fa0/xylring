# Git Client

Go + MyGo v0.5.5 Native UI + 系统 Git。功能范围按《独立桌面 Git 客户端需求文档 v0.3》的 P0 实现，验收状态见 [P0-进度.md](P0-进度.md)。

## 本机运行

需要 Go 1.27.2 和系统 Git，在 `prototype/` 执行：

```sh
go run . /path/to/repository
```

构建 macOS 应用：

```sh
go tool mygo build -skip-dmg -skip-notarize
```

生成 `prototype/build/darwin-arm64/Git Client.app`。本机构建使用 ad hoc 签名，未进行发行公证。

## 使用

- `+` 打开仓库；标签可以拖动排序，退出时保存标签、筛选、选择和滚动位置。
- 左侧切换引用、文件树或 Stash。点击引用只浏览历史，检出操作在右键菜单中。
- 提交右键可新建分支、检出、合并、cherry-pick、revert、reset 或添加标签；引用右键可切换、删除、合并和推送。
- Fetch 在顶栏；推送、拉取、Stash、冲突中止和外部工具在“操作”菜单中。
- 搜索完整历史中的消息正文、作者、日期、哈希、分支和标签；“下一个”逐项跳转。
- 文件右键提供版本查看、工作区比较和重命名追踪历史。设置可分别应用到用户或仓库。
- `Cmd/Ctrl+T` 打开仓库，`Cmd/Ctrl+W` 关闭标签，`Cmd/Ctrl+1…9` 或 `Ctrl+Tab` 切换。

所有写操作先确认；hard reset 再次确认。系统 Git 的 credential helper、SSH agent 和 hooks 继续生效，需要输入凭据时弹出认证框。

本客户端当前不提供 P1 的暂存和创建提交界面。需要完成冲突后的提交时可使用配置的外部工具或终端。

## 验证

在 `prototype/` 执行：

```sh
CGO_ENABLED=0 go test ./...
go test -race ./...
go tool mygo vet .
```

从仓库根目录测量 macOS 原生进程（生成独立的十万条提交仓库）：

```sh
python3 scripts/p0-native-benchmark.py --output /tmp/gitclient-p0.json
python3 scripts/p0-native-benchmark.py --soak-minutes 60 --output /tmp/gitclient-p0-soak.json
```

脚本使用发布构建、记录 RSS、macOS physical footprint 和 Go 堆，并验证窗口确实完成绘制。`P0_CAPTURE=1` 可额外导出原生截图；截图会增加测量过程的临时内存，不建议在内存基准测试中启用。
