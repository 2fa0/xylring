package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Operation targets are resolved before confirmation so moving refs cannot change them.
type repoOperation struct {
	remoteURLs                                                                                    map[string]string
	ready                                                                                         bool
	expectedBranch, expectedHead                                                                  string
	stashChoice, hunkChoice                                                                       string
	action, repo, target, path, name, value, remote, output, err, hub, newRemote, newURL, refName string
	includeUntracked, global, confirmed                                                           bool
	patch                                                                                         string
	hunks                                                                                         []patchHunk
	hunk                                                                                          int
	busy                                                                                          bool
	gen                                                                                           int
	remotes                                                                                       []string
	stashes                                                                                       []stashEntry
	stash                                                                                         int
	state                                                                                         string
}
type stashEntry struct{ hash, label string }

func operationDisplayName(op string) string {
	switch op {
	case "rebase":
		return "变基"
	case "merge":
		return "合并"
	case "cherry-pick":
		return "拣选"
	case "revert":
		return "撤销"
	default:
		return op
	}
}
func operationState(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	b, err := runGit(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	gitDir := strings.TrimSpace(string(b))
	for _, p := range []struct{ file, op string }{{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"MERGE_HEAD", "merge"}, {"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}} {
		path := filepath.Join(gitDir, p.file)
		if _, err = os.Stat(path); err == nil {
			return p.op, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}
func listStashes(ctx context.Context, dir string) ([]stashEntry, error) {
	b, e := runGit(ctx, dir, "stash", "list", "--format=%H%x00%gd: %s")
	if e != nil {
		return nil, e
	}
	var entries []stashEntry
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		h, label, ok := strings.Cut(line, "\x00")
		if ok {
			entries = append(entries, stashEntry{h, label})
		}
	}
	return entries, nil
}
func stashRef(ctx context.Context, dir, hash string) (string, error) {
	entries, e := listStashes(ctx, dir)
	if e != nil {
		return "", e
	}
	for i, s := range entries {
		if s.hash == hash {
			return fmt.Sprintf("stash@{%d}", i), nil
		}
	}
	return "", errors.New("所选 Stash 已变化，请重新打开列表")
}
func resolveOperationTarget(ctx context.Context, dir, ref string) (string, error) {
	h, e := resolveCommit(ctx, dir, strings.TrimSpace(ref))
	if e != nil {
		return "", e
	}
	return hashHex(h), nil
}
func executeOperation(ctx context.Context, d repoOperation) (string, error) {
	dir := d.repo
	var args []string
	switch d.action {
	case "审阅并暂存", "审阅并取消暂存":
		return "", applyIndexHunk(ctx, dir, d.path, d.patch, d.action == "审阅并取消暂存", d.hunk)
	case "外部合并工具":
		args = []string{"--literal-pathspecs", "mergetool", "--no-prompt", "--", d.path}
	case "合并":
		args = []string{"merge", "--no-edit", d.target}
	case "变基":
		args = []string{"rebase", d.target}
	case "拣选":
		args = []string{"cherry-pick", d.target}
	case "撤销提交":
		args = []string{"revert", "--no-edit", d.target}
	case "软重置":
		args = []string{"reset", "--soft", d.target}
	case "混合重置":
		args = []string{"reset", "--mixed", d.target}
	case "硬重置":
		args = []string{"reset", "--hard", d.target}
	case "继续操作", "中止操作":
		state, e := operationState(ctx, dir)
		if e != nil {
			return "", e
		}
		if state == "" || state != d.state {
			return "", errors.New("仓库操作状态已变化，请重新打开")
		}
		flag := "--continue"
		if d.action == "中止操作" {
			flag = "--abort"
		}
		args = []string{state, flag}
	case "保存 Stash":
		args = []string{"stash", "push", "-m", d.value}
		if d.includeUntracked {
			args = append(args, "--include-untracked")
		}
	case "应用 Stash", "弹出 Stash", "删除 Stash":
		if d.stash < 0 || d.stash >= len(d.stashes) {
			return "", errors.New("请选择 Stash")
		}
		hash := d.stashes[d.stash].hash
		if d.action == "应用 Stash" {
			args = []string{"stash", "apply", hash}
		} else {
			ref, e := stashRef(ctx, dir, hash)
			if e != nil {
				return "", e
			}
			// Pop only drops after a successful apply; conflicts retain the stash.
			if d.action == "弹出 Stash" {
				if _, e = runGit(ctx, dir, "stash", "apply", hash); e != nil {
					return "", e
				}
				ref, e = stashRef(ctx, dir, hash)
				if e != nil {
					return "", e
				}
			}
			args = []string{"stash", "drop", ref}
		}
	case "查看 Stash":
		if d.stash < 0 || d.stash >= len(d.stashes) {
			return "", errors.New("请选择 Stash")
		}
		args = []string{"stash", "show", "--patch", "--include-untracked", d.stashes[d.stash].hash}
	case "新增远程":
		args = []string{"remote", "add", "--", d.name, d.value}
	case "修改远程地址":
		args = []string{"remote", "set-url", "--", d.remote, d.value}
	case "删除远程":
		args = []string{"remote", "remove", "--", d.remote}
	case "推送分支":
		if d.remote == "" || d.name == "" {
			return "", errors.New("请选择远程并填写目标分支")
		}
		if _, e := runGit(ctx, dir, "check-ref-format", "refs/heads/"+d.name); e != nil {
			return "", e
		}
		args = []string{"push", "--set-upstream", "--", d.remote, "HEAD:refs/heads/" + d.name}
	case "推送标签":
		if d.remote == "" {
			return "", errors.New("请选择远程")
		}
		if _, e := runGit(ctx, dir, "check-ref-format", "refs/tags/"+d.name); e != nil {
			return "", e
		}
		args = []string{"push", "--", d.remote, "refs/tags/" + d.name + ":refs/tags/" + d.name}
	case "设置身份":
		scope := "--local"
		if d.global {
			scope = "--global"
		}
		if strings.TrimSpace(d.name) == "" || strings.TrimSpace(d.value) == "" {
			return "", errors.New("姓名和邮箱不能为空")
		}
		if _, e := runGit(ctx, dir, "config", scope, "user.name", d.name); e != nil {
			return "", e
		}
		args = []string{"config", scope, "user.email", d.value}
	case "恢复历史文件":
		args = []string{"--literal-pathspecs", "restore", "--source=" + d.target, "--worktree", "--", d.path}
	case "Blame":
		args = []string{"--literal-pathspecs", "blame", "--date=short", d.target, "--", d.path}
	case "行历史":
		a, b, err := parseLineRange(d.value)
		if err != nil {
			return "", err
		}
		if strings.ContainsAny(d.path, ":\n") {
			return "", errors.New("此路径暂不支持行历史")
		}
		args = []string{"log", "--no-ext-diff", "--no-color", "-L", fmt.Sprintf("%d,%d:%s", a, b, d.path), d.target}
	case "克隆仓库":
		if d.value == "" || !filepath.IsAbs(d.path) {
			return "", errors.New("请填写仓库地址和目标目录")
		}
		args = []string{"clone", "--", d.value, d.path}
		dir = ""
	case "初始化仓库":
		if !filepath.IsAbs(d.path) {
			return "", errors.New("请填写目录")
		}
		args = []string{"init", "--", d.path}
		dir = ""
	default:
		return "", errors.New("未知操作")
	}
	b, e := runGitExtra(ctx, dir, 1<<20, []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}, args...)
	return string(b), e
}

func parseLineRange(raw string) (int, int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, 0, errors.New("请输入起止行，例如 10,20 或 10-20")
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 1 {
			return 0, 0, errors.New("行范围无效")
		}
		return n, n, nil
	}
	for _, sep := range []string{",", "-", ":", ".."} {
		if left, right, ok := strings.Cut(s, sep); ok {
			a, e1 := strconv.Atoi(strings.TrimSpace(left))
			b, e2 := strconv.Atoi(strings.TrimSpace(right))
			if e1 != nil || e2 != nil || a < 1 || b < a {
				return 0, 0, errors.New("行范围无效")
			}
			return a, b, nil
		}
	}
	return 0, 0, errors.New("请输入起止行，例如 10,20 或 10-20")
}

func repoNameFromURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimRight(s, "/\\")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/\\")
	if i := strings.LastIndexAny(s, "/:\\"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

func deriveClonePath(chosenDir, repoURL string) string {
	if chosenDir == "" {
		return ""
	}
	name := repoNameFromURL(repoURL)
	if name == "" || filepath.Base(chosenDir) == name {
		return chosenDir
	}
	entries, err := os.ReadDir(chosenDir)
	if err == nil && len(entries) > 0 {
		return filepath.Join(chosenDir, name)
	}
	return chosenDir
}
