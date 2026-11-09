<p align="center">
  <img src="cmd/gitclient/resources/logo.png" alt="Xylring Logo" width="96" height="96" />
</p>

<h1 align="center">Xylring</h1>

<p align="center">
  <strong>A blazing fast, lightweight, and native Git client built with Go.</strong><br>
  No Electron overhead. No bloated WebViews. Just pure speed, fluid graph rendering, and effortless diff reviewing.
</p>

<p align="center">
  <a href="#why-xylring">Features</a> •
  <a href="#comparison">Comparison</a> •
  <a href="#installation">Installation</a> •
  <a href="#cli-usage">CLI Usage</a> •
  <a href="#roadmap">Roadmap</a> •
  <a href="./README_zh.md">简体中文</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Release-v0.8.0-blue.svg?style=flat-square" alt="Release" />
  <img src="https://img.shields.io/badge/Go-1.27+-00ADD8.svg?style=flat-square&logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/Platform-macOS%20%7C%20Windows-brightgreen.svg?style=flat-square" alt="Platforms" />
</p>

---

## 💡 Why Xylring?

Most developers face an awkward dilemma with Git clients:
- **Electron-based clients** (GitKraken, VS Code GitLens) offer rich UI but consume hundreds of megabytes of RAM and feel sluggish to cold-start.
- **Top-tier native clients** (Sublime Merge, Fork) offer exceptional performance, but are closed-source and paid commercial tools.
- **Terminal TUIs** (Lazygit) are blazingly fast, but limited when inspecting multi-branch branch topology, side-by-side rich diffs, or images.

**Xylring** is designed to bridge this gap: **Native Go performance, aesthetic modern UI, instant CLI invocation, under its noncommercial source-available license.**

---

## ⚡ Highlights

- 🚀 **Sub-second Startup & Minimal Footprint**: Built with Go and native rendering. Launches in milliseconds with ~50MB RAM usage. Zero Webview / Electron baggage.
- 🎨 **Cubic Bezier Graph**: Smooth branch curves tracking the first-parent lane. View the whole repository history without jitter or stutter across pagination.
- 🔍 **Omni Search with Autocomplete**: A single search box covering commit messages, code changes (Pickaxe `-S`), author names, file paths, branches, tags, and commit hashes. Supports prefixes like `code:`, `author:`, and `path:`. Built-in in-diff find (⌘F / Ctrl+F) with match highlights.
- ⚖️ **Arbitrary Snapshots & Merge-Base Comparison**: Compare any two commits, branches, or tags. Supports two-dot snapshot diffing or three-dot (`...`) branch fork diffing.
- 🧩 **Hunk-Level Interactive Staging**: Review and stage individual patch hunks, discard changes, or amend previous commits with a single click.
- 💻 **Terminal-First Workflow**: Launch any repository right from your terminal with `xylring .`, `xylring /path/to/repo`, or switch between multiple repositories effortlessly.
- 🖼️ **Full-File Source View & Image Diff**: Read full-file snapshots with syntax highlighting and inspect visual image diffs side-by-side.
- 🛡️ **Local & Privacy First**: 100% offline-ready. Never uploads logs, metrics, or credentials. Uses your existing system Git and SSH config directly.

---

## 📊 Comparison with Popular Git Clients

| Dimension | **Xylring** | **Sublime Merge** | **Fork** | **GitKraken** | **Lazygit** |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **License** | **Noncommercial** | ❌ No | ❌ No | ❌ No | ✅ Yes |
| **Tech Stack** | **Go (Native)** | C++ (Custom) | Swift / WPF | Electron / Web | Go (TUI) |
| **Initial Memory** | **~50 MB** | ~60 MB | ~100 MB | ~300 MB+ | ~20 MB |
| **Cold Startup** | **< 0.2s** | < 0.3s | < 1.0s | 2-4s | < 0.1s |
| **CLI Wakeup (`.`)** | ✅ `xylring .` | ✅ `smerge .` | ✅ `fork .` | ⚠️ Limited | ✅ `lazygit` |
| **Visual Graph** | ✅ **Bezier Lanes** | ✅ Smooth Lanes | ✅ Native | ✅ Web SVG | ⚠️ Text ASCII |
| **Hunk / Patch Staging** | ✅ **Yes** | ✅ Yes | ✅ Yes | ✅ Yes | ✅ Yes |
| **Price / License** | **Noncommercial** | \$99 (Commercial) | \$49.99 (Commercial)| \$60+/yr (Subscription) | Free |

---

## 📦 Installation

Pre-built binaries are available on the [**GitHub Releases**](../../releases) page for **macOS** and **Windows**:

### macOS (Apple Silicon & Intel)

1. Download the `.dmg` matching your architecture:
   - **Apple Silicon (M1/M2/M3/M4)**: `xylring-0.8.0-arm64.dmg`
   - **Intel x86_64**: `xylring-0.8.0-amd64.dmg`
2. Open the disk image and drag **Xylring** into your `Applications` folder.

> [!NOTE]
> **First-time launch on macOS**:  
> Since releases are currently self-distributed without Apple notarization, right-click (or Control-click) `Xylring.app` in Finder and select **Open** on the first launch. Or run:
> ```sh
> xattr -cr /Applications/Xylring.app
> ```

### Windows (10 & 11 x64)

- **Installer**: Download and run `xylring-setup-0.8.0-amd64.exe` (installs to user directory, no admin rights required).
- **Portable**: Download and extract `Xylring 0.8.0 windows-x64.zip`, then run `Xylring.exe`.

### Build from Source

Requirements: **Go 1.27+** and system **Git**.

```sh
# Clone the repository
git clone REPOSITORY_URL
cd xylring

# Run directly
go run ./cmd/gitclient /path/to/your/repo

# Or build native macOS .app
cd cmd/gitclient
go tool mygo build -skip-dmg -skip-notarize
```

---

## 🚀 CLI Usage

Seamlessly inspect any Git repository directly from your terminal, just like `code .`:

```sh
# Open repository in current directory
xylring .

# Open specific repository
xylring /path/to/project

# Open multiple repositories side-by-side
xylring repoA repoB

# Just focus/activate the running Xylring window
xylring
```

### Install CLI Helper in PATH
- **From GUI Menu**: Select `File` → `Install 'xylring' Command Line Tool in PATH...`
- **From Terminal**:
  ```sh
  xylring install-cli
  ```

---

## 🗺️ Roadmap

- [x] High-performance commit graph with cubic Bezier lanes
- [x] Omni-search across messages, authors, files, refs, and SHAs
- [x] Hunk-level staging and interactive patch discard
- [x] Dual-point and triple-point (`...`) commit comparisons
- [x] Syntax-highlighted full-file viewer and image diffing
- [x] CLI `xylring .` terminal integration
- [x] Cross-platform support for macOS (ARM64/x64) and Windows (x64)
- [ ] Interactive Rebase UI (Squash, Reorder, Drop)
- [ ] Linux desktop support (X11 / Wayland)
- [ ] Custom themes (Solarized, Nord, Catppuccin)
- [ ] Vim-style keyboard navigation

---

## 🤝 Contributing

Contributions, issues, and feature requests are welcome!  
Feel free to check out the [issues page](../../issues).

1. Fork the Project
2. Create your Feature Branch (`git checkout -b feat/AmazingFeature`)
3. Commit your Changes (`git commit -m 'feat: add some amazing feature'`)
4. Push to the Branch (`git push origin feat/AmazingFeature`)
5. Open a Pull Request

---

## 📄 License

Xylring is distributed under its noncommercial source-available license; see [`LICENSE`](LICENSE).
