package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestCommitScrollTopButtonDoesNotSelectOrOpenDiff(t *testing.T) {
	dir, git := initTestRepo(t)
	for i := range 40 {
		git("commit", "--allow-empty", "-m", fmt.Sprintf("commit-%02d", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading && tab.hasWorktreeRow() })

	tt := ui.NewTester(a.view, 1100, 560)
	defer retireTester(tt)
	tt.Frame()

	// At the top of the list, the scroll-to-top button should stay hidden.
	if _, ok := tt.Find(tr("回到顶部")); ok {
		t.Fatal("scroll-to-top button should not appear at the top of the list")
	}

	// Scroll down without selecting anything; button should appear.
	tab.list.ScrollTo(commitScrollTopThreshold+2, ui.Start)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)

	table, ok := tt.AccessByRole(ui.RoleTable, "")
	if !ok {
		t.Fatal("commit table is missing")
	}
	// The bottom-right rows must still receive wheel input while the button is visible.
	beforeWheel, _ := tab.list.Visible()
	tt.Wheel(table.Bounds.X+table.Bounds.W-33, table.Bounds.Y+table.Bounds.H-29, 0, 120)
	tt.Advance(500 * time.Millisecond)
	if afterWheel, _ := tab.list.Visible(); afterWheel <= beforeWheel {
		t.Fatalf("wheel at the bottom right did not scroll: before=%d after=%d", beforeWheel, afterWheel)
	}

	// Clicking the button scrolls back to row 0 without selecting row 0 or opening the diff pane.
	if err := tt.Click(tr("回到顶部")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)

	first, _ := tab.list.Visible()
	if first != 0 {
		t.Fatalf("top visible row = %d, want 0", first)
	}
	if tab.selected != -1 || tab.selection.Len() != 0 {
		t.Fatalf("selection should stay empty, got selected=%d len=%d", tab.selected, tab.selection.Len())
	}
	if tab.worktree || tab.inspecting != zeroHash || a.hasInspectBottom(tab) {
		t.Fatalf("bottom diff pane should stay closed: worktree=%v inspecting=%x", tab.worktree, tab.inspecting)
	}
	if _, ok := tt.Find(tr("回到顶部")); ok {
		t.Fatal("scroll-to-top button should hide after returning to top")
	}
}

func TestCommitScrollTopButtonKeepsExistingSelection(t *testing.T) {
	dir, git := initTestRepo(t)
	for i := range 25 {
		git("commit", "--allow-empty", "-m", fmt.Sprintf("commit-%02d", i))
	}

	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })

	tt := ui.NewTester(a.view, 1100, 560)
	defer retireTester(tt)
	tt.Frame()

	// Shrink the history pane to the height that previously let the button overlap the header.
	a.selectCommit(tab, 15)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	wantHash := tab.commits[15].hash
	tab.detailSplit = 10
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tab.list.ScrollTo(commitScrollTopThreshold+2, ui.Start)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)

	btnRect, ok := tt.Find(tr("回到顶部"))
	if !ok {
		t.Fatal("scroll-to-top button should appear after scrolling down")
	}
	header, ok := tt.AccessByRole(ui.RoleColumnHeader, tr("哈希"))
	if !ok || btnRect.Y+btnRect.H > header.Bounds.Y {
		t.Fatalf("button should stay above the commit table header: button=%+v header=%+v", btnRect, header.Bounds)
	}

	tt.SetFocused(true)
	for i := 0; i < 80 && !tt.Focused(tr("回到顶部")); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused(tr("回到顶部")) {
		t.Fatal("scroll-to-top button should be reachable with Tab")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Advance(300 * time.Millisecond)

	first, _ := tab.list.Visible()
	if first != 0 {
		t.Fatalf("top visible row = %d, want 0", first)
	}
	if tab.selected != 15 || !tab.selection.Has(wantHash) || tab.inspecting != wantHash {
		t.Fatalf("existing selection should be preserved: selected=%d inspecting=%x want=%x", tab.selected, tab.inspecting, wantHash)
	}
	if _, ok := tt.Find(tr("回到顶部")); ok {
		t.Fatal("scroll-to-top button should hide after returning to top")
	}
	tt.Key(0, ui.KeyDown)
	if tab.selected != 16 {
		t.Fatalf("Down should move to the next commit after returning to top, selected=%d", tab.selected)
	}
}
