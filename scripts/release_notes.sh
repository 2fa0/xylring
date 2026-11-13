#!/bin/bash
# 从 CHANGELOG.md 抽出某个版本的段落。
#
# 更新窗口里的发布说明（go tool mygo build 读 CHANGELOG.md）和 GitHub Release
# 正文都从这里取，所以两边不会各写一份、各错一处。
#
# 用法：scripts/release_notes.sh <版本号，可带 v>
set -euo pipefail

if [ "$#" -ne 1 ] || [ -z "$1" ]; then
  echo "用法：$0 <版本号>" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NOTES="$ROOT/CHANGELOG.md"

if [ ! -f "$NOTES" ]; then
  echo "找不到 $NOTES" >&2
  exit 1
fi

awk -v want="${1#v}" '
  /^## / {
    if (inside) exit
    fields = substr($0, 4)
    split(fields, parts, " ")
    version = parts[1]
    gsub(/^\[/, "", version)
    sub(/\].*/, "", version)
    sub(/^v/, "", version)
    if (version == want) { inside = 1; found = 1 }
    next
  }
  inside {
    if (started || $0 !~ /^[[:space:]]*$/) { started = 1; print }
  }
  END {
    if (!found) {
      printf "CHANGELOG.md 里没有 %s 的段落\n", want > "/dev/stderr"
      exit 1
    }
  }
' "$NOTES"
