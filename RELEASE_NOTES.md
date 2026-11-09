# Xylring v0.6.0 Release Notes

> **Xylring**: A blazing-fast, lightweight, and modern native Git client built with Go.  
> Release Date: 2026-11-09

---

## 🌟 Highlights in v0.6.0

- 🚀 **Lightning Fast Startup & Native Rendering**: Built with Go and native UI rendering, launching in milliseconds with ~50MB initial memory usage. Zero Electron overhead.
- 🎨 **Cubic Bezier Graph**: Beautiful commit graph with first-parent lane colors, jump-free rendering across pagination, and hollow nodes for merge commits.
- 🔍 **Omni Search & Instant Jump**: Single search box covering commit message, author, file path, branch, tag, and SHA with real-time autocompletion.
- ⚖️ **Deep Comparison & Hunk Staging**: Compare arbitrary snapshots or merge-bases (`...`), review and stage individual diff hunks or discard changes.
- ⚡ **Terminal CLI Integration**: Open any repository instantly with `xylring .` or `xylring /path/to/repo` right from your terminal.
- 🖼️ **Rich File Viewers & Image Diff**: Full-file read-only preview with syntax highlighting and side-by-side image comparison.
- 🔄 **Safe Auto-Sync & Session Restore**: Automatically track remote updates with silent background fetch; restore open repositories, commit drafts, and amend states across restarts.

---

## 📦 Downloads & Verification

| Platform | Architecture | Package | SHA256 Checksum |
| :--- | :--- | :--- | :--- |
| **macOS** | Apple Silicon (M1/M2/M3/M4) | [`xylring-0.6.0-arm64.dmg`](../../releases/download/v0.6.0/xylring-0.6.0-arm64.dmg) | `56145ee0c0c681ab8fa66da69094feea5d47dc22ae53cb103ee8f70bb38cca47` |
| **macOS** | Intel x86_64 | [`xylring-0.6.0-amd64.dmg`](../../releases/download/v0.6.0/xylring-0.6.0-amd64.dmg) | `c6fca4d6cf508b6517044183fe1a8297da2488817f10900d8c3a3a56e189f015` |
| **Windows** | x64 (Installer) | [`xylring-setup-0.6.0-amd64.exe`](../../releases/download/v0.6.0/xylring-setup-0.6.0-amd64.exe) | `30ad74dba2b7a27476b3d0cc8b7912a4329572fa1f419d0ae7cef612978f5114` |
| **Windows** | x64 (Portable Zip) | [`Xylring 0.6.0 windows-x64.zip`](../../releases/download/v0.6.0/Xylring%200.6.0%20windows-x64.zip) | `745bc97af37f33d12796389e259ac9d32dd09c792c3009f226070809ca5ba071` |

---

## 💻 Installation & Security Notes

### macOS
1. Download the `.dmg` matching your chip (Apple Silicon `arm64` or Intel `amd64`).
2. Open the disk image and drag **Xylring.app** to your **Applications** folder.
3. **Gatekeeper Notice**: Because this build is unsigned/notarized by Apple:
   - On first launch, if macOS says *"Xylring cannot be opened because it is from an unidentified developer"*, right-click (or Control-click) `Xylring.app` in Finder and select **Open** → Click **Open** again.
   - Or run in Terminal:
     ```sh
     xattr -cr /Applications/Xylring.app
     ```

### Windows (10 & 11 x64)
- **Installer**: Run `xylring-setup-0.6.0-amd64.exe`. It installs to your user local folder without requiring administrator privileges.
- **Portable**: Extract `Xylring 0.6.0 windows-x64.zip` and run `Xylring.exe` directly.
- **SmartScreen Notice**: If Windows Defender SmartScreen prompts *"Windows protected your PC"*, click **More info** → **Run anyway**.

---

## 🛠️ CLI Setup
After starting Xylring:
- From the menu bar, choose **File** → **Install 'xylring' Command Line Tool in PATH...**, or run:
  ```sh
  xylring install-cli
  ```
- Then use `xylring .` anywhere in your terminal!
