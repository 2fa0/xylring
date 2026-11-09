package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestCloseRightFileTabsReleasesReferences(t *testing.T) {
	tab := newTab(1, "/unused")
	tab.fileTabs = []*fileDiffTab{{}, {}, {}}
	backing := tab.fileTabs
	a := &app{}
	a.closeRightFileTabs(tab, 0)
	if len(tab.fileTabs) != 1 || backing[1] != nil || backing[2] != nil {
		t.Fatal("closed file tabs remain reachable")
	}
}

func TestFileTabBudgetIncludesFullBufferAndDeduplicatesBuffers(t *testing.T) {
	buf := editor.NewBuffer(strings.Repeat("x", 4<<20))
	tab := newTab(1, "/unused")
	tab.diff = &editor.DiffState{Old: buf, New: buf}
	tab.fileTabs = []*fileDiffTab{{fullFile: true, fullContent: editor.State{Buffer: buf}}}
	want := int64(4<<20) + int64(unsafe.Sizeof(*buf)) + int64(buf.LineCount())*32
	if got := tab.cachedBytes(); got != want {
		t.Fatalf("buffer budget = %d", got)
	}
}

func TestFileTabEvictionPreservesSnapshotsAndCleansDisk(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprint(full), func(t *testing.T) {
			repo, active := newTab(1, t.TempDir()), newTab(2, "/active")
			snapshot := &fileDiffTab{file: fileChange{path: "a.txt", status: "M"}, worktree: true, fullFile: full}
			if full {
				snapshot.fullContent = editor.State{Buffer: editor.NewBuffer("original snapshot\n")}
			} else {
				snapshot.source = &fileDiffSource{old: []byte("base\n"), new: []byte("original snapshot\n")}
				snapshot.diff, snapshot.simple = prepareDiff(snapshot.source.old, snapshot.source.new, "a.txt")
			}
			repo.fileTabs = []*fileDiffTab{snapshot}
			repo.loaded, repo.historyBytes = true, historyMemoryStep+1
			updates := make(chan func(), 16)
			a := &app{tabs: []*repoTab{repo, active}, active: 1, dispatch: func(fn func()) { updates <- fn }}
			t.Cleanup(func() { a.closeAllFileTabs(repo) })
			a.trimInactiveHistory()
			waitUpdates(t, updates, func() bool { return snapshot.evicted })
			path := snapshot.backing
			if repo.cachedBytes() != 0 || path == "" || snapshot.source != nil || snapshot.diff != nil || snapshot.fullContent.Buffer != nil {
				t.Fatal("eviction retained heavy file data")
			}
			if err := os.WriteFile(filepath.Join(repo.path, "a.txt"), []byte("later edit\n"), 0600); err != nil {
				t.Fatal(err)
			}
			a.active = 0
			repo.contentTab = 1
			a.restoreFileTab(repo, snapshot)
			waitUpdates(t, updates, func() bool { return snapshot.diff != nil || snapshot.fullContent.Buffer != nil })
			var got string
			if full {
				got = snapshot.fullContent.Buffer.String()
			} else {
				got = snapshot.diff.New.String()
			}
			if got != "original snapshot\n" {
				t.Fatalf("restored current file instead of snapshot: %q", got)
			}
			if !full {
				snapshot.ignoreWhitespace = true
				a.reloadFileTab(repo, snapshot)
				waitUpdates(t, updates, func() bool { return snapshot.note != "正在读取改动…" })
				if snapshot.diff == nil || !strings.Contains(snapshot.diff.New.String(), "original snapshot") {
					t.Fatal("whitespace toggle lost snapshot")
				}
			}
			a.closeFileTab(repo, 0)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("closed tab left disk backing: %v", err)
			}
		})
	}
}

