package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func newTestApp(t *testing.T, tab *repoTab) (*app, chan func()) {
	t.Helper()
	updates := make(chan func(), 64)
	a := &app{split: 60, filePane: 260, tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	t.Cleanup(func() {
		for _, tab := range a.tabs {
			tab.releaseHeavy()
		}
	})
	return a, updates
}

func awaitUpdates(t *testing.T, updates chan func(), ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for !ready() {
		select {
		case fn := <-updates:
			fn()
		case <-deadline.C:
			t.Fatal("timed out waiting for repository update")
		}
	}
}

func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts=%q", label, err, tt.Texts())
	}
}

func TestNavigateOutsideLoadedHistory(t *testing.T) {
	dir, git := initTestRepo(t)
	importTestHistory(t, dir, 650)
	git("branch", "old", "HEAD~649")
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded && !tab.loading })
	if tab.err != "" || len(tab.commits) != pageFirst {
		t.Fatalf("initial history: %d %s", len(tab.commits), tab.err)
	}
	tt := ui.NewTester(a.view, 1000, 800)
	tab.refInput = "old"
	tt.Frame()
	click(t, tt, "打开提交")
	awaitUpdates(t, updates, func() bool { return !tab.refBusy && tab.loaded && !tab.loading })
	tt.Frame()
	awaitUpdates(t, updates, func() bool { return !tab.detailLoading && tab.info != nil })
	tt.Frame()
	if tab.scope != "指定 ref" || tab.selectedCommit() == nil || tab.selectedCommit().subject != "commit 0" || !tt.HasText("commit 0") || !tt.HasText("test@example.com") {
		t.Fatalf("jump failed: scope=%s selected=%v texts=%q", tab.scope, tab.selectedCommit(), tt.Texts())
	}
	want := tab.selectedCommit().hash
	tab.refInput = "not-a-ref"
	tt.Frame()
	click(t, tt, "打开提交")
	awaitUpdates(t, updates, func() bool { return !tab.refBusy })
	if tab.refErr == "" || tab.selectedCommit() == nil || tab.selectedCommit().hash != want {
		t.Fatal("invalid ref replaced current history")
	}
	if branch := strings.TrimSpace(string(git("symbolic-ref", "--short", "HEAD"))); branch != "main" {
		t.Fatalf("navigation checked out %s", branch)
	}
}

func TestTabSwitchRestoresDeepSelectionAndScroll(t *testing.T) {
	dir, _ := initTestRepo(t)
	importTestHistory(t, dir, 650)
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded && !tab.loading })
	for len(tab.commits) < 600 {
		a.loadMore(tab)
		awaitUpdates(t, updates, func() bool { return !tab.loading })
		if tab.err != "" {
			t.Fatal(tab.err)
		}
	}
	tt := ui.NewTester(a.view, 1000, 800)
	tab.selected = 549
	tab.list.ScrollTo(540, ui.Start)
	tt.Frame()
	want := tab.selectedCommit().hash
	first, _ := tab.list.Visible()
	other := newTab(2, "/tmp/other-repo")
	other.loaded = true
	a.tabs = append(a.tabs, other)
	a.activate(1)
	tt.Frame()
	if tab.commits != nil || tab.info != nil || tab.diff != nil || !tab.released {
		t.Fatal("background tab kept heavy data")
	}
	a.activate(0)
	awaitUpdates(t, updates, func() bool { return tab.loaded && !tab.loading })
	tt.Frame()
	gotFirst, _ := tab.list.Visible()
	if selected := tab.selectedCommit(); selected == nil || selected.hash != want || gotFirst != first {
		t.Fatalf("restored row=%d first=%d want first=%d error=%s", tab.selected, gotFirst, first, tab.err)
	}
}

func TestMultiBranchFilterUI(t *testing.T) {
	dir, git := initTestRepo(t)
	importTestHistory(t, dir, 10)
	git("branch", "old", "HEAD~9")
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	tt := ui.NewTester(a.view, 1000, 800)
	click(t, tt, "全部分支")
	click(t, tt, "多选分支")
	if !a.showFilter {
		t.Fatal("branch filter did not open")
	}
	click(t, tt, "应用筛选")
	if a.filterErr == "" || !a.showFilter {
		t.Fatal("empty selection was accepted")
	}
	click(t, tt, "old（本地）")
	click(t, tt, "应用筛选")
	awaitUpdates(t, updates, func() bool { return tab.loaded && !tab.loading })
	tt.Frame()
	if tab.scope != "多选分支" || len(tab.commits) != 1 || tab.commits[0].subject != "commit 0" {
		t.Fatalf("filter: scope=%s count=%d error=%s", tab.scope, len(tab.commits), tab.err)
	}
	click(t, tt, "选择分支…")
	click(t, tt, "取消")
	tt.Frame()
	click(t, tt, "多选分支")
	click(t, tt, "当前分支")
	awaitUpdates(t, updates, func() bool { return tab.loaded && !tab.loading })
	if len(tab.commits) != 10 || tab.scope != "当前分支" {
		t.Fatalf("current branch: count=%d scope=%s", len(tab.commits), tab.scope)
	}
}

func TestComparisonUIAndRenameDiff(t *testing.T) {
	dir, git := initTestRepo(t)
	oldPath := filepath.Join(dir, "旧文件.txt")
	if err := os.WriteFile(oldPath, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "旧文件.txt")
	git("commit", "-m", "before")
	git("branch", "before")
	git("mv", "旧文件.txt", "新文件.txt")
	git("commit", "-m", "after")
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	tt := ui.NewTester(a.view, 1000, 800)
	click(t, tt, "before")
	if err := tt.ClickWith(ui.Cmd, "after"); err != nil {
		t.Fatal(err)
	}
	awaitUpdates(t, updates, func() bool { return !tab.detailLoading })
	tt.Frame()
	if tab.comparison == nil || len(tab.files) != 1 || tab.files[0].status != "R" || !tt.HasText("关闭比较") {
		t.Fatalf("Cmd-click comparison=%v files=%v err=%s", tab.comparison, tab.files, tab.detailErr)
	}
	awaitUpdates(t, updates, func() bool { return !tab.diffLoading })
	if tab.diff == nil {
		t.Fatalf("comparison diff missing: %s", tab.detailErr)
	}
	old, next, note, err := loadSides(context.Background(), dir, tab.files[0], false, hashHex(tab.comparison.to), hashHex(tab.comparison.from))
	if err != nil || note != "" || old != "before\n" || next != old {
		t.Fatalf("rename sides: %q %q %q %v", old, next, note, err)
	}
	// Refresh must preserve comparison rather than reselect an older list row.
	want := *tab.comparison
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded && !tab.loading && !tab.detailLoading })
	tt.Frame()
	if tab.comparison == nil || *tab.comparison != want {
		t.Fatal("comparison was lost on reload")
	}
	click(t, tt, "关闭比较")
	click(t, tt, "比较…")
	a.compareTo = "before..main"
	tt.Frame()
	click(t, tt, "开始比较")
	awaitUpdates(t, updates, func() bool { return !tab.refBusy && !tab.detailLoading })
	tt.Frame()
	if a.showCompare || tab.comparison == nil || *tab.comparison != want {
		t.Fatalf("ref comparison: %v error=%s", tab.comparison, a.compareErr)
	}
	click(t, tt, "比较…")
	a.compareTo = "before...main"
	tt.Frame()
	click(t, tt, "开始比较")
	if a.compareErr == "" || !a.showCompare {
		t.Fatal("three-dot comparison was accepted as two-dot")
	}
	click(t, tt, "取消")
}
