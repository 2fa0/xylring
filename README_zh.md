<p align="center">
  <img src="cmd/gitclient/resources/logo.png" alt="Xylring Logo" width="96" height="96" />
</p>

<h1 align="center">Xylring</h1>

<p align="center">
  <strong>基于 Go 开发的原生 Git 客户端。</strong><br>
  在桌面界面中浏览仓库历史、审阅改动和执行 Git 操作。
</p>

<p align="center">
  <a href="#为什么要做-xylring">核心特性</a> •
  <a href="#下载与安装">下载安装</a> •
  <a href="#命令行-cli-使用">命令行</a> •
  <a href="#路线图-roadmap">路线图</a> •
  <a href="./README.md">English</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Release-v0.8.1-blue.svg?style=flat-square" alt="Release" />
  <img src="https://img.shields.io/badge/Go-1.27+-00ADD8.svg?style=flat-square&logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/平台-macOS%20%7C%20Windows-brightgreen.svg?style=flat-square" alt="Platforms" />
</p>

---

## 💡 为什么要做 Xylring？

Xylring 是使用 Go 和 MyGo 构建的桌面 Git 客户端。它调用系统 Git，
通过原生界面浏览和操作本地仓库。

---

## ⚡ 核心亮点

- 🚀 **原生桌面应用**：使用 Go 和 MyGo 构建，不依赖 Electron 界面。
- 🎨 **平滑三次贝塞尔提交图谱**：第一父链同色追踪，跨行连线平滑过渡；大仓库分页滚动时已渲染节点不抖动，合并节点空心区分。
- 🔍 **全能搜索与智能联想（Omni Search）**：一个搜索框覆盖提交信息、代码变动（Pickaxe -S）、作者、文件路径、分支、标签与 SHA 哈希；支持输入前缀语法（如 `code:`、`author:`、`path:`）与下拉建议；Diff 审阅内支持 ⌘F / Ctrl+F 页面内快速查找与高亮。
- ⚖️ **快照与分叉点任意比对**：任意选择两个提交或分支比对，支持两点快照比较或三点（`...` 分叉点起）改动审查。
- 🧩 **块级交互式暂存（Hunk Staging）**：精细审阅单个文本改动块，支持块级暂存/取消暂存/丢弃改动，支持工作区提交与 Amend 上次提交。
- 💻 **深度契合终端工作流**：支持在终端像 VS Code 一样随时输入 `xylring .` 唤醒当前仓库，支持同时打开多仓库。
- 🖼️ **全量文件语法高亮与图片 Diff**：支持查看任意历史快照的完整只读文件，支持多格式图片并排对比。
- 🛡️ **本地仓库操作**：本地操作调用系统 Git；获取、拉取和推送会连接配置的 Git 远程仓库。

---

## 📦 下载与安装

