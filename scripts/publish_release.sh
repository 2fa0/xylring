#!/bin/bash
# 一键上传 release/ 目录下的安装包到 GitHub Release
# 使用前提：安装并登录 GitHub CLI (brew install gh && gh auth login)

set -e

TAG="${1:-v0.8.2}"
RELEASE_DIR="$(cd "$(dirname "$0")/../release" && pwd)"
NOTES_FILE="$(cd "$(dirname "$0")/.." && pwd)/RELEASE_NOTES.md"

if ! command -v gh >/dev/null 2>&1; then
  echo "❌ 未检测到 GitHub CLI (gh)。"
  echo "建议运行：brew install gh && gh auth login"
  echo "或者在 GitHub 网页端手动创建 Release 并上传 release/ 目录下的所有文件。"
  exit 1
fi

echo "📦 正在为 ${TAG} 创建 GitHub Release 草稿..."
gh release create "$TAG" "$RELEASE_DIR"/* \
  --draft \
  --title "Xylring ${TAG}" \
  --notes-file "$NOTES_FILE"

echo "✅ 发布草稿已创建，请在 GitHub 仓库的 Releases 页面检查后再发布。"
