package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexHunksAndStalePatch(t *testing.T) {
	dir, git := initTestRepo(t)
	ctx := context.Background()
	p := filepath.Join(dir, "a.txt")
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "unchanged")
	}
	write := func() {
		t.Helper()
		if e := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write()
	git("add", ".")
	git("commit", "-m", "initial")
	lines[1] = "first change"
	lines[28] = "second change"
	write()
	patch, e := readIndexPatch(ctx, dir, "a.txt", false)
	if e != nil {
		t.Fatal(e)
	}
	h, e := splitPatch(patch)
	if e != nil || len(h) != 2 {
		t.Fatalf("hunks=%d err=%v", len(h), e)
	}
	if e = applyIndexHunk(ctx, dir, "a.txt", patch, false, 0); e != nil {
		t.Fatal(e)
	}
	staged := string(git("diff", "--cached"))
	if !strings.Contains(staged, "first change") || strings.Contains(staged, "second change") {
		t.Fatal(staged)
	}
	if e = applyIndexHunk(ctx, dir, "a.txt", patch, false, 1); e == nil {
		t.Fatal("stale patch accepted")
	}
	patch, e = readIndexPatch(ctx, dir, "a.txt", true)
	if e != nil {
		t.Fatal(e)
	}
	if e = applyIndexHunk(ctx, dir, "a.txt", patch, true, 0); e != nil {
		t.Fatal(e)
	}
	if s := string(git("diff", "--cached")); s != "" {
		t.Fatal(s)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "first change") {
		t.Fatal("worktree lost")
	}
}
func TestStashStableIdentityAndRestoreFile(t *testing.T) {
	dir, git := initTestRepo(t)
	ctx := context.Background()
	p := filepath.Join(dir, "file.txt")
	os.WriteFile(p, []byte("base\n"), 0600)
	git("add", ".")
	git("commit", "-m", "initial")
	base := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	os.WriteFile(p, []byte("first\n"), 0600)
	if _, e := executeOperation(ctx, repoOperation{repo: dir, action: "保存 Stash", value: "first"}); e != nil {
		t.Fatal(e)
	}
	entries, e := listStashes(ctx, dir)
	if e != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, e)
	}
	os.WriteFile(p, []byte("second\n"), 0600)
	git("stash", "push", "-m", "second")
	if _, e = executeOperation(ctx, repoOperation{repo: dir, action: "删除 Stash", stashes: entries, stash: 0}); e != nil {
		t.Fatal(e)
	}
	remaining, _ := listStashes(ctx, dir)
	if len(remaining) != 1 || !strings.Contains(remaining[0].label, "second") {
		t.Fatal(remaining)
	}
	os.WriteFile(p, []byte("staged\n"), 0600)
	git("add", ".")
	os.WriteFile(p, []byte("working\n"), 0600)
	if _, e = executeOperation(ctx, repoOperation{repo: dir, action: "恢复历史文件", target: base, path: "file.txt"}); e != nil {
		t.Fatal(e)
	}
	if s := string(git("show", ":file.txt")); s != "staged\n" {
		t.Fatal(s)
	}
	if b, _ := os.ReadFile(p); string(b) != "base\n" {
		t.Fatal(string(b))
	}
}
func TestMergeConflictContinueAbort(t *testing.T) {
	dir, git := initTestRepo(t)
	ctx := context.Background()
	p := filepath.Join(dir, "f")
	os.WriteFile(p, []byte("base\n"), 0600)
	git("add", ".")
	git("commit", "-m", "base")
	git("checkout", "-b", "feature")
	os.WriteFile(p, []byte("feature\n"), 0600)
	git("commit", "-am", "feature")
	feature := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	git("checkout", "main")
	os.WriteFile(p, []byte("main\n"), 0600)
	git("commit", "-am", "main")
	if _, e := executeOperation(ctx, repoOperation{repo: dir, action: "合并", target: feature}); e == nil {
		t.Fatal("expected conflict")
	}
	state, e := operationState(ctx, dir)
	if e != nil || state != "merge" {
		t.Fatalf("%s %v", state, e)
	}
	if _, e = executeOperation(ctx, repoOperation{repo: dir, action: "中止操作", state: state}); e != nil {
		t.Fatal(e)
	}
	if state, e = operationState(ctx, dir); e != nil || state != "" {
		t.Fatalf("%s %v", state, e)
	}
}

func TestRemoteIdentityCloneAndReadOperations(t *testing.T) {
	dir, git := initTestRepo(t)
	ctx := context.Background()
	os.WriteFile(filepath.Join(dir, "f"), []byte("one\ntwo\n"), 0600)
	git("add", ".")
	git("commit", "-m", "base")
	hash := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	remote := filepath.Join(t.TempDir(), "remote.git")
	git("init", "--bare", remote)
	for _, d := range []repoOperation{
		{repo: dir, action: "新增远程", name: "test-remote", value: remote},
		{repo: dir, action: "推送分支", remote: "test-remote", name: "review"},
		{repo: dir, action: "设置身份", name: "Local Name", value: "local@example.com"},
		{repo: dir, action: "Blame", target: hash, path: "f"},
		{repo: dir, action: "行历史", target: hash, path: "f", value: "1,2"},
	} {
		if _, e := executeOperation(ctx, d); e != nil {
			t.Fatalf("%s: %v", d.action, e)
		}
	}
	if s := strings.TrimSpace(string(git("config", "--local", "user.name"))); s != "Local Name" {
		t.Fatal(s)
	}
	if s := branchUpstream(t, dir, "main"); s != "test-remote/review" {
		t.Fatal(s)
	}
	git("tag", "v1")
	if _, e := executeOperation(ctx, repoOperation{repo: dir, action: "推送标签", remote: "test-remote", name: "v1"}); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "clone")
	if _, e := executeOperation(ctx, repoOperation{action: "克隆仓库", value: dir, path: target}); e != nil {
		t.Fatal(e)
	}
	if _, e := repoRoot(ctx, target); e != nil {
		t.Fatal(e)
	}
	target = filepath.Join(t.TempDir(), "new")
	if _, e := executeOperation(ctx, repoOperation{action: "初始化仓库", path: target}); e != nil {
		t.Fatal(e)
	}
	if _, e := repoRoot(ctx, target); e != nil {
		t.Fatal(e)
	}
}
func TestCommitOperations(t *testing.T) {
	for _, action := range []string{"拣选", "撤销提交", "变基", "软重置", "混合重置", "硬重置"} {
		t.Run(action, func(t *testing.T) {
			dir, git := initTestRepo(t)
			ctx := context.Background()
			git("commit", "--allow-empty", "-m", "base")
			base := strings.TrimSpace(string(git("rev-parse", "HEAD")))
			git("checkout", "-b", "feature")
			os.WriteFile(filepath.Join(dir, "feature"), []byte("change"), 0600)
			git("add", ".")
			git("commit", "-m", "feature")
			feature := strings.TrimSpace(string(git("rev-parse", "HEAD")))
			target := base
			if action == "拣选" || action == "变基" {
				git("checkout", "main")
				target = feature
			} else if action == "撤销提交" {
				target = feature
			}
			if _, e := executeOperation(ctx, repoOperation{repo: dir, action: action, target: target}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