预编译安装包将在 [**GitHub Releases**](https://github.com/2fa0/xylring/releases) 发布；首个版本发布前，请按下文从源码构建。

### macOS 用户 (Apple Silicon / Intel)

1. 根据芯片架构下载对应磁盘镜像：
   - **Apple 芯片 (M1/M2/M3/M4 系列)**：`xylring-0.8.1-arm64.dmg`
   - **Intel 芯片 (x86_64)**：`xylring-0.8.1-amd64.dmg`
2. 双击打开 `.dmg`，将 **Xylring** 拖入「应用程序（Applications）」文件夹。

> [!NOTE]
> **macOS 首次打开安全提示**：  
> 如果发行版本未经过 Apple 开发者证书公证，初次打开若提示“无法验证开发者”，请在访达（Finder）中对 `Xylring.app` 按住 **Control** 点按并选择「打开」，或在终端执行：
> ```sh
> xattr -cr /Applications/Xylring.app
> ```

### Windows 用户 (Windows 10 & 11 x64)

- **安装版**：下载运行 `xylring-setup-0.8.1-amd64.exe`（安装到当前用户目录，无需管理员权限）。
- **便携版**：下载解压 `Xylring 0.8.1 windows-x64.zip`，直接双击 `Xylring.exe` 即可运行。

### 源码编译安装

环境要求：**Go 1.27+** 和系统 **Git**。

```sh
# 克隆仓库
git clone https://github.com/2fa0/xylring.git
cd xylring

# 直接运行
go run ./cmd/gitclient /path/to/your/repo

# 或编译 macOS 原生 .app
cd cmd/gitclient
go tool mygo build -skip-dmg -skip-notarize
```

---

## 🚀 命令行（CLI）使用

像 VS Code 的 `code .` 一样，在终端随时打开 Git 仓库并唤醒 Xylring：

```sh
# 打开当前目录的 Git 仓库
xylring .

# 打开指定目录的 Git 仓库
xylring /path/to/repository

# 同时打开多个仓库
xylring repoA repoB

# 仅激活并聚焦当前运行的 Xylring 窗口
xylring
```

### 安装命令行工具
- **GUI 方式**：在菜单栏选择「文件」→「在 PATH 中安装 'xylring' 命令行工具…」。
- **终端方式**：
  ```sh
  xylring install-cli
  ```

---

## 🗺️ 路线图 (Roadmap)

- [x] 高性能贝塞尔曲线提交图谱
- [x] 全能搜索（支持说明、作者、路径、引用及 SHA）
- [x] 块级（Hunk）审阅暂存与改动丢弃
- [x] 两点快照与三点（`...`）分叉点对比
- [x] 全文语法高亮快照阅读与图片 Diff
- [x] 终端 `xylring .` 联动
- [x] macOS (ARM64/x64) 与 Windows (x64) 原生打包
- [ ] 交互式变基（Interactive Rebase / Squash UI）
- [ ] Linux 桌面环境支持（X11 / Wayland）
- [ ] 更多主题配色（Solarized、Nord、Catppuccin）
- [ ] Vim 风格键盘导航

---

## 🔒 隐私与本地数据

Xylring 的诊断日志位于 macOS 的 `~/Library/Application Support/Xylring/logs/`
或 Windows 的 `%AppData%\Xylring\logs\`。普通日志 `xylring.log` 和崩溃日志
`crash.log` 可能包含本地仓库路径、操作错误和崩溃信息。日志约达到 1 MiB 时轮转，
并按 7 天的保留策略清理；正在使用的文件或清理失败的文件可能保留更久。
macOS 也可能生成独立的系统诊断报告。

最近仓库和会话状态保存在同一 `Xylring` 配置目录下的 `recent.json`、
`session.json` 中，其中可能包含仓库路径和未提交的提交说明草稿。应用没有实现
自动遥测或日志上传；用户主动获取、拉取或推送时，系统 Git 会使用已有配置连接远程仓库。
分享日志或会话文件前，请先检查其中的内容。

---

## 🛡️ 报告安全漏洞

请通过 [11802492+2fa0@users.noreply.github.com](mailto:11802492+2fa0@users.noreply.github.com) 私下报告潜在漏洞，
不要将漏洞细节直接发布到公开 Issue。请说明受影响版本、复现步骤和影响；
分享日志或示例仓库前，请移除凭据及其他敏感信息。维护者将尽力处理报告；
条件允许时，请先使用最新默认分支或最新已发布版本复现。

---

## 🤝 参与贡献

欢迎在 [Issue 页面](https://github.com/2fa0/xylring/issues)反馈普通问题、提出建议或提交 Pull Request；安全漏洞请按下文私下报告。

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feat/AmazingFeature`)
3. 提交改动 (`git commit -m 'feat: add amazing feature'`)
4. 推送分支 (`git push origin feat/AmazingFeature`)
5. 提交 Pull Request

---

## 📄 许可说明

Xylring 采用 [MIT 许可](LICENSE) 发布。

简而言之：你可以出于商业或非商业目的使用、复制、修改、合并、发布、分发、
再许可和/或销售本软件的副本，只需在所有副本或软件的实质性部分中保留版权声明
与许可声明。本软件按“现状”提供，不附带任何形式的担保。

第三方组件继续适用其各自的许可，详见
[第三方许可说明](cmd/gitclient/resources/THIRD_PARTY_NOTICES.txt)。
