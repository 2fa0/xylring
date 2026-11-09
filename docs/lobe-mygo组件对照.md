# Lobe MyGo 组件对照

对照日期：2026-11-09。依据本地源码与 [Lobe MyGo 源码](https://github.com/lobehub/lobe-mygo) 的组件实现；这是代码与交互能力对照，尚未完成两套原生应用的并排视觉验收。

## 能否直接复制

可以。上游采用 MIT 许可，允许复制、修改和分发，需随代码保留版权和许可声明。本轮适配代码位于 `cmd/gitclient/lobe_widgets.go`，许可原文位于 `third_party/lobe-mygo.LICENSE`。没有添加 `lobe-mygo` 模块依赖，也没有升级 MyGo。

本地使用 MyGo v0.8.3；此次查看的上游使用 v0.8.4-0.20261108082913-9233663736ca。复制时需逐项核对 API，不能把整个组件目录直接搬入。很多组件依赖上游的 theme、motion、overlay、lucide 和本地化包；只复制单个 Go 文件通常不能编译。

## 具体差距与处理

| 本地位置 | 对方实现 | 对照结论 | 本轮处理 |
| --- | --- | --- | --- |
| `icons.go` 的标题栏、面板、切换、关闭按钮 | `pkg/action/actionicon.go` | 对方将图标尺寸、点击区域、激活与悬停状态集中实现。本地有四份相似样式，面板图标点击区域约 22 DIP，标题栏还关闭了焦点圈。对应 interface-details 的 interactivity-3、accessibility-1。 | 适配共享图标按钮样式，面板按钮统一为 24 DIP，标题栏保留 26 DIP，恢复默认焦点圈。继续使用本地主题、图标和 120ms 颜色过渡。 |
| `main.go` 的错误横幅复制按钮 | `pkg/action/copybutton.go` | 对方复制后在原位显示勾选并保持两秒；本地依赖 Toast。原位反馈能清楚指出刚才执行的是哪个操作。 | 移植基于元素动画状态的两秒确认，显示“已复制”标签和勾选；重复点击重新复制当前内容并重置确认时间。 |
| `history_query.go` 的全能搜索工具栏 | `pkg/field/searchbar.go` | 对方把搜索图标、清除、加载态和快捷键提示放在输入容器中。本地视觉更分散，但已有更完整的 Git 搜索语法、联想、筛选退出与定位功能。 | 已适配统一输入外壳，内置搜索、清空、加载状态与平台快捷键提示；清空后恢复输入焦点，保留当前筛选、Git 搜索逻辑与 Handle。 |
| `file_tabs.go` 的文件标签栏 | `pkg/nav/tabs.go` | 对方有独立选中指示器和滑动过渡。本地用 DrawOver 直接画下划线，但已有滚动显露、关闭、右键菜单等文件工作流。 | 已适配独立选中指示器与 180ms 位置过渡，取消选中时加粗造成的宽度变化，并补充完整路径和版本 Tooltip。保留文件工作流。 |
| `write_ui.go`、`operations_ui.go` 等弹窗 | `pkg/feedback/modal.go` | 对方集中管理标题、关闭、页脚与滚动正文，并限制宽高。本地各弹窗分别拼装固定宽度内容，缺少统一外壳。 | 已统一六类弹窗：比较、获取远程、提交/分支/标签操作、仓库操作、年轮设置、丢弃确认。统一标题与关闭按钮、滚动正文、固定页脚；宽高限制在窗口减 32 DIP 内。运行中的写入、获取、仓库操作及年轮任务禁止遮罩、Esc 和关闭按钮误关，保留原有取消执行与确认规则。 |
| 全局错误横幅 | `pkg/feedback/alert.go` | 对方的标题/描述分层和状态类型更完整；本地已有复制、仓库归属与关闭逻辑，单一错误横幅并不需要整个通用 Alert API。 | 仅改复制反馈，保留现有错误展示。 |
| 普通按钮、输入、Checkbox、Select | `pkg/action/button.go`、`pkg/field/input.go` 等 | 本地多使用 MyGo 标准控件；对方提供更多皮肤与尺寸选项。这不构成功能或质量上必然更优的证据。 | 暂不批量替换。 |

提交图谱、Diff 编辑器、文件树、暂存与历史分页是本地业务组件；上游通用组件没有提供可直接替换的同等实现。

## 适配边界

- 使用 `ui.ButtonBase` 保留键盘、禁用和可访问性语义，持久状态由 MyGo 的稳定 Key 管理。
- 不引入上游整套设计 token、图标包和动效系统。
- 当前只将原位复制反馈接入错误横幅，其他复制入口保留现有行为。
- 已完成对照中待办的搜索框、文件标签栏和六类弹窗适配；使用本地 MyGo v0.8.3 的基础控件，没有复制上游整套依赖。
- 弹窗采用原生 DialogBase 保留焦点约束；空闲时支持 Esc、遮罩与关闭按钮，运行中使用已有取消操作。

## 验证与预览

全量 `CGO_ENABLED=0 go test ./...`、新增布局交互检查和 `go tool mygo vet .` 均通过。

新增 `lobe_layout_test.go` 覆盖六类弹窗在 420×320、1100×720 和深浅主题下的边界与页脚可见性，以及 Esc 关闭、执行中防误关、取消、重开、搜索清空后的焦点、加载防重复提交、标签切换和移除后重建。原有搜索语法、联想、文件标签菜单及 Git 操作测试也纳入全量检查。

预览使用本地 MyGo Tester 渲染器生成，属于应用渲染截图，不代表已经在 macOS/Windows 原生窗口中做过人工端到端验收。

- [主界面（浅色）](validation/lobe-mygo/main-light.png)
- [主界面（深色）](validation/lobe-mygo/main-dark.png)
- [窄窗口主界面](validation/lobe-mygo/main-narrow.png)
- [短窗口年轮设置](validation/lobe-mygo/ring-narrow.png)
- [短窗口仓库操作（深色）](validation/lobe-mygo/operation-narrow-dark.png)
- [文件标签页](validation/lobe-mygo/file-tabs.png)

复现：

```sh
CGO_ENABLED=0 go test ./...
go tool mygo vet .
LOBE_UI_PNG_DIR=/tmp/xylring-lobe-previews CGO_ENABLED=0 go test ./cmd/gitclient -run '^TestLobe' -count=1
```