func TestCloseDuringFileTabSpillDiscardsResult(t *testing.T) {
	repo := newTab(1, "/unused")
	snapshot := &fileDiffTab{source: &fileDiffSource{new: []byte("snapshot")}}
	repo.fileTabs = []*fileDiffTab{snapshot}
	updates := make(chan func(), 4)
	a := &app{dispatch: func(fn func()) { updates <- fn }}
	a.spillFileTabs(repo)
	a.closeAllFileTabs(repo)
	waitUpdates(t, updates, func() bool { return !snapshot.cachePending })
	if snapshot.backing != "" {
		t.Fatal("closed tab accepted a spill result")
	}
}

func TestWorktreePollCachesUnchangedContentAndChecksIndex(t *testing.T) {
	dir, git := initTestRepo(t)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("base\n"), 0600)
	git("add", ".")
	git("commit", "-m", "base")
	os.WriteFile(path, []byte("first\n"), 0600)
	ctx := context.Background()
	files, cache, err := pollWorktreeStatus(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, cached, err := pollWorktreeStatus(ctx, dir, cache)
	if err != nil || cached != cache {
		t.Fatal("unchanged worktree was reread", err)
	}
	info, _ := os.Stat(path)
	os.WriteFile(path, []byte("other\n"), 0600)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	cache.checked = time.Now().Add(-31 * time.Second)
	changed, cache, err := pollWorktreeStatus(ctx, dir, cache)
	if err != nil || changed[0].worktreeVersion == files[0].worktreeVersion {
		t.Fatal("periodic check missed preserved timestamps", err)
	}
	git("add", ".")
	staged, next, err := pollWorktreeStatus(ctx, dir, cache)
	if err != nil || next == cache || staged[0].extra != "已暂存" {
		t.Fatal("index change was not reflected", err)
	}
}

func TestFileTreeCacheInvalidatesOnReplacementAndFilter(t *testing.T) {
	tab := newTab(1, "/unused")
	tab.compareFiles = []fileChange{{path: "dir/one.go"}, {path: "dir/two.go"}}
	tab.prepareFileTree()
	tab.fileFilter = "two"
	if tree := tab.prepareFileTree(); len(tree.nodes) != 2 {
		t.Fatalf("filter did not invalidate tree: %+v", tree.nodes)
	}
	tab.compareFiles = []fileChange{{path: "dir/new.go"}, {path: "dir/another.go"}}
	tab.fileFilter = "new"
	if tree := tab.prepareFileTree(); tree.nodes["dir/new.go"].dir || len(tree.nodes) != 2 {
		t.Fatal("replacement did not invalidate tree")
	}
}

func TestSidebarSummaryBatchOnlyChecksVisibleRepos(t *testing.T) {
	a := &app{dispatch: func(func()) {}}
	for i := range 500 {
		a.found = append(a.found, fmt.Sprintf("/repo/%d", i))
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.List(c, &a.repoListState, len(a.found), func(i int) { ui.Text(c, a.found[i]) }).FillHeight()
	}, 300, 200)
	tt.Frame()
	paths := a.visibleRepoPaths()
	if len(paths) == 0 || len(paths) > 32 {
		t.Fatalf("visible summary batch has %d paths", len(paths))
	}
	first, last := a.repoListState.Visible()
	if len(paths) != last-first+1 || paths[len(paths)-1] != a.found[last] {
		t.Fatal("summary batch omitted the last visible repository")
	}
	// An in-flight batch prevents additional workers, including repeated UI builds.
	a.summaryRunning = map[string]bool{"/busy": true}
	a.refreshRepoSummaries()
	if len(a.summaryRunning) != 1 {
		t.Fatal("overlapping summary workers")
	}
}

func TestTreeRefreshPreservesCollapsedDirectories(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		t.Run(fmt.Sprint(worktree), func(t *testing.T) {
			tab := newTab(1, "/unused")
			tab.worktree = worktree
			files := []fileChange{{status: "M", path: "dir/a.go"}, {status: "M", path: "dir/b.go"}}
			tab.compareFiles = files
			tab.prepareFileTree()
			tab.fileOutline.Open.Clear()
			tab.compareFiles = append([]fileChange(nil), files...)
			if worktree {
				tab.compareFiles[0].worktreeVersion = "new contents"
			} else {
				tab.compareFiles[0].added = 42
			}
			tab.prepareFileTree()
			if tab.fileOutline.Open.Has("dir/") {
				t.Fatal("refresh reopened a collapsed directory")
			}
			tab.compareFiles = []fileChange{{status: "A", path: "new/a.go"}}
			if _, ok := tab.prepareFileTree().nodes["new/a.go"]; !ok {
				t.Fatal("structural change was ignored")
			}
		})
	}
}

