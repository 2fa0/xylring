package main

import (
	"context"
	"fmt"
	"path/filepath"
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
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "普通",
			raw:  "worktree /repo/main\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00",
			want: "path=/repo/main branch=\"main\" head=0123456 detached=false locked=\"\" prunable=\"\" bare=false\n",
		},
		{
			name: "分离头指针",
			raw:  "worktree /repo/main\x00HEAD " + sha + "\x00detached\x00\x00",
			want: "path=/repo/main branch=\"\" head=0123456 detached=true locked=\"\" prunable=\"\" bare=false\n",
		},
		{
			name: "锁定（带与不带原因）",
			raw: "worktree /repo/main\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00" +
				"worktree /repo/locked\x00HEAD " + sha + "\x00branch refs/heads/side\x00locked\x00\x00" +
				"worktree /repo/reasoned\x00HEAD " + sha + "\x00branch refs/heads/other\x00locked user asked\x00\x00",
			want: "path=/repo/main branch=\"main\" head=0123456 detached=false locked=\"\" prunable=\"\" bare=false\n" +
				"path=/repo/locked branch=\"side\" head=0123456 detached=false locked=\"locked\" prunable=\"\" bare=false\n" +
				"path=/repo/reasoned branch=\"other\" head=0123456 detached=false locked=\"user asked\" prunable=\"\" bare=false\n",
		},
		{
			name: "可清理",
			raw:  "worktree /repo/main\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00worktree /repo/gone\x00HEAD " + sha + "\x00branch refs/heads/side\x00prunable gitdir file points to non-existent location\x00\x00",
			want: "path=/repo/main branch=\"main\" head=0123456 detached=false locked=\"\" prunable=\"\" bare=false\n" +
				"path=/repo/gone branch=\"side\" head=0123456 detached=false locked=\"\" prunable=\"gitdir file points to non-existent location\" bare=false\n",
		},
		{name: "空输出", raw: "", want: ""},
		{
			name: "含空格路径",
			raw:  "worktree /repo/my project\x00HEAD " + sha + "\x00branch refs/heads/feature branch\x00\x00worktree /repo/my project-wt\x00HEAD " + sha + "\x00detached\x00locked\x00\x00",
			want: "path=/repo/my project branch=\"feature branch\" head=0123456 detached=false locked=\"\" prunable=\"\" bare=false\n" +
				"path=/repo/my project-wt branch=\"\" head=0123456 detached=true locked=\"locked\" prunable=\"\" bare=false\n",
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
