#!/bin/bash
# 双击本文件，打包可分发的安装包：
#   macOS Apple 芯片（arm64）磁盘镜像
#   macOS Intel 芯片（x86_64 / amd64）磁盘镜像
#   Windows 10 & 11 x64 安装程序与免安装压缩包
# 在 Mac 上交叉编译。未配置发行签名和公证。

set -u

export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:${PATH:-/usr/bin:/bin}"

ROOT="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="$ROOT/cmd/xylring-main"
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
  fail "找不到 cmd/xylring-main/。请把 build.command 留在仓库根目录。"
fi

if ! command -v go >/dev/null 2>&1; then
  fail "没有找到 Go。需要 Go 1.27.3，并保证终端能运行 go version。"
fi

version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["version"])' "$APP_DIR/mygo.json" 2>/dev/null || true)"
if [ -z "$version" ]; then
  fail "无法读取 cmd/xylring-main/mygo.json 里的 version。"
fi

# 更新包必须用私钥签名。没有密钥时 mygo build 只给一句警告，打出来的包看着
# 正常，实际不能让已安装的版本升级，所以在这里直接停下。
if [ -z "${MYGO_UPDATER_PRIVATE_KEY:-}" ]; then
  update_key="$HOME/Library/Application Support/mygo/update-keys/mygo-update.key"
  if [ ! -f "$update_key" ]; then
    fail "找不到更新签名密钥。先运行 cd cmd/xylring-main && go tool mygo keygen，或把密钥放进 MYGO_UPDATER_PRIVATE_KEY。"
  fi
  MYGO_UPDATER_PRIVATE_KEY="$(cat "$update_key")"
  export MYGO_UPDATER_PRIVATE_KEY
fi

if ! command -v makensis >/dev/null 2>&1; then
  fail "未找到 makensis，无法生成 Windows 安装程序。请先安装：brew install makensis"
fi

build_commit="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || true)"
if [ -z "$build_commit" ]; then
  fail "无法获取当前 Git 提交（HEAD）。请在有效的 Git 仓库中运行 build.command。"
fi
if [ -n "$(git -C "$ROOT" status --porcelain 2>/dev/null)" ]; then
  fail "工作区存在未提交改动，请先提交后再打包正式发布包（日常测试请用 build-dev.command）。"
fi
build_dirty="false"

printf '\033]0;打包 Xylring\007'
echo "Xylring ${version}"
echo
echo "将打包："
echo "  • macOS Apple 芯片（arm64）磁盘镜像"
echo "  • macOS Intel 芯片（amd64 / x86_64）磁盘镜像"
echo "  • Windows 10 & 11 x64 安装程序与免安装压缩包"
echo
echo "目标电脑需要已安装 Git。这些包没有发行签名和公证。"
echo

cd "$APP_DIR" || fail "无法进入 cmd/xylring-main/。"

started=$(date +%s)
# makensis 在 LANG 为空或 C 时写安装包会崩溃（NSIS bug 1165）。
LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 go tool mygo build -platform darwin/arm64,darwin/amd64,windows/amd64 -skip-notarize
build_status=$?
echo

if [ "$build_status" -ne 0 ]; then
  fail "构建命令未成功（退出码 ${build_status}），未更新 release/。"
fi

staging="$(mktemp -d "${ROOT}/.release-staging.XXXXXX")"
backup=""
cleanup() {
  [ -n "$staging" ] && [ -d "$staging" ] && rm -rf "$staging"
  [ -n "$backup" ] && [ -d "$backup" ] && rm -rf "$backup"
}
trap cleanup EXIT

note_windows_installer=0
note_windows_zip=0

# macOS ARM64
shopt -s nullglob
dmgs_arm=("$APP_DIR/build/darwin-arm64/"*.dmg)
shopt -u nullglob
if [ "${#dmgs_arm[@]}" -eq 1 ]; then
  cp -p "${dmgs_arm[0]}" "$staging/" || fail "无法复制 darwin-arm64 磁盘镜像。"
elif [ "${#dmgs_arm[@]}" -eq 0 ]; then
  fail "darwin-arm64 缺少磁盘镜像（.dmg）。"
else
  fail "darwin-arm64 里有多份磁盘镜像，无法判断该交付哪一份。"
fi

# macOS AMD64 (Intel)
shopt -s nullglob
dmgs_intel=("$APP_DIR/build/darwin-amd64/"*.dmg)
shopt -u nullglob
if [ "${#dmgs_intel[@]}" -eq 1 ]; then
  cp -p "${dmgs_intel[0]}" "$staging/" || fail "无法复制 darwin-amd64 磁盘镜像。"
elif [ "${#dmgs_intel[@]}" -eq 0 ]; then
  fail "darwin-amd64 缺少磁盘镜像（.dmg）。"
else
  fail "darwin-amd64 里有多份磁盘镜像，无法判断该交付哪一份。"
fi

# Windows AMD64 (Windows 10 & 11 x86_64)
win_dir="$APP_DIR/build/windows-amd64"
if [ ! -d "$win_dir" ]; then
  fail "缺少 windows-amd64 构建目录。"
fi
shopt -s nullglob
setups=("$win_dir/"*[Ss]etup*.exe)
shopt -u nullglob
if [ "${#setups[@]}" -eq 1 ]; then
  cp -p "${setups[0]}" "$staging/" || fail "无法复制 Windows 安装程序。"
  note_windows_installer=1
