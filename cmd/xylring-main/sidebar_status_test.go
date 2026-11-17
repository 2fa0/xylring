package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 长分支名会把分支名截断，但不能把后面的状态（工作区有改动、领先提交数）一起
// 截掉：状态单独成一个文本，且不参与收缩。
func TestSidebarBranchStatusSurvivesLongBranchName(t *testing.T) {
	long := "ZA-PR-1021130_13784_feature_with_a_very_long_name"
	root := "/tmp/work/zascr-bapi"
	tab := newTab(1, root)
	tab.loaded, tab.branch, tab.worktreeCount, tab.ahead = true, long, 1, 1
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{root},
		foundFrom: "/tmp/work",
		repoSplit: 208,
	}

	tt := ui.NewTester(a.view, 900, 600)
	tt.Frame()

	// 分支名被压到侧栏能放下的宽度，说明这一行真的不够宽。
	branch, ok := tt.Find(long)
	if !ok {
		t.Fatalf("missing branch label, texts: %q", tt.Texts())
	}
	if branch.W >= 130 {
		t.Fatalf("long branch name should be clipped, got width %v", branch.W)
	}

	// 状态标记完整画出来：有自己的文本和宽度，没有被分支名吃掉。
	mark, ok := tt.Find("* ↑1")
	if !ok {
		t.Fatalf("missing status mark in sidebar, texts: %q", tt.Texts())
	}
	if mark.W <= 0 {
		t.Fatalf("status mark has no width: %+v", mark)
	}
	if mark.X < branch.X+branch.W {
		t.Fatalf("status mark overlaps branch name: branch=%+v mark=%+v", branch, mark)
	}
	if right := mark.X + mark.W; right > a.repoSplit {
		t.Fatalf("status mark spills out of sidebar: right=%v split=%v", right, a.repoSplit)
	}
}

// 工作树子行的改动标记同样不会被长分支名截掉。
func TestWorktreeRowDirtyMarkSurvivesLongBranchName(t *testing.T) {
	long := "ZA-PR-1021130_13784_feature_with_a_very_long_name"
	root := "/tmp/work/zascr-bapi"
	side := "/tmp/work/zascr-bapi-side"
	tab := newTab(1, root)
	tab.loaded = true
	tab.worktrees = []repoWorktree{{path: side, branch: long, head: worktreeHash}}
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{root},
		foundFrom: "/tmp/work",
		repoSplit: 208,
		// 工作树行的状态来自仓库摘要，写成刚查过的，免得被刷新成“仓库不可用”。
		repoSummaries: map[string]repoSummary{side: {branch: long, dirty: true, checked: time.Now()}},
	}

	tt := ui.NewTester(a.view, 900, 600)
	tt.Frame()

	mark, ok := tt.Find("*")
	if !ok {
		t.Fatalf("missing dirty mark on worktree row, texts: %q", tt.Texts())
	}
	if mark.W <= 0 {
		t.Fatalf("dirty mark has no width: %+v", mark)
	}
	if right := mark.X + mark.W; right > a.repoSplit {
		t.Fatalf("dirty mark spills out of sidebar: right=%v split=%v", right, a.repoSplit)
	}
}
