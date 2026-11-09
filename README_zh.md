<p align="center">
  <img src="cmd/gitclient/resources/logo.png" alt="Xylring Logo" width="96" height="96" />
</p>

<h1 align="center">Xylring</h1>

<p align="center">
  <strong>基于 Go 开发的极速、轻量、现代化原生 Git 客户端。</strong><br>
  拒绝 Electron 的笨重与高内存消耗。带来瞬间冷启动、丝滑贝塞尔提交图谱与极致高效的 Diff 审阅体验。
</p>

<p align="center">
  <a href="#为什么要做-xylring">核心特性</a> •
  <a href="#主流客户端横向对比">竞品对比</a> •
  <a href="#下载与安装">下载安装</a> •
  <a href="#命令行-cli-使用">命令行</a> •
  <a href="#路线图-roadmap">路线图</a> •
  <a href="./README.md">English</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Release-v0.6.0-blue.svg?style=flat-square" alt="Release" />
  <img src="https://img.shields.io/badge/Go-1.27+-00ADD8.svg?style=flat-square&logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/平台-macOS%20%7C%20Windows-brightgreen.svg?style=flat-square" alt="Platforms" />
</p>

---

## 💡 为什么要做 Xylring？

日常开发中，Git 客户端总让人陷入两难：
- **Electron 系客户端**（如 GitKraken、VS Code 插件）：UI 虽丰富，但动辄几百 MB 内存，冷启动慢半拍；
- **顶尖原生客户端**（如 Sublime Merge、Fork）：手感极佳、性能优越，但均为闭源商业付费软件；
- **终端 TUI 工具**（如 Lazygit）：键盘流极快，但复杂多分支拓扑展示、并排富文本 Diff 和图片比对在终端中受限。

**Xylring** 的诞生就是为了填补这块空白：**兼具 Go 原生的高性能与低内存占用、现代美观的交互设计、秒级终端唤醒，项目采用非商业源码可用许可。**

---

## ⚡ 核心亮点

- 🚀 **毫秒级启动与极低内存占用**：纯 Go 原生渲染编译，秒级启动，初始内存仅约 50MB。彻底告别臃肿的 WebView 和 Chromium 内核。
- 🎨 **平滑三次贝塞尔提交图谱**：第一父链同色追踪，跨行连线平滑过渡；大仓库分页滚动时已渲染节点不抖动，合并节点空心区分。
- 🔍 **全能搜索与智能联想（Omni Search）**：一个搜索框覆盖提交信息、作者、文件路径、分支、标签与 SHA 哈希；支持输入前缀语法（如 `author:`、`path:`）与下拉建议。
- ⚖️ **快照与分叉点任意比对**：任意选择两个提交或分支比对，支持两点快照比较或三点（`...` 分叉点起）改动审查。
- 🧩 **块级交互式暂存（Hunk Staging）**：精细审阅单个文本改动块，支持块级暂存/取消暂存/丢弃改动，支持工作区提交与 Amend 上次提交。
- 💻 **深度契合终端工作流**：支持在终端像 VS Code 一样随时输入 `xylring .` 唤醒当前仓库，支持同时打开多仓库。
- 🖼️ **全量文件语法高亮与图片 Diff**：支持查看任意历史快照的完整只读文件，支持多格式图片并排对比。
- 🛡️ **本地隐私安全**：100% 离线可用，不收集任何日志或上报指标；直接沿用系统已有的 SSH 密钥与 Git 凭据配置。

---

## 📊 主流客户端横向对比

| 评测维度 | **Xylring** | **Sublime Merge** | **Fork** | **GitKraken** | **Lazygit** |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **许可状态** | **非商业使用** | ❌ 闭源 | ❌ 闭源 | ❌ 闭源 | ✅ 开源 |
| **技术栈** | **Go (原生渲染)** | C++ (自研原生) | Swift / WPF | Electron / Web | Go (TUI 终端) |
| **初始内存占用** | **~50 MB** | ~60 MB | ~100 MB | ~300 MB+ | ~20 MB |
| **冷启动耗时** | **< 0.2s** | < 0.3s | < 1.0s | 2~4s | < 0.1s |
| **终端唤起 (`.`)** | ✅ `xylring .` | ✅ `smerge .` | ✅ `fork .` | ⚠️ 较慢 | ✅ `lazygit` |
| **可视化图谱** | ✅ **贝塞尔曲线** | ✅ 平滑曲线 | ✅ 原生绘制 | ✅ Web SVG | ⚠️ ASCII 字符 |
| **块级暂存 (Hunk)**| ✅ **支持** | ✅ 支持 | ✅ 支持 | ✅ 支持 | ✅ 支持 |
| **商业使用价格** | **完全免费** | \$99 (买断) | \$49.99 (买断) | \$60+/年 (订阅) | 完全免费 |

---

## 📦 下载与安装

预编译安装包已发布在 [**GitHub Releases**](../../releases)：

### macOS 用户 (Apple Silicon / Intel)

1. 根据芯片架构下载对应磁盘镜像：
   - **Apple 芯片 (M1/M2/M3/M4 系列)**：`xylring-0.6.0-arm64.dmg`
   - **Intel 芯片 (x86_64)**：`xylring-0.6.0-amd64.dmg`
2. 双击打开 `.dmg`，将 **Xylring** 拖入「应用程序（Applications）」文件夹。

> [!NOTE]
> **macOS 首次打开安全提示**：  
> 如果发行版本未进行 Apple 开发者证书公证，初次打开若提示“无法验证开发者”，请在访达（Finder）中对 `Xylring.app` 按住 **Control** 点按并选择「打开」，或在终端执行：
> ```sh
> xattr -cr /Applications/Xylring.app
> ```

### Windows 用户 (Windows 10 & 11 x64)

- **安装版**：下载运行 `xylring-setup-0.6.0-amd64.exe`（安装到当前用户目录，无需管理员权限）。
- **便携版**：下载解压 `Xylring 0.6.0 windows-x64.zip`，直接双击 `Xylring.exe` 即可运行。

### 源码编译安装

环境要求：**Go 1.27+** 和系统 **Git**。

```sh
# 克隆仓库
git clone REPOSITORY_URL
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
- [x] 终端 `xylring .` 秒级联动
- [x] macOS (ARM64/x64) 与 Windows (x64) 原生打包
- [ ] 交互式变基（Interactive Rebase / Squash UI）
- [ ] Linux 桌面环境支持（X11 / Wayland）
- [ ] 更多主题配色（Solarized、Nord、Catppuccin）
- [ ] Vim 风格键盘导航

---

## 🤝 参与贡献

非常欢迎提交 Issue、提出新功能建议或 Pull Request！

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feat/AmazingFeature`)
3. 提交改动 (`git commit -m 'feat: add amazing feature'`)
4. 推送分支 (`git push origin feat/AmazingFeature`)
5. 提交 Pull Request

---

## 📄 许可说明

本项目采用非商业源码可用许可，详情见 [`LICENSE`](LICENSE)。
