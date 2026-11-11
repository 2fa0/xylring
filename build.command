#!/bin/bash
# 双击本文件，打包可分发的安装包：
#   macOS Apple 芯片（arm64）磁盘镜像
#   macOS Intel 芯片（x86_64 / amd64）磁盘镜像
#   Windows 10 & 11 x64 安装程序与免安装压缩包
# 在 Mac 上交叉编译。未配置发行签名和公证。

set -u

export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:${PATH:-/usr/bin:/bin}"

ROOT="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="$ROOT/cmd/gitclient"
PACKAGES="$ROOT/release"

pause() {
  echo
  if [ -t 0 ]; then
    printf '按回车关闭… '
    read -r _
  fi
}

fail() {
  echo
  echo "打包失败：$1"
  pause
  exit 1
}

if [ "$(uname -s)" != "Darwin" ]; then
  fail "请在 Mac 上运行。磁盘镜像需要 macOS，Windows 包会在这里交叉编译。"
fi

if [ ! -f "$ROOT/go.mod" ] || [ ! -f "$APP_DIR/mygo.json" ]; then
  fail "找不到 cmd/gitclient/。请把 build.command 留在仓库根目录。"
fi

if ! command -v go >/dev/null 2>&1; then
  fail "没有找到 Go。需要 Go 1.27.3，并保证终端能运行 go version。"
fi

version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["version"])' "$APP_DIR/mygo.json" 2>/dev/null || true)"
if [ -z "$version" ]; then
  fail "无法读取 cmd/gitclient/mygo.json 里的 version。"
fi

have_nsis=0
if command -v makensis >/dev/null 2>&1; then
  have_nsis=1
fi

printf '\033]0;打包 Xylring\007'
echo "Xylring ${version}"
echo
echo "将打包："
echo "  • macOS Apple 芯片（arm64）磁盘镜像"
echo "  • macOS Intel 芯片（amd64 / x86_64）磁盘镜像"
if [ "$have_nsis" -eq 1 ]; then
  echo "  • Windows 10 & 11 x64 安装程序与免安装压缩包"
else
  echo "  • Windows 10 & 11 x64 免安装压缩包"
  echo
  echo "未找到 makensis，这次不会生成 Windows 安装程序。"
  echo "安装后重新双击本文件即可：brew install makensis"
fi
echo
echo "目标电脑需要已安装 Git。这些包没有发行签名和公证。"
echo

cd "$APP_DIR" || fail "无法进入 cmd/gitclient/。"

started=$(date +%s)
# makensis 在 LANG 为空或 C 时写安装包会崩溃（NSIS bug 1165）。
LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 go tool mygo build -platform darwin/arm64,darwin/amd64,windows/amd64 -skip-notarize
build_status=$?
echo

if [ "$build_status" -ne 0 ]; then
  fail "构建命令未成功（退出码 ${build_status}），未更新 release/。"
fi

staging="$(mktemp -d "${TMPDIR:-/tmp}/gitclient-packages.XXXXXX")"
cleanup() {
  rm -rf "$staging"
}
trap cleanup EXIT

found=0
note_windows_installer=0
note_windows_zip=0

# macOS ARM64
shopt -s nullglob
dmgs_arm=("$APP_DIR/build/darwin-arm64/"*.dmg)
shopt -u nullglob
if [ "${#dmgs_arm[@]}" -eq 1 ]; then
  cp -p "${dmgs_arm[0]}" "$staging/"
  found=1
elif [ "${#dmgs_arm[@]}" -gt 1 ]; then
  fail "darwin-arm64 里有多份磁盘镜像，无法判断该交付哪一份。"
fi

# macOS AMD64 (Intel)
shopt -s nullglob
dmgs_intel=("$APP_DIR/build/darwin-amd64/"*.dmg)
shopt -u nullglob
if [ "${#dmgs_intel[@]}" -eq 1 ]; then
  cp -p "${dmgs_intel[0]}" "$staging/"
  found=1
elif [ "${#dmgs_intel[@]}" -gt 1 ]; then
  fail "darwin-amd64 里有多份磁盘镜像，无法判断该交付哪一份。"
fi

# Windows AMD64 (Windows 10 & 11 x86_64)
win_dir="$APP_DIR/build/windows-amd64"
if [ -d "$win_dir" ]; then
  shopt -s nullglob
  setups=("$win_dir/"*[Ss]etup*.exe)
  shopt -u nullglob
  if [ "${#setups[@]}" -eq 1 ]; then
    cp -p "${setups[0]}" "$staging/"
    found=1
    note_windows_installer=1
  elif [ "${#setups[@]}" -gt 1 ]; then
    fail "windows-amd64 里有多份安装程序，无法判断该交付哪一份。"
  fi

  if [ -f "$win_dir/Xylring.exe" ]; then
    payload="$(mktemp -d "${TMPDIR:-/tmp}/gitclient-win.XXXXXX")"
    inner="$payload/Xylring"
    mkdir -p "$inner"
    for entry in "$win_dir"/*; do
      name="$(basename "$entry")"
      case "$name" in
        *[Ss]etup*.exe) continue ;;
      esac
      cp -R "$entry" "$inner/"
    done
    zip_name="xylring-${version}-windows-x64.zip"
    if ! ditto -c -k --norsrc "$inner" "$staging/$zip_name"; then
      rm -rf "$payload"
      fail "无法压缩 Windows 文件。"
    fi
    rm -rf "$payload"
    found=1
    note_windows_zip=1
  fi
fi

if [ "$found" -eq 0 ]; then
  fail "没有生成可交付的包。请查看上面的构建输出。"
fi

rm -rf "$PACKAGES"
mkdir -p "$PACKAGES" || fail "无法创建 release/。"
cp -p "$staging"/* "$PACKAGES/" || fail "无法把安装包放进 release/。"

elapsed=$(( $(date +%s) - started ))
echo "可交付的包在 release/，用时 ${elapsed} 秒："
echo
ls -lh "$PACKAGES"
echo
if [ "${#dmgs_arm[@]}" -eq 1 ]; then
  echo "macOS Apple 芯片（ARM64）：把 arm64 磁盘镜像发给 M1/M2/M3/M4 系列 Mac。对方打开后把 Xylring 拖进“应用程序”。"
fi
if [ "${#dmgs_intel[@]}" -eq 1 ]; then
  echo "macOS Intel 芯片（x86_64）：把 amd64 磁盘镜像发给 Intel 架构 Mac。对方打开后把 Xylring 拖进“应用程序”。"
fi
if [ "${#dmgs_arm[@]}" -gt 0 ] || [ "${#dmgs_intel[@]}" -gt 0 ]; then
  echo "注：未签名公证的应用首次打开时若被拦截，在访达里对应用按住 Control 点按并选择“打开”。"
fi
echo
if [ "$note_windows_installer" -eq 1 ]; then
  echo "Windows 10 & 11 x64 安装版：运行安装程序（*setup*.exe）。安装到当前用户，无需管理员权限。"
fi
if [ "$note_windows_zip" -eq 1 ]; then
  echo "Windows 10 & 11 x64 便携版：解压 zip 后直接双击运行 Xylring.exe。"
fi
if [ "$note_windows_installer" -eq 0 ] && [ "$note_windows_zip" -eq 0 ]; then
  echo "这次没有生成 Windows 包。"
fi

if [ -t 1 ]; then
  open "$PACKAGES"
fi
pause
