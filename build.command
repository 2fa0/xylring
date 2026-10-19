#!/bin/bash
# 双击本文件，打包两份可分发的安装包：
#   macOS Apple 芯片（arm64）磁盘镜像
#   Windows 11 x64 安装程序
# 在 Intel Mac 上也可以交叉编译这两份。未配置发行签名和公证。

set -u

export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:${PATH:-/usr/bin:/bin}"

ROOT="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="$ROOT/prototype"
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

if [ ! -f "$APP_DIR/go.mod" ] || [ ! -f "$APP_DIR/mygo.json" ]; then
  fail "找不到 prototype/。请把 build.command 留在仓库根目录。"
fi

if ! command -v go >/dev/null 2>&1; then
  fail "没有找到 Go。需要 Go 1.27.2，并保证终端能运行 go version。"
fi

version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["version"])' "$APP_DIR/mygo.json" 2>/dev/null || true)"
if [ -z "$version" ]; then
  fail "无法读取 prototype/mygo.json 里的 version。"
fi

have_nsis=0
if command -v makensis >/dev/null 2>&1; then
  have_nsis=1
fi

printf '\033]0;打包 Git Client\007'
echo "Git Client ${version}"
echo
echo "将打包："
echo "  • macOS Apple 芯片（arm64）磁盘镜像"
if [ "$have_nsis" -eq 1 ]; then
  echo "  • Windows 11 x64 安装程序"
else
  echo "  • Windows 11 x64 压缩包"
  echo
  echo "未找到 makensis，这次不会生成 Windows 安装程序。"
  echo "安装后重新双击本文件即可：brew install makensis"
fi
echo
echo "目标电脑需要已安装 Git。这两份都没有发行签名。"
echo

cd "$APP_DIR" || fail "无法进入 prototype/。"

started=$(date +%s)
# makensis 在 LANG 为空或 C 时写安装包会崩溃（NSIS bug 1165）。
LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 go tool mygo build -platform windows/amd64,darwin/arm64 -skip-notarize
build_status=$?
echo

if [ "$build_status" -ne 0 ]; then
  echo "构建命令未成功（退出码 ${build_status}）。若下面仍列出了包，那是已完成的部分。"
  echo
fi

staging="$(mktemp -d "${TMPDIR:-/tmp}/gitclient-packages.XXXXXX")"
cleanup() {
  rm -rf "$staging"
}
trap cleanup EXIT

found=0
note_windows=""

shopt -s nullglob
dmgs=("$APP_DIR/build/darwin-arm64/"*.dmg)
shopt -u nullglob
if [ "${#dmgs[@]}" -eq 1 ]; then
  cp -p "${dmgs[0]}" "$staging/"
  found=1
elif [ "${#dmgs[@]}" -gt 1 ]; then
  fail "darwin-arm64 里有多份磁盘镜像，无法判断该交付哪一份。"
fi

win_dir="$APP_DIR/build/windows-amd64"
if [ -d "$win_dir" ]; then
  shopt -s nullglob
  setups=("$win_dir/"*"Setup"*.exe)
  shopt -u nullglob
  if [ "${#setups[@]}" -eq 1 ]; then
    cp -p "${setups[0]}" "$staging/"
    found=1
    note_windows="installer"
  elif [ "${#setups[@]}" -gt 1 ]; then
    fail "windows-amd64 里有多份安装程序，无法判断该交付哪一份。"
  elif [ -f "$win_dir/Git Client.exe" ]; then
    payload="$(mktemp -d "${TMPDIR:-/tmp}/gitclient-win.XXXXXX")"
    inner="$payload/Git Client"
    mkdir -p "$inner"
    for entry in "$win_dir"/*; do
      name="$(basename "$entry")"
      case "$name" in
        *Setup*.exe) continue ;;
      esac
      cp -R "$entry" "$inner/"
    done
    zip_name="Git Client ${version} windows-x64.zip"
    if ! ditto -c -k --norsrc "$inner" "$staging/$zip_name"; then
      rm -rf "$payload"
      fail "无法压缩 Windows 文件。"
    fi
    rm -rf "$payload"
    found=1
    note_windows="zip"
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
if [ "${#dmgs[@]}" -eq 1 ]; then
  echo "macOS：把磁盘镜像发给 Apple 芯片的 Mac。对方打开后把 Git Client 拖进“应用程序”。"
  echo "其他 Mac 第一次可能会被系统拦截：在访达里对应用按住 Control 点按，选择“打开”。"
else
  echo "这次没有生成 macOS 磁盘镜像。"
fi
echo
if [ "$note_windows" = "installer" ]; then
  echo "Windows 11 x64：运行安装程序。它安装到当前用户，不需要管理员权限。"
  echo "第一次可能出现 SmartScreen 提示，选择“仍要运行”。"
elif [ "$note_windows" = "zip" ]; then
  echo "Windows 11 x64：解压后运行 Git Client.exe。这次没有安装程序。"
  echo "装好 makensis 后重新双击本文件，即可改打安装包：brew install makensis"
else
  echo "这次没有生成 Windows 包。"
fi
if [ "$build_status" -ne 0 ]; then
  echo
  echo "构建没有完整成功，请先看上面的错误，再把已生成的包发出去。"
fi

if [ -t 1 ]; then
  open "$PACKAGES"
fi
pause
if [ "$build_status" -ne 0 ]; then
  exit "$build_status"
fi