func TestFileTabViewRestoresEvictedSnapshotOnce(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprint(full), func(t *testing.T) {
			path, err := writeFileTabSnapshot(fileTabSnapshot{Old: []byte("before\n"), New: []byte("snapshot\n"), Full: "snapshot\n"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Remove(path) })
			repo := newTab(1, "/unused")
			snapshot := &fileDiffTab{file: fileChange{path: "a.txt", status: "M"}, fullFile: full, backing: path, evicted: true}
			repo.fileTabs, repo.contentTab = []*fileDiffTab{snapshot}, 1
			updates := make(chan func(), 8)
			a := &app{tabs: []*repoTab{repo}, dispatch: func(fn func()) { updates <- fn }}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { a.fileTabPane(c, repo, snapshot) })
			}, 1100, 720)
			gen := snapshot.loadGen
			tt.Frame()
			tt.Frame()
			if snapshot.loadGen != gen {
				t.Fatal("extra frames started duplicate snapshot restores")
			}
			waitUpdates(t, updates, func() bool { return snapshot.diff != nil || snapshot.fullContent.Buffer != nil })
			tt.Frame()
			tt.Frame()
			if full {
				if snapshot.fullContent.Buffer.String() != "snapshot\n" {
					t.Fatal("full-file view lost snapshot")
				}
			} else if snapshot.diff.New.String() != "snapshot\n" {
				t.Fatal("diff view lost snapshot")
			}
			if snapshot.loadGen != gen || snapshot.evicted {
				t.Fatal("visible snapshot was evicted or restored repeatedly")
			}
		})
	}
}

func TestSpillKeepsVisibleFileTab(t *testing.T) {
	repo := newTab(1, "/unused")
	visible := &fileDiffTab{source: &fileDiffSource{new: []byte("visible")}}
	other := &fileDiffTab{source: &fileDiffSource{new: []byte("background")}}
	repo.fileTabs, repo.contentTab = []*fileDiffTab{visible, other}, 1
	updates := make(chan func(), 8)
	a := &app{tabs: []*repoTab{repo}, dispatch: func(fn func()) { updates <- fn }}
	t.Cleanup(func() { a.closeAllFileTabs(repo) })
	a.spillFileTabs(repo)
	waitUpdates(t, updates, func() bool { return other.evicted })
	if visible.evicted || visible.source == nil || visible.backing != "" {
		t.Fatal("spill discarded the visible file tab")
	}
}

func BenchmarkCachedFileTree(b *testing.B) {
	tab := newTab(1, "/unused")
	for i := range 50000 {
		tab.compareFiles = append(tab.compareFiles, fileChange{path: fmt.Sprintf("src/dir%05d/file%05d.go", i/10, i)})
	}
	tab.prepareFileTree()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		tab.prepareFileTree()
	}
}

func BenchmarkUnchangedWorktreePoll(b *testing.B) {
	dir := b.TempDir()
	// Use a real repository and large dirty files, matching the review workload.
	ctx := context.Background()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"config", "commit.gpgsign", "false"}} {
		if _, err := runGit(ctx, dir, args...); err != nil {
			b.Fatal(err)
		}
	}
	data := []byte(strings.Repeat("a", (1<<20)-1) + "\n")
	for i := range 20 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), data, 0600)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "base"}} {
		if _, err := runGit(ctx, dir, args...); err != nil {
			b.Fatal(err)
		}
	}
	data[0] = 'b'
	for i := range 20 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), data, 0600)
	}
	_, cache, err := pollWorktreeStatus(ctx, dir, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := pollWorktreeStatus(ctx, dir, cache); err != nil {
			b.Fatal(err)
		}
	}
}
