package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAutoRefreshKeepsReadingContext(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "first")
	for range 12 {
		git("commit", "--allow-empty", "-m", "later")
	}
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, len(tab.commits)-1)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	diff, selected := tab.diff, tab.inspecting
	if diff == nil || tab.fileSel != 0 {
		t.Fatal("missing initial diff")
	}
	tt := ui.NewTester(a.view, 1100, 560)
	tt.Frame()
	tab.list.ScrollTo(5, ui.Start)
	tt.Frame()
	first, _ := tab.list.Visible()
	top := tab.commits[first].hash
	rBefore, _ := tt.Find(shortHash(top))
	git("commit", "--allow-empty", "-m", "external commit")
	a.checkActiveRepo()
	waitUpdates(t, updates, func() bool { return !tab.checking && !tab.loading })
	tt.Frame()
	if tab.commits[0].subject != "external commit" || tab.inspecting != selected || tab.diff != diff || tab.fileSel != 0 || !tab.selection.Has(selected) {
		t.Fatalf("reading context changed: inspecting=%x file=%d diffSame=%v", tab.inspecting, tab.fileSel, tab.diff == diff)
	}
	first, _ = tab.list.Visible()
	rAfter, _ := tt.Find(shortHash(top))
	if rAfter.Y != rBefore.Y {
		t.Fatalf("scroll offset moved: %v -> %v", rBefore.Y, rAfter.Y)
	}
	if tab.commits[first].hash != top {
		t.Fatalf("scroll anchor moved: %x -> %x", top, tab.commits[first].hash)
	}
	gen := tab.generation
	a.checkActiveRepo()
	waitUpdates(t, updates, func() bool { return !tab.checking })
	if tab.generation != gen {
		t.Fatal("unchanged repository unnecessarily reloaded")
	}
	git("checkout", "-b", "new-branch")
	a.checkActiveRepo()
	waitUpdates(t, updates, func() bool { return !tab.checking && !tab.loading })
	if tab.branch != "new-branch" || tab.diff != diff {
		t.Fatalf("checkout not reflected: %s", tab.branch)
	}
	a.refresh(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	if tab.inspecting != selected || tab.diff != diff {
		t.Fatal("manual refresh lost the current diff")
	}
}

func TestHistoryIgnoresClosedAndObsoleteLoads(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "first")
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	a.reload(tab)
	a.changeHistory(tab, historyQuery{term: "not found", kind: "说明"})
	waitUpdates(t, updates, func() bool { return !tab.loading })
	if len(tab.commits) != 0 || tab.query.term != "not found" {
		t.Fatal("obsolete load replaced the new search")
	}
	a.reload(tab)
	a.closeTab(0)
	fn := <-updates
	fn()
	if len(a.tabs) != 0 || !tab.closed {
		t.Fatal("closed repository was restored")
	}
}

func TestAutoRefreshUpdatesSameFileContents(t *testing.T) {
	for _, untracked := range []bool{false, true} {
		t.Run(fmt.Sprint(untracked), func(t *testing.T) {
			dir, git := initTestRepo(t)
			path := filepath.Join(dir, "a.txt")
			os.WriteFile(path, []byte("base\n"), 0o600)
			git("add", ".")
			git("commit", "-m", "base")
			if untracked {
				path = filepath.Join(dir, "new.txt")
			}
			os.WriteFile(path, []byte("first\n"), 0o600)
			os.WriteFile(filepath.Join(dir, "0-other.txt"), []byte("other dirty file\n"), 0o600)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			tab := newTab(1, dir)
			updates := make(chan func(), 64)
			a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
			defer tab.cancel()
			a.reload(tab)
			waitUpdates(t, updates, func() bool { return !tab.loading })
			a.showWorktree(tab)
			waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
			for i, f := range tab.compareFiles {
				if f.path == filepath.Base(path) {
					tab.fileSel = i
					a.loadFileDiff(tab, i)
					break
				}
			}
			waitUpdates(t, updates, func() bool { return !tab.diffLoading })
			// Equal-length edits and preserved timestamps must still refresh the preview.
			for _, content := range []string{"other\n", "other\nmore\n"} {
				os.WriteFile(path, []byte(content), 0o600)
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				a.checkActiveRepo()
				waitUpdates(t, updates, func() bool { return !tab.checking && !tab.loading && !tab.compareLoading && !tab.diffLoading })
				if tab.diff == nil || tab.diff.New.String() != content {
					t.Fatalf("preview did not update to %q", content)
				}
			}
			diff := tab.diff
			a.checkActiveRepo()
			waitUpdates(t, updates, func() bool { return !tab.checking })
			if tab.diff != diff {
				t.Fatal("unchanged worktree rebuilt its diff")
			}
		})
	}
}
