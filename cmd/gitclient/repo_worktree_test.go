package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// worktreeDump 是解析结果的快照形式：字段按固定顺序排，一行为一个工作树。
func worktreeDump(list []repoWorktree) string {
	var b strings.Builder
	for _, wt := range list {
		fmt.Fprintf(&b, "path=%s branch=%q head=%s detached=%v locked=%q prunable=%q bare=%v\n",
			wt.path, wt.branch, shortHash(wt.head), wt.detached, wt.locked, wt.prunable, wt.bare)
	}
	return b.String()
}

func TestParseWorktreeList(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	// 路径先过 canonicalRepoDir：Windows 会补盘符，分隔符也换成 \。
	path := func(p string) string { return "path=" + canonicalRepoDir(p) }
	line := func(p, branch, head, detached, locked, prunable, bare string) string {
		return path(p) + " branch=" + branch + " head=" + head + " detached=" + detached +
			" locked=" + locked + " prunable=" + prunable + " bare=" + bare + "\n"
	}
	plain := func(p, branch string) string {
		return line(p, strconv.Quote(branch), "0123456", "false", `""`, `""`, "false")
	}
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "普通",
			raw:  "worktree /repo/main\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00",
			want: plain("/repo/main", "main"),
		},
		{
			name: "分离头指针",
			raw:  "worktree /repo/main\x00HEAD " + sha + "\x00detached\x00\x00",
			want: line("/repo/main", `""`, "0123456", "true", `""`, `""`, "false"),
		},
		{
			name: "锁定（带与不带原因）",
			raw: "worktree /repo/main\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00" +
				"worktree /repo/locked\x00HEAD " + sha + "\x00branch refs/heads/side\x00locked\x00\x00" +
				"worktree /repo/reasoned\x00HEAD " + sha + "\x00branch refs/heads/other\x00locked user asked\x00\x00",
			want: plain("/repo/main", "main") +
				line("/repo/locked", `"side"`, "0123456", "false", `"locked"`, `""`, "false") +
				line("/repo/reasoned", `"other"`, "0123456", "false", `"user asked"`, `""`, "false"),
		},
		{
			name: "可清理",
			raw:  "worktree /repo/main\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00worktree /repo/gone\x00HEAD " + sha + "\x00branch refs/heads/side\x00prunable gitdir file points to non-existent location\x00\x00",
			want: plain("/repo/main", "main") +
				line("/repo/gone", `"side"`, "0123456", "false", `""`, `"gitdir file points to non-existent location"`, "false"),
		},
		{name: "空输出", raw: "", want: ""},
		{
			name: "含空格路径",
			raw:  "worktree /repo/my project\x00HEAD " + sha + "\x00branch refs/heads/feature branch\x00\x00worktree /repo/my project-wt\x00HEAD " + sha + "\x00detached\x00locked\x00\x00",
			want: plain("/repo/my project", "feature branch") +
				line("/repo/my project-wt", `""`, "0123456", "true", `"locked"`, `""`, "false"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := worktreeDump(parseWorktreeList([]byte(tc.raw))); got != tc.want {
				t.Fatalf("parseWorktreeList:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestWorktreeListExcludesMainWorktree(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "first")
	side := filepath.Join(t.TempDir(), "side checkout")
	git("worktree", "add", side, "-b", "side")

	ctx := context.Background()
	list, err := listWorktrees(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].branch != "side" {
		t.Fatalf("linked worktrees = %+v", list)
	}
	if list[0].path != canonicalRepoDir(side) {
		t.Fatalf("path=%q want %q", list[0].path, canonicalRepoDir(side))
	}

	// 从工作树里读也一样：主工作树排第一条，不进子行。
	fromSide, err := listWorktrees(ctx, side)
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range fromSide {
		if wt.path == canonicalRepoDir(dir) || wt.path == canonicalRepoDir(side) {
			t.Fatalf("main or own worktree left in %+v", fromSide)
		}
	}

	// 侧栏里该仓库下正好多一行，主工作树不再单独出现。
	a := &app{}
	tab := newTab(1, dir)
	tab.loaded, tab.worktrees = true, list
	a.tabs = []*repoTab{tab}
	rows := a.sidebarRows(a.repoItems())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].path != dir || rows[0].worktree != nil {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1].worktree == nil || rows[1].path != canonicalRepoDir(side) {
		t.Fatalf("child row = %+v", rows[1])
	}
	if label := worktreeRowLabel(*rows[1].worktree, false); label != "side" {
		t.Fatalf("child label = %q", label)
	}
}
