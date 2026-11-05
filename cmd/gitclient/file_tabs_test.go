package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestFileTabsKeepVersionsAndHistorySelection(t *testing.T) {
	dir, _ := renameHistoryRepo(t)
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 0)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	a.openFileTab(tab)
	first := tab.fileTabs[0]
	if first.diff.Old.String() != "two\n" || first.diff.New.Len() != 0 {
		t.Fatal("delete snapshot has the wrong contents")
	}
	tab.contentTab = 0
	a.selectCommit(tab, 1)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	for i, f := range tab.compareFiles {
		if f.path == "新名.txt" {
			tab.fileSel = i
			a.loadFileDiff(tab, i)
			break
		}
	}
	waitUpdates(t, updates, func() bool { return !tab.diffLoading })
	a.openFileTab(tab)
	second := tab.fileTabs[1]
	if second.file.path != first.file.path || second.to == first.to || second.diff.New.String() != "two\n" || first.diff.New.Len() != 0 {
		t.Fatal("opening a second revision overwrote the first snapshot")
	}
	a.openFileTab(tab)
	if len(tab.fileTabs) != 2 || tab.contentTab != 2 {
		t.Fatal("opening the same file and revision should select its existing tab")
	}
	selected, diff := tab.inspecting, tab.diff
	for _, size := range [][2]int{{1200, 800}, {860, 560}} {
		tt := ui.NewTester(a.view, size[0], size[1])
		tt.SetTitleBar(ui.TitleBar{Height: 40, Left: 72, Right: 12})
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame()
			tt.Frame()
			if tt.HasText("图谱") || !tt.HasText("新版本 · "+shortHash(second.to)) {
				t.Fatal("file tab did not replace the history area")
			}
			for _, text := range []string{"提交历史", "关闭标签页", "复制路径"} {
				r, ok := tt.Find(text)
				if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(size[0])+1 || r.Y+r.H > float32(size[1])+1 {
					t.Fatalf("%q is outside the window: %+v", text, r)
				}
			}
			if err := tt.Click("提交历史"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !tt.HasText("图谱") || tab.inspecting != selected || tab.diff != diff {
				t.Fatal("returning to history lost its selection or diff")
			}
			if err := tt.Click(filepath.Base(second.file.path) + " · " + shortHash(second.to)); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if tab.contentTab != 2 || tab.fileTabs[1].diff != second.diff {
				t.Fatal("switching tabs replaced the retained diff state")
			}
		}
	}
	// Repository cache eviction must leave explicitly opened snapshots readable.
	tab.evictHistory()
	if len(tab.fileTabs) != 2 || first.diff.Old.String() != "two\n" || second.diff.New.String() != "two\n" || tab.cachedBytes() == 0 {
		t.Fatal("history eviction discarded or stopped counting file snapshots")
	}
	tt := ui.NewTester(a.view, 860, 560)
	tt.Frame()
	tt.Key(commandModifier(), ui.KeyW)
	tt.Frame()
	if len(tab.fileTabs) != 1 || tab.contentTab != 0 || len(a.tabs) != 1 || tab.closed {
		t.Fatal("Cmd/Ctrl+W should close the file tab and return to history")
	}
}

