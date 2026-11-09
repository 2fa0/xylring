#!/bin/bash
# 一键上传 release/ 目录下的安装包到 GitHub Release
# 使用前提：安装并登录 GitHub CLI (brew install gh && gh auth login)

set -e

TAG="${1:-v0.6.0}"
RELEASE_DIR="$(cd "$(dirname "$0")/../release" && pwd)"
NOTES_FILE="$(cd "$(dirname "$0")/.." && pwd)/RELEASE_NOTES.md"

if ! command -v gh >/dev/null 2>&1; then
  echo "❌ 未检测到 GitHub CLI (gh)。"
  echo "建议运行：brew install gh && gh auth login"
  echo "或者在 GitHub 网页端手动创建 Release 并上传 release/ 目录下的所有文件。"
  exit 1
fi

echo "📦 正在发布 ${TAG} 到 GitHub Releases..."
gh release create "$TAG" "$RELEASE_DIR"/* \
  --title "Xylring ${TAG}" \
  --notes-file "$NOTES_FILE"

echo "✅ 发布完成！访问 GitHub 仓库的 Releases 页面即可查看。"