elif [ "${#setups[@]}" -gt 1 ]; then
  fail "windows-amd64 里有多份安装程序，无法判断该交付哪一份。"
else
  fail "windows-amd64 缺少安装程序（*setup*.exe）。"
fi

if [ ! -f "$win_dir/Xylring.exe" ]; then
  fail "windows-amd64 缺少 Xylring.exe，无法生成便携压缩包。"
fi
payload="$(mktemp -d "${TMPDIR:-/tmp}/xylring-win.XXXXXX")"
inner="$payload/Xylring"
mkdir -p "$inner" || { rm -rf "$payload"; fail "无法创建 Windows 便携包临时目录。"; }
shopt -s nullglob
for entry in "$win_dir"/*; do
  name="$(basename "$entry")"
  case "$name" in
    *[Ss]etup*.exe|update-*.json|*.tar.gz|*.delta) continue ;;
  esac
  if ! cp -R "$entry" "$inner/"; then
    rm -rf "$payload"
    fail "无法复制 Windows 文件：${name}"
  fi
done
shopt -u nullglob
zip_name="xylring-${version}-windows-x64.zip"
if ! ditto -c -k --norsrc "$inner" "$staging/$zip_name"; then
  rm -rf "$payload"
  fail "无法压缩 Windows 文件。"
fi
rm -rf "$payload"
note_windows_zip=1

# 每个平台的构建目录里必须同时具备更新清单、完整归档及对应增量包。
for target in darwin-arm64 darwin-amd64 windows-amd64; do
  dir="$APP_DIR/build/$target"
  [ -d "$dir" ] || fail "缺少 $target 构建目录。"
  manifest="$dir/update-${target}.json"
  [ -f "$manifest" ] || fail "$target 缺少更新清单 update-${target}.json。"
  shopt -s nullglob
  full_archives=("$dir"/*.tar.gz)
  deltas=("$dir"/*.delta)
  shopt -u nullglob
  if [ "${#full_archives[@]}" -eq 0 ]; then
    fail "$target 缺少完整更新归档（*.tar.gz）。"
  fi
  if ! cp -p "$manifest" "${full_archives[@]}" "$staging/"; then
    fail "无法复制 $target 的更新清单或完整归档。"
  fi
  if [ "${#deltas[@]}" -gt 0 ]; then
    if ! cp -p "${deltas[@]}" "$staging/"; then
      fail "无法复制 $target 的增量更新包。"
    fi
  fi
done

# 校验文件跟着包一起发；核对构建期间提交与工作区未变，并在替换 release/ 前逐项校验。
post_build_commit="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || true)"
if [ "$post_build_commit" != "$build_commit" ]; then
  fail "构建期间 Git HEAD 发生变化（${build_commit:0:7} -> ${post_build_commit:0:7}），未更新 release/。"
fi
if [ -n "$(git -C "$ROOT" status --porcelain 2>/dev/null)" ]; then
  fail "构建后工作区存在未提交改动，未更新 release/。"
fi
(cd "$staging" && shasum -a 256 * > checksums.txt) || fail "无法生成 checksums.txt。"
checksums_sha="$(shasum -a 256 "$staging/checksums.txt" | awk '{print $1}')"
python3 - "$staging/.build-meta.json" "$version" "$build_commit" "$build_dirty" "$checksums_sha" <<'PY' || fail "无法写入构建元数据。"
import json, sys
out, version, commit, dirty, checksums_sha = sys.argv[1:6]
with open(out, "w", encoding="utf-8") as f:
    json.dump({
        "version": version,
        "commit": commit,
        "dirty": dirty == "true",
        "checksums_sha256": checksums_sha,
    }, f, indent=2)
    f.write("\n")
PY
if ! (cd "$ROOT" && go run ./scripts/verifyrelease -dir "$staging" -config "$APP_DIR/mygo.json" -version "$version" -expected-commit "$build_commit" -require-win-installer=true); then
  fail "产物完整性校验未通过，未更新 release/。"
fi

# 先校验完成，再在同一文件系统原子替换 release/，避免替换失败时丢失上一版可用包。
if [ -e "$PACKAGES" ]; then
  backup="${ROOT}/.release-backup.$$"
  rm -rf "$backup"
  mv "$PACKAGES" "$backup" || fail "无法备份旧 release/ 目录，未更新产物。"
fi
if ! mv "$staging" "$PACKAGES"; then
  if [ -n "$backup" ] && [ -e "$backup" ]; then
    mv "$backup" "$PACKAGES" || true
    backup=""
  fi
  fail "无法将已校验产物替换到 release/。"
fi
staging=""
if [ -n "$backup" ] && [ -e "$backup" ]; then
  rm -rf "$backup"
  backup=""
fi

elapsed=$(( $(date +%s) - started ))
echo "可交付的包在 release/，用时 ${elapsed} 秒："
echo
ls -lh "$PACKAGES"
echo

echo "已安装的旧版本会读 update-*.json 自动升级；这些文件和安装包一起留在 Release 里。"
echo
echo "手工发布（浏览器上传）："
echo "  1. 打开 https://github.com/2fa0/xylring/releases/new，标签填 v${version}"
echo "  2. 把 release/ 里的全部文件拖上去：安装包、*.tar.gz、*.delta、update-*.json、checksums.txt"
echo "  3. 勾上 Set as the latest release；正文粘 CHANGELOG.md 里 ${version} 那一段"
echo "  4. 发完打开 https://github.com/2fa0/xylring/releases/latest/download/update-darwin-arm64.json 确认能下载"
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
