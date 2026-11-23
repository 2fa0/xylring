#!/bin/bash
# 双击本文件，快速构建 macOS Apple 芯片（arm64）与 Windows 10 & 11 x64 测试版
# （仅构建 darwin/arm64 与 windows/amd64，跳过 DMG 镜像、NSIS 安装包、
# 公证、更新签名与增量包），输出到 release-dev/。

set -u

export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:${PATH:-/usr/bin:/bin}"

ROOT="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="$ROOT/cmd/xylring-main"
OUT="$ROOT/release-dev"

pause() {
  echo
  if [ -t 0 ]; then
    printf '按回车关闭… '
    read -r _
  fi
}

fail() {
  echo
  echo "构建失败：$1"
  pause
  exit 1
}

if [ ! -f "$ROOT/go.mod" ] || [ ! -f "$APP_DIR/mygo.json" ]; then
  fail "找不到 cmd/xylring-main/。请把 build-dev.command 留在仓库根目录。"
fi

if ! command -v go >/dev/null 2>&1; then
  fail "没有找到 Go。需要 Go 1.27.x，并保证终端能运行 go version。"
fi

version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["version"])' "$APP_DIR/mygo.json" 2>/dev/null || true)"
if [ -z "$version" ]; then
  version="dev"
fi

printf '\033]0;快速构建 Xylring (Dev)\007'
echo "Xylring ${version} (macOS arm64 + Windows x64 快速测试包)"
echo "仅编译 darwin/arm64 与 windows/amd64，跳过 DMG、NSIS 安装包、公证与更新包签名。"
echo

cd "$APP_DIR" || fail "无法进入 cmd/xylring-main/。"

started=$(date +%s)
# 不传 MYGO_UPDATER_PRIVATE_KEY，避免生成更新归档与 delta 增量包耗时。
env -u MYGO_UPDATER_PRIVATE_KEY \
  go tool mygo build -platform darwin/arm64,windows/amd64 -skip-installer -skip-dmg -skip-notarize
build_status=$?
echo

if [ "$build_status" -ne 0 ]; then
  fail "构建命令未成功（退出码 ${build_status}）。"
fi

mac_app="$APP_DIR/build/darwin-arm64/Xylring.app"
if [ ! -d "$mac_app" ]; then
  fail "未找到构建产物：$mac_app"
fi

win_dir="$APP_DIR/build/windows-amd64"
if [ ! -f "$win_dir/Xylring.exe" ]; then
  fail "未找到构建产物：$win_dir/Xylring.exe"
fi

staging="$(mktemp -d "${TMPDIR:-/tmp}/xylring-dev.XXXXXX")"
cleanup() {
  rm -rf "$staging"
}
trap cleanup EXIT

if command -v ditto >/dev/null 2>&1; then
  ditto "$mac_app" "$staging/Xylring.app" || fail "无法复制 macOS 应用包。"
else
  cp -R "$mac_app" "$staging/Xylring.app" || fail "无法复制 macOS 应用包。"
fi

payload="$staging/Xylring"
mkdir -p "$payload"
for entry in "$win_dir"/*; do
  name="$(basename "$entry")"
  case "$name" in
    *[Ss]etup*.exe|*.tar.gz|*.delta|update-*.json) continue ;;
  esac
  cp -R "$entry" "$payload/"
done

zip_name="xylring-${version}-dev-windows-x64.zip"
if command -v ditto >/dev/null 2>&1; then
  ditto -c -k --norsrc "$payload" "$staging/$zip_name" || fail "无法压缩 Windows 测试包。"
else
  (cd "$staging" && zip -qr "$zip_name" "Xylring") || fail "无法压缩 Windows 测试包。"
fi

rm -rf "$OUT"
mkdir -p "$OUT" || fail "无法创建 release-dev/。"
if command -v ditto >/dev/null 2>&1; then
  ditto "$staging/Xylring.app" "$OUT/Xylring.app" || fail "无法复制 macOS 应用包到 release-dev/。"
else
  cp -R "$staging/Xylring.app" "$OUT/Xylring.app" || fail "无法复制 macOS 应用包到 release-dev/。"
fi
cp -R "$payload" "$OUT/Xylring" || fail "无法复制 Windows 可执行目录到 release-dev/。"
cp -p "$staging/$zip_name" "$OUT/" || fail "无法复制 Windows 压缩包到 release-dev/。"

elapsed=$(( $(date +%s) - started ))
echo "快速测试包已生成在 release-dev/，用时 ${elapsed} 秒："
echo
ls -lh "$OUT"
echo
echo "macOS (arm64)：直接运行 release-dev/Xylring.app"
echo "Windows (x64)：将 release-dev/Xylring/Xylring.exe（或 ${zip_name}）传到 Windows 解压双击运行。"

if [ -t 1 ] && command -v open >/dev/null 2>&1; then
  open "$OUT"
fi
pause
