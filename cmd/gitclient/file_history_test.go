package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func renameHistoryRepo(t *testing.T) (string, func(...string) []byte) {
	t.Helper()
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "旧名[1].txt"), []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "create")
	git("mv", "旧名[1].txt", "新名.txt")
	git("commit", "-m", "rename")
	os.WriteFile(filepath.Join(dir, "新名.txt"), []byte("two\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("unrelated\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "modify")
	git("rm", "新名.txt")
	git("commit", "-m", "delete")
	return dir, git
}

func TestFileHistoryReturnsComparisonAndLocatesUnreferencedCommit(t *testing.T) {
	dir, git := renameHistoryRepo(t)
	tree := strings.TrimSpace(string(git("rev-parse", "HEAD~1^{tree}")))
	orphan := strings.TrimSpace(string(git("commit-tree", tree, "-m", "unreferenced")))
	ctx := context.Background()
	head, _ := resolveCommit(ctx, dir, "HEAD")
	target, err := parseHash(orphan)
	if err != nil {
		t.Fatal(err)
	}
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.showComparison(tab, head, target, "main", orphan, false)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	diff := tab.diff
	a.openFileHistory(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.leaveFileHistory(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading && !tab.compareLoading && !tab.diffLoading })
	if tab.comparison == nil || tab.comparison.to != target || tab.diff != diff {
		t.Fatal("return from file history lost the comparison")
	}
	a.openFileHistory(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 0)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	a.locateFileCommit(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading && !tab.compareLoading && !tab.diffLoading })
	if tab.inspecting != target || tab.indexOf(target) < 0 || tab.query.filePath != "" {
		t.Fatal("could not locate an unreferenced file revision in the graph")
	}
}

func TestFileHistoryFollowsRenamesAcrossPages(t *testing.T) {
	dir, _ := renameHistoryRepo(t)
	ctx := context.Background()
	head, _ := resolveCommit(ctx, dir, "HEAD")
	q := historyQuery{filePath: "新名.txt", fileAt: head}
	wantSubjects := []string{"delete", "modify", "rename", "create"}
	wantPaths := []string{"新名.txt", "新名.txt", "新名.txt", "旧名[1].txt"}
	for i := range wantSubjects {
		page := readQuery(t, dir, q, i, 1)
		if len(page) != 1 || page[0].subject != wantSubjects[i] || page[0].file == nil || page[0].file.path != wantPaths[i] {
			t.Fatalf("page %d: %+v", i, page)
		}
		if i == 2 && (page[0].file.status != "R" || page[0].file.oldPath != "旧名[1].txt") {
			t.Fatalf("lost rename: %+v", page[0].file)
		}
	}
	if page := readQuery(t, dir, q, 4, 1); len(page) != 0 {
		t.Fatalf("extra page: %+v", page)
	}
}

func TestFileHistoryReadsHistoricalPathsAndReturns(t *testing.T) {
	dir, _ := renameHistoryRepo(t)
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 0)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	originalDiff, originalHash := tab.diff, tab.inspecting
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	clickFileAction(t, tt, tab, "文件历史")
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if slices.Contains(tt.Texts(), "图谱") || !tt.HasText("新名.txt") || len(tab.commits) != 4 {
		t.Fatalf("history not shown: %q", tt.Texts())
	}
	if err := tt.Click("rename"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	tt.Frame()
	if len(tab.compareFiles) != 1 || tab.diff == nil || tab.diff.Old.String() != "one\n" || tab.diff.New.String() != "one\n" || tab.compareFiles[0].oldPath != "旧名[1].txt" {
		t.Fatalf("wrong rename diff: files=%+v note=%s", tab.compareFiles, tab.diffNote)
	}
	if err := tt.Click("create"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	tt.Frame()
	if tab.compareFiles[0].path != "旧名[1].txt" || tab.diff.New.String() != "one\n" {
		t.Fatal("old path not loaded")
	}
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading && !tab.compareLoading && !tab.diffLoading })
	tt.Frame()
	if tab.query.filePath != "" || tab.inspecting != originalHash || tab.diff != originalDiff || !tt.HasText("图谱") {
		t.Fatalf("return lost context: hash=%x diffSame=%v", tab.inspecting, tab.diff == originalDiff)
	}
	a.openFileHistory(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 2)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	target := tab.inspecting
	a.locateFileCommit(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading && !tab.compareLoading && !tab.diffLoading })
	if tab.query.filePath != "" || tab.inspecting != target || tab.selected != 2 {
		t.Fatal("could not locate file commit in graph")
	}
}

func TestFileHistoryLocateKeepsScope(t *testing.T) {
	dir, git := renameHistoryRepo(t)
	git("checkout", "-b", "feature")
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.changeScope(tab, historyQuery{ref: "HEAD"})
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 0)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	a.openFileHistory(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 1)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	a.locateFileCommit(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	if tab.query.ref != "HEAD" || tab.query.filePath != "" || tab.query.hideRemote {
		t.Fatalf("locate dropped scope: %+v", tab.query)
	}
}

func TestFileHistoryFromLaterFile(t *testing.T) {
	dir, git := initTestRepo(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o600)
	}
	git("add", ".")
	git("commit", "-m", "four files")
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 0)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	if len(tab.compareFiles) < 4 {
		t.Fatalf("need 4 files, got %d", len(tab.compareFiles))
	}
	tab.fileSel = 3
	want := tab.compareFiles[3].path
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	clickFileAction(t, tt, tab, "文件历史")
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if tab.query.filePath != want {
		t.Fatalf("file history path=%q want=%q", tab.query.filePath, want)
	}
}
