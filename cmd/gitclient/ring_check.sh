#!/bin/sh
# xylring-strict-hook v1

guidance() {
    cat >&2 <<'EOF'

年轮检查失败，提交已拒绝。请修改提交说明后正常重试，不要使用 --no-verify，
也不要修改 Hook 或 core.hooksPath 绕过检查。

AI 参与了本次工作：
1. 将下面的 <实际Agent产品名> 替换成真实产品名，例如 Cindy、Claude Code、Codex。
2. 必须填写真实的 AI-Model；不知道时先确认模型，不能省略或猜测。
3. 正文和末尾的年轮字段之间必须有一个空行，每个字段只写一次。

提交说明模板（必须替换尖括号中的内容）：
<本次改动说明>

AI-Agent: <实际Agent产品名>
AI-Model: <实际模型名>

重试命令（保留原来的提交选项，例如 --amend；先替换占位内容）：
git commit -m '<本次改动说明>' --trailer 'AI-Agent: <实际Agent产品名>' --trailer 'AI-Model: <实际模型名>'
不要在已有年轮字段的消息上重复追加；应先修正或删除原来的重复字段。

仅当本次工作完全由人工完成时，才使用：
git commit -m '<本次改动说明>' --trailer 'AI-Agent: human'
人工声明不能同时带 AI-Model、AI-Attribution 或 AI 原生签名。
AI 参与的提交绝不能填写 human 来通过检查。

提交后运行 git log -1 --format=%B，确认最终提交保留了正确的年轮字段。
EOF
}

validate() {
    printf '%s\n' "$1" | awk '
    function fail(message) { print "年轮错误：" message; bad = 1 }
    {
        colon = index($0, ":")
        if (!colon) next
        key = tolower(substr($0, 1, colon - 1))
        value = substr($0, colon + 1)
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
        if (key == "assisted-by" && value == "Cindy (https://cindy.app)") native = 1
        if (key != "ai-agent" && key != "ai-model" && key != "ai-attribution") next
        count[key]++
        values[key] = value
        if (count[key] > 1) fail(key " 重复；每个字段只能出现一次。")
        if (value == "") fail(key " 不能为空；AI 提交必须填写真实模型名。")
        if (value ~ /[<>]/ || tolower(value) ~ /^(unknown|unspecified|tbd|todo|n\/a|your agent\/product name|your exact model name)$/ || value ~ /^(实际Agent名称|实际Agent产品名|确知的实际模型名|实际模型名)$/)
            fail(key " 仍是占位值，请填写真实信息；模型未知时先确认。")
    }
    END {
        if (count["ai-agent"] != 1) fail("必须在提交说明末尾声明唯一的 AI-Agent；Assisted-by 不能代替它。")
        if (count["ai-agent"] == 1 && tolower(values["ai-agent"]) != "human" && count["ai-model"] != 1)
            fail("AI 提交必须声明唯一的非空 AI-Model，请填写实际模型名。")
        if (count["ai-attribution"] && values["ai-attribution"] != "stamp" && values["ai-attribution"] != "inferred")
            fail("AI-Attribution 只允许 stamp 或 inferred；显式声明时省略。")
        if (tolower(values["ai-agent"]) == "human" && (count["ai-model"] || count["ai-attribution"] || native))
            fail("人工声明不能同时带模型、AI 归因来源或 AI 原生签名；AI 参与时请填真实 Agent。")
        exit bad ? 1 : 0
    }' >&2
}

case "${1:-}" in
    --range)
        [ "$#" = 2 ] || { echo '用法：sh ring_check.sh --range BASE..HEAD' >&2; exit 1; }
        commits=$(git rev-list --reverse "$2" --) || exit 1
        result=0
        for commit in $commits; do
            message=$(git show -s --format=%B "$commit") || exit 1
            trailers=$(printf '%s\n' "$message" | git interpret-trailers --parse) || exit 1
            if ! validate "$trailers"; then
                echo "不合规提交：$commit" >&2
                result=1
            fi
        done
        [ "$result" = 0 ] || guidance
        exit "$result"
        ;;
    '') echo '用法：sh ring_check.sh COMMIT_EDITMSG 或 --range BASE..HEAD' >&2; exit 1 ;;
esac

# Preserve user hooks and validate their final message, rather than overwriting it.
orig="$0.xylring-orig"
if [ -x "$orig" ]; then
    "$orig" "$@" || exit $?
fi
trailers=$(git interpret-trailers --parse "$1") || { guidance; exit 1; }
if ! validate "$trailers"; then
    guidance
    exit 1
fi
