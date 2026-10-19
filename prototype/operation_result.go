package main

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
)

var errStashChanged = errors.New("Stash 列表已发生变化，请刷新后重新选择")

var credentialURL = regexp.MustCompile(`(?i)(https?://)[^\s/@]+(?::[^\s/@]*)?@`)

func operationTitle(kind string) string {
	if kind == "Fetch" {
		return "获取"
	}
	return kind
}

func redactGitOutput(s string) string {
	return credentialURL.ReplaceAllString(s, "${1}[已隐藏凭据]@")
}

// Git's state files also work in linked worktrees via --git-path.
func pendingOperations(ctx context.Context, repo string) []string {
	var pending []string
	for _, item := range []struct{ marker, operation string }{{"MERGE_HEAD", "中止合并"}, {"CHERRY_PICK_HEAD", "中止 Cherry-pick"}, {"REVERT_HEAD", "中止 Revert"}} {
		path, err := runGit(ctx, repo, "rev-parse", "--path-format=absolute", "--git-path", item.marker)
		if err == nil {
			if _, err := os.Stat(strings.TrimSpace(string(path))); err == nil {
				pending = append(pending, item.operation)
			}
		}
	}
	return pending
}

func operationFailure(err, cancelled error, pending []string) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errStashChanged) {
		return err.Error()
	}
	if errors.Is(cancelled, context.Canceled) || errors.Is(err, context.Canceled) {
		return "已取消。已经完成的步骤不会回滚，请核对仓库状态后再操作。"
	}
	if errors.Is(cancelled, context.DeadlineExceeded) {
		return "操作超时。请检查连接与仓库状态后重新发起操作。"
	}
	if len(pending) > 0 {
		return "操作未完成，仓库仍有进行中的 Git 操作。请在终端或外部工具检查并解决冲突，或选择适用的中止操作。"
	}
	text := strings.ToLower(err.Error())
	for _, hint := range []string{"authentication failed", "permission denied (publickey", "could not read username", "invalid username", "http 401", "http 403", "authentication canceled"} {
		if strings.Contains(text, hint) {
			return "认证未通过。请检查凭据或 SSH 密钥，再重新发起操作。"
		}
	}
	for _, hint := range []string{"could not resolve", "unable to access", "connection refused", "connection timed out", "network is unreachable", "could not read from remote repository"} {
		if strings.Contains(text, hint) {
			return "无法连接远程仓库。请检查网络、远程地址和访问权限，再重新发起操作。"
		}
	}
	return "操作未完成。请查看 Git 原始输出和已完成步骤，核对仓库状态后再操作。"
}
