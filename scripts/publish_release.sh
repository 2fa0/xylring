#!/bin/bash
# 一键上传 release/ 目录下的安装包到 GitHub Release
# 使用前提：安装并登录 GitHub CLI (brew install gh && gh auth login)
#
# 可选：不装 gh 也行，直接用浏览器在
# https://github.com/2fa0/xylring-release/releases/new 把 release/ 里全部文件
# 拖上去，记得勾 Set as the latest release。
#
# release/ 里除安装包外还有 update-*.json 和更新用的归档：它们一起上传，
# 已安装的旧版本才查得到新版本。Release 正文取自 CHANGELOG.md 的当前版本段。

set -e

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE_DIR="$ROOT/release"
# 安装包与更新文件都发在公开的发布仓库里；源码仓库是私有的，客户端只读这里。
RELEASE_REPO="2fa0/xylring-release"

TAG="${1:-}"
if [ -z "$TAG" ]; then
  version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["version"])' "$ROOT/cmd/gitclient/mygo.json")"
  TAG="v$version"
fi

NOTES_FILE="$(mktemp -t xylring-notes)"
trap 'rm -f "$NOTES_FILE"' EXIT
"$ROOT/scripts/release_notes.sh" "$TAG" > "$NOTES_FILE"

if ! command -v gh >/dev/null 2>&1; then
  echo "❌ 未检测到 GitHub CLI (gh)。"
  echo "建议运行：brew install gh && gh auth login"
  echo "或者在 GitHub 网页端手动创建 Release 并上传 release/ 目录下的所有文件。"
  exit 1
fi

echo "📦 正在为 ${TAG} 创建 GitHub Release 草稿..."
gh release create "$TAG" "$RELEASE_DIR"/* \
  --repo "$RELEASE_REPO" \
  --draft \
  --title "Xylring ${TAG}" \
  --notes-file "$NOTES_FILE"

echo "✅ 发布草稿已创建，请在 GitHub 仓库的 Releases 页面检查后再发布。"