func TestFileTabsFreezeWorktreeContents(t *testing.T) {
	dir, git := initTestRepo(t)
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("committed\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")
	os.WriteFile(path, []byte("first snapshot\n"), 0o600)
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.showWorktree(tab)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	a.openFileTab(tab)
	a.openFileTab(tab)
	if len(tab.fileTabs) != 1 {
		t.Fatal("reopening the loaded worktree result created a duplicate")
	}
	first := tab.fileTabs[0]
	os.WriteFile(path, []byte("second snapshot\n"), 0o600)
	a.loadFileDiff(tab, tab.fileSel)
	waitUpdates(t, updates, func() bool { return !tab.diffLoading })
	a.openFileTab(tab)
	if len(tab.fileTabs) != 2 || first.diff.New.String() != "first snapshot\n" || tab.fileTabs[1].diff.New.String() != "second snapshot\n" {
		t.Fatal("refreshing the worktree changed the pinned snapshot")
	}
	a.closeFileTab(tab, 0)
	if tab.contentTab != 1 || tab.fileTabs[0].diff.New.String() != "second snapshot\n" {
		t.Fatal("closing an earlier tab changed the active snapshot")
	}
	tt := ui.NewTester(a.view, 860, 560)
	tt.Frame()
	if err := tt.Click("关闭标签页"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(tab.fileTabs) != 0 || tab.contentTab != 0 || !tt.HasText("图谱") {
		t.Fatal("closing the last file tab did not return to history")
	}
}

func TestFileTabOverflowKeepsActiveTabVisible(t *testing.T) {
	tab := newTab(1, "/repo")
	tab.loaded = true
	a := &app{tabs: []*repoTab{tab}}
	for i := range 10 {
		tab.diffTo = [20]byte{byte(i + 1)}
		tab.compareFiles = []fileChange{{path: fmt.Sprintf("directory/long-file-name-%d.txt", i), status: "M"}}
		tab.fileSel = 0
		tab.diff, tab.simpleDiff = prepareDiff([]byte("before\n"), []byte("after\n"), "file.txt")
		a.openFileTab(tab)
	}
	tt := ui.NewTester(a.view, 860, 560)
	visible := func(text string) {
		t.Helper()
		tt.Frame()
		tt.Frame()
		r, ok := tt.Find(text)
		if !ok || r.X < 0 || r.X+r.W > 861 || r.W == 0 {
			t.Fatalf("active tab %d is outside the visible strip: %q %+v", tab.contentTab, text, r)
		}
	}
	last := "long-file-name-9.txt · " + shortHash(tab.diffTo)
	visible(last)
	if err := tt.Click(last); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyHome)
	visible("提交历史")
	if tab.contentTab != 0 {
		t.Fatal("Home did not return to the history tab")
	}
	tt.Key(0, ui.KeyEnd)
	visible(last)
	if tab.contentTab != 10 {
		t.Fatal("End did not select the last file tab")
	}
}

func TestFileTabWheelScrolling(t *testing.T) {
	tab := newTab(1, "/repo")
	tab.loaded = true
	a := &app{tabs: []*repoTab{tab}}
	for i := range 10 {
		tab.diffTo = [20]byte{byte(i + 1)}
		tab.compareFiles = []fileChange{{path: fmt.Sprintf("directory/long-file-name-%d.txt", i), status: "M"}}
		tab.fileSel = 0
		tab.diff, tab.simpleDiff = prepareDiff([]byte("before\n"), []byte("after\n"), "file.txt")
		a.openFileTab(tab)
	}
	tt := ui.NewTester(a.view, 860, 560)
	tt.Frame()
	tab.contentTab = 0
	tab.revealFileTab = true
	tt.Frame()
	tt.Frame()

	initX := tab.fileTabsScroll.X
	r, ok := tt.Find("提交历史")
	if !ok {
		t.Fatal("missing 提交历史")
	}
	// Scroll vertically with wheel over the tab bar
	tt.Wheel(r.X+10, r.Y+10, 0, 100)
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if tab.fileTabsScroll.X <= initX {
		t.Fatalf("wheel dy should have scrolled file tabs horizontally: before=%v after=%v", initX, tab.fileTabsScroll.X)
	}
}

