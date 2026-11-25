#!/bin/bash
# 一键上传 release/ 目录下的安装包到 GitHub Release
# 使用前提：安装并登录 GitHub CLI (brew install gh && gh auth login)
#
# 可选：不装 gh 也行，直接用浏览器在
# https://github.com/2fa0/xylring/releases/new 把 release/ 里全部文件
# 拖上去，记得勾 Set as the latest release。
#
# release/ 里除安装包外还有 update-*.json 和更新用的归档：它们一起上传，
# 已安装的旧版本才查得到新版本。Release 正文取自 CHANGELOG.md 的当前版本段。

set -e

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE_DIR="$ROOT/release"
# 安装包与更新文件跟源码一起发在同一个公开仓库：客户端只读这里的 Releases。
RELEASE_REPO="2fa0/xylring"

version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["version"])' "$ROOT/cmd/xylring-main/mygo.json")"
expected_tag="v${version}"
TAG="${1:-$expected_tag}"
if [ "$TAG" != "$expected_tag" ]; then
  echo "❌ 发布标签 ${TAG} 与 cmd/xylring-main/mygo.json 中的版本 ${version}（预期 ${expected_tag}）不一致。"
  exit 1
fi

if [ -n "$(git -C "$ROOT" status --porcelain)" ]; then
  echo "❌ 工作区存在未提交改动，请先提交并从最终候选提交重新构建 release/。"
  exit 1
fi

if ! git -C "$ROOT" rev-parse --verify --quiet "refs/tags/${TAG}^{commit}" >/dev/null; then
  echo "❌ 本地不存在标签 ${TAG}。请先为当前提交打上标签并重建 release/。"
  exit 1
fi

tag_commit="$(git -C "$ROOT" rev-parse "${TAG}^{commit}")"
head_commit="$(git -C "$ROOT" rev-parse HEAD)"
if [ "$tag_commit" != "$head_commit" ]; then
  echo "❌ 标签 ${TAG} 指向 ${tag_commit:0:7}，与当前 HEAD（${head_commit:0:7}）不一致。"
  exit 1
fi

echo "🔍 正在核对远端标签 ${TAG}..."
remote_refs="$(git -C "$ROOT" ls-remote --tags origin "refs/tags/${TAG}" "refs/tags/${TAG}^{}")"
remote_tag_commit="$(printf '%s\n' "$remote_refs" | awk -v t="refs/tags/${TAG}^{}" '$2 == t {print $1}')"
if [ -z "$remote_tag_commit" ]; then
  remote_tag_commit="$(printf '%s\n' "$remote_refs" | awk -v t="refs/tags/${TAG}" '$2 == t {print $1}')"
fi
if [ -z "$remote_tag_commit" ]; then
  echo "❌ 远端 origin 不存在标签 ${TAG}，请先推送标签后再发布。"
  exit 1
fi
if [ "$remote_tag_commit" != "$head_commit" ]; then
  echo "❌ 远端标签 ${TAG} 指向 ${remote_tag_commit:0:7}，与当前 HEAD（${head_commit:0:7}）不一致。"
  exit 1
fi

if [ ! -d "$RELEASE_DIR" ]; then
  echo "❌ 找不到 release/ 目录，请先运行 build.command 构建产物。"
  exit 1
fi

echo "🔍 正在校验 ${RELEASE_DIR} 中的产物、清单、构建提交、校验和与签名..."
(cd "$ROOT" && go run ./scripts/verifyrelease -dir "$RELEASE_DIR" -config "$ROOT/cmd/xylring-main/mygo.json" -version "$version" -expected-commit "$head_commit" -require-win-installer=true)

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
  --verify-tag \
  --draft \
  --title "Xylring ${TAG}" \
  --notes-file "$NOTES_FILE"

echo "✅ 发布草稿已创建，请在 GitHub 仓库的 Releases 页面检查后再发布。"
