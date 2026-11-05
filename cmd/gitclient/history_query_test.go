package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func readQuery(t *testing.T, dir string, q historyQuery, skip, n int) []commit {
	t.Helper()
	ctx := context.Background()
	meta, err := loadMeta(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	tips, err := queryTips(ctx, dir, meta, q)
	if err != nil {
		t.Fatal(err)
	}
	var out []commit
	if err := streamHistory(ctx, dir, q, tips, skip, n, func(c commit) error {
		out = append(out, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSearchEntireHistory(t *testing.T) {
	dir, git := initTestRepo(t)
	os.Mkdir(filepath.Join(dir, "src"), 0o700)
	os.WriteFile(filepath.Join(dir, "src", "说明[1].txt"), []byte("one"), 0o600)
	git("add", ".")
	git("-c", "user.name=Ada", "commit", "-m", "initial", "-m", "修复 NEEDLE.*，不是正则")
	tree := strings.TrimSpace(string(git("rev-parse", "HEAD^{tree}")))
	parent := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	for i := 0; i < pageFirst+2; i++ {
		parent = strings.TrimSpace(string(git("commit-tree", tree, "-p", parent, "-m", fmt.Sprintf("later %d", i))))
	}
	git("update-ref", "refs/heads/main", parent)
	for _, q := range []historyQuery{
		{term: "needle.*", kind: "说明"},
		{term: "ada", kind: "作者"},
		{term: "说明[1]", kind: "文件路径"},
		{term: "src/说明", kind: "文件路径"},
		{term: "SRC", kind: "文件路径"},
	} {
		got := readQuery(t, dir, q, 0, pageFirst)
		if len(got) != 1 || got[0].subject != "initial" {
			t.Fatalf("query %+v: %+v", q, got)
		}
	}
	if got := readQuery(t, dir, historyQuery{term: "不存在", kind: "说明"}, 0, pageFirst); len(got) != 0 {
		t.Fatalf("unexpected matches: %+v", got)
	}
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.loadMore(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, len(tab.commits)-1)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	originalHash, originalDiff := tab.inspecting, tab.diff
	tab.searchText = "later 0"
	a.searchHistory(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	if len(tab.commits) != 1 {
		t.Fatalf("unexpected search results: %d", len(tab.commits))
	}
	a.leaveSearch(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading && !tab.compareLoading && !tab.diffLoading })
	if tab.inspecting != originalHash || tab.selected < pageFirst || tab.diff != originalDiff || tab.compareFiles[tab.fileSel].path != "src/说明[1].txt" {
		t.Fatal("return from search lost a selection beyond the first page")
	}
}

func TestBranchScopesAndPinnedPagination(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	git("checkout", "-b", "feature")
	git("commit", "--allow-empty", "-m", "feature")
	git("checkout", "main")
	tree := strings.TrimSpace(string(git("rev-parse", "HEAD^{tree}")))
	remote := strings.TrimSpace(string(git("commit-tree", tree, "-m", "remote only")))
	git("update-ref", "refs/remotes/origin/lost", remote)
	for _, tc := range []struct {
		q historyQuery
		n int
	}{{historyQuery{}, 3}, {historyQuery{ref: "HEAD"}, 1}, {historyQuery{ref: "refs/heads/feature"}, 2}, {historyQuery{hideRemote: true}, 2}} {
		if got := readQuery(t, dir, tc.q, 0, 100); len(got) != tc.n {
			t.Fatalf("scope %+v: %d commits, want %d", tc.q, len(got), tc.n)
		}
	}
	ctx := context.Background()
	meta, _ := loadMeta(ctx, dir)
	tips, _ := queryTips(ctx, dir, meta, historyQuery{ref: "refs/heads/feature"})
	git("checkout", "feature")
	git("commit", "--allow-empty", "-m", "new after page one")
	var page []commit
	err := streamHistory(ctx, dir, historyQuery{}, tips, 1, 10, func(c commit) error { page = append(page, c); return nil })
	if err != nil || len(page) != 1 || page[0].subject != "base" {
		t.Fatalf("pagination moved after new commit: %+v %v", page, err)
	}
}

func waitUpdates(t *testing.T, updates <-chan func(), done func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !done() {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatal("等待后台读取超时")
		}
	}
}

func hoverSelectedFile(t *testing.T, tt *ui.Tester, tab *repoTab) {
	t.Helper()
	if tab.fileSel < 0 || tab.fileSel >= len(tab.compareFiles) {
		t.Fatal("没有选中的文件")
	}
	name := filepath.Base(tab.compareFiles[tab.fileSel].path)
	r, ok := tt.Find(name)
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("选中的文件不在画面上: %q", name)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
}

func clickFileAction(t *testing.T, tt *ui.Tester, tab *repoTab, action string) {
	t.Helper()
	hoverSelectedFile(t, tt, tab)
	if err := tt.Click(action); err != nil {
		t.Fatal(err)
	}
}

func retireTester(tt *ui.Tester) {
	for i := 0; i < 32; i++ {
		tt.Frame()
	}
}

func TestSearchKeyboardAndScopeMenu(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "first needle")
	git("checkout", "-b", "feature")
	git("commit", "--allow-empty", "-m", "second")
	git("checkout", "main")
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	tt.Key(commandModifier(), ui.KeyF)
	tt.Type("needle")
	tt.Key(0, ui.KeyEnter)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if len(tab.commits) != 1 || tab.query.term != "needle" || !tt.HasText("图谱") {
		t.Fatalf("search failed: %+v, %q", tab.query, tt.Texts())
	}
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if err := tt.Click("筛选分支"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("当前分支"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if tab.query.ref != "HEAD" || len(tab.commits) != 1 || !tt.HasText("图谱") {
		t.Fatalf("scope not applied: %+v, %q", tab.query, tt.Texts())
	}
}

func TestBranchVisibilityControl(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	git("checkout", "-b", "feature")
	git("commit", "--allow-empty", "-m", "local feature")
	git("checkout", "main")
	tree := strings.TrimSpace(string(git("rev-parse", "HEAD^{tree}")))
	remote := strings.TrimSpace(string(git("commit-tree", tree, "-m", "remote only")))
	git("update-ref", "refs/remotes/origin/remote-only", remote)
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if tab.query.hideRemote || len(tab.commits) != 3 || !tt.HasText("remote only") {
		t.Fatalf("default scope hid remote history: %+v, %q", tab.query, tt.Texts())
	}
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		waitUpdates(t, updates, func() bool { return !tab.loading })
		tt.Frame()
		tt.Frame()
	}
	click("本地分支")
	if !tab.query.hideRemote || len(tab.commits) != 2 || tt.HasText("remote only") {
		t.Fatalf("local scope includes remote history: %+v, %q", tab.query, tt.Texts())
	}
	click("全部分支")
	if tab.query.hideRemote || len(tab.commits) != 3 || !tt.HasText("remote only") {
		t.Fatalf("all branches did not include remote history: %+v, %q", tab.query, tt.Texts())
	}
	click("筛选分支")
	if err := tt.Click("远程 · origin/remote-only"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if tab.query.ref != "refs/remotes/origin/remote-only" || len(tab.commits) != 1 {
		t.Fatalf("remote branch filter not applied: %+v", tab.query)
	}
	click("本地分支")
	if !tab.query.hideRemote || tab.query.ref != "" || len(tab.commits) != 2 || tt.HasText("remote only") {
		t.Fatalf("local scope retained remote filter: %+v, %q", tab.query, tt.Texts())
	}
	tt.Key(0, ui.KeyRight)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if tab.query.hideRemote || len(tab.commits) != 3 {
		t.Fatalf("keyboard did not switch scope: %+v", tab.query)
	}
	tab.searchText = "only"
	a.searchHistory(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	click("本地分支")
	if len(tab.commits) != 0 || tab.query.term != "only" {
		t.Fatalf("switching scope lost search: %+v", tab.query)
	}
	click("返回")
	if !tab.query.hideRemote || tab.query.term != "" || len(tab.commits) != 2 {
		t.Fatalf("leaving search lost local scope: %+v", tab.query)
	}
}

func TestLocateRefShowsErrorNearInput(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tab.locate = "111111111111111111111111"
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("跳到"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return tab.locateErr != "" })
	tt.Frame()
	if !tt.HasText("找不到") || tt.HasText("fatal") {
		t.Fatalf("texts=%q err=%q", tt.Texts(), tab.locateErr)
	}
	jump, ok := tt.Find("跳到")
	msg, found := tt.Find(tab.locateErr)
	if !ok || !found {
		t.Fatalf("missing jump or error: jump=%v msg=%v texts=%q", ok, found, tt.Texts())
	}
	if msg.Y < jump.Y || msg.Y > jump.Y+80 {
		t.Fatalf("error not under toolbar: jump=%+v msg=%+v", jump, msg)
	}
	if r, ok := tt.Find(tab.locate); ok {
		tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	} else {
		tt.ClickAt(jump.X-40, jump.Y+jump.H/2)
	}
	tt.Type("x")
	tt.Frame()
	if tab.locateErr != "" || tt.HasText("找不到") {
		t.Fatalf("error survived edit: %q %q", tab.locateErr, tt.Texts())
	}
}