func TestFileTabContextMenu(t *testing.T) {
	tab := newTab(1, "/repo")
	tab.loaded = true
	a := &app{tabs: []*repoTab{tab}}
	for i := range 3 {
		tab.diffTo = [20]byte{byte(i + 1)}
		tab.compareFiles = []fileChange{{path: fmt.Sprintf("file-%d.txt", i), status: "M"}}
		tab.fileSel = 0
		tab.diff, tab.simpleDiff = prepareDiff([]byte("before\n"), []byte("after\n"), fmt.Sprintf("file-%d.txt", i))
		a.openFileTab(tab)
	}
	tt := ui.NewTester(a.view, 860, 560)
	tt.Frame()

	// Right click on second file tab ("file-1.txt")
	if err := tt.RightClick("file-1.txt"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	if menu == nil {
		t.Fatal("expected context menu on file tab")
	}
	wantItems := []string{"关闭", "关闭其他", "关闭右侧标签页", "全部关闭", "-", "复制相对路径", "复制绝对路径", "-", "查看文件历史", "使用默认编辑器打开"}
	for _, item := range wantItems {
		if !slices.Contains(menu, item) {
			t.Fatalf("menu %v missing %q", menu, item)
		}
	}

	// Test "关闭右侧标签页"
	if err := tt.ChooseMenuItem("关闭右侧标签页"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(tab.fileTabs) != 2 {
		t.Fatalf("expected 2 file tabs after closing right, got %d", len(tab.fileTabs))
	}
	if tab.fileTabs[0].file.path != "file-0.txt" || tab.fileTabs[1].file.path != "file-1.txt" {
		t.Fatalf("unexpected tabs remaining: %+v", tab.fileTabs)
	}

	// Test "复制相对路径"
	if err := tt.RightClick("file-1.txt"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("复制相对路径"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.Clipboard() != "file-1.txt" {
		t.Fatalf("expected clipboard file-1.txt, got %q", tt.Clipboard())
	}

	// Right click on "file-1.txt", test "关闭其他"
	if err := tt.RightClick("file-1.txt"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("关闭其他"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(tab.fileTabs) != 1 || tab.fileTabs[0].file.path != "file-1.txt" {
		t.Fatalf("expected only file-1.txt remaining, got %d", len(tab.fileTabs))
	}

	// Right click on "提交历史", test "关闭全部文件标签页"
	if err := tt.RightClick("提交历史"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("关闭全部文件标签页"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(tab.fileTabs) != 0 || tab.contentTab != 0 {
		t.Fatalf("expected all file tabs closed, got len=%d contentTab=%d", len(tab.fileTabs), tab.contentTab)
	}
}

func TestWorktreeSnapshotSurvivesWhitespaceToggle(t *testing.T) {
	for _, ignoreAtOpen := range []bool{false, true} {
		t.Run(fmt.Sprint(ignoreAtOpen), func(t *testing.T) {
			dir, git := initTestRepo(t)
			path := filepath.Join(dir, "a.txt")
			os.WriteFile(path, []byte("base\n"), 0o600)
			git("add", ".")
			git("commit", "-m", "base")
			os.WriteFile(path, []byte("snapshot  \n"), 0o600)
			tab := newTab(1, dir)
			updates := make(chan func(), 64)
			a := &app{tabs: []*repoTab{tab}, diffIgnoreWhitespace: ignoreAtOpen, dispatch: func(fn func()) { updates <- fn }}
			defer tab.cancel()
			a.reload(tab)
			waitUpdates(t, updates, func() bool { return !tab.loading })
			a.showWorktree(tab)
			waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
			a.openFileTab(tab)
			snapshot := tab.fileTabs[0]
			opened := snapshot.openedAt
			os.WriteFile(path, []byte("later edit\n"), 0o600)
			tt := ui.NewTester(a.view, 1100, 720)
			for range 2 {
				tt.Frame()
				if err := tt.Click("忽略空白"); err != nil {
					t.Fatal(err)
				}
				waitUpdates(t, updates, func() bool { return snapshot.note != "正在读取改动…" })
				if snapshot.diff == nil || !strings.Contains(snapshot.diff.New.String(), "snapshot") || snapshot.openedAt != opened {
					t.Fatalf("snapshot replaced: diff=%v note=%q", snapshot.diff != nil, snapshot.note)
				}
			}
		})
	}
}

func TestFileTabIgnoresObsoleteWhitespaceResults(t *testing.T) {
	tab := newTab(1, "/repo")
	updates := make(chan func(), 2)
	a := &app{dispatch: func(fn func()) { updates <- fn }}
	snapshot := &fileDiffTab{file: fileChange{path: "a.txt", status: "M"}, source: &fileDiffSource{old: []byte("a b\n"), new: []byte("ab\n")}, ignoreWhitespace: true}
	a.reloadFileTab(tab, snapshot)
	oldResult := <-updates
	snapshot.ignoreWhitespace = false
	a.reloadFileTab(tab, snapshot)
	newResult := <-updates
	newResult()
	oldResult()
	if snapshot.diff == nil || snapshot.diff.New.String() != "ab\n" || snapshot.note != "" {
		t.Fatalf("obsolete whitespace result replaced current diff: note=%q", snapshot.note)
	}
}
