package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestBuildRepoGroups(t *testing.T) {
	// 1. Empty items
	if groups := buildRepoGroups(nil); len(groups) != 0 {
		t.Fatalf("expected 0 groups, got %d", len(groups))
	}

	// 2. Multiple standalone repositories
	flat := []string{"/work/xylring", "/work/snapover", "/work/api"}
	flatGroups := buildRepoGroups(flat)
	if len(flatGroups) != 3 {
		t.Fatalf("expected 3 flat groups, got %d", len(flatGroups))
	}
	for i, p := range flat {
		if flatGroups[i].root != p || len(flatGroups[i].children) != 0 {
			t.Fatalf("unexpected group[%d]: %+v", i, flatGroups[i])
		}
	}

	// 3. Hierarchical / nested repositories
	items := []string{
		"/work/SRC",
		"/work/SRC/bapi",
		"/work/SRC/fapi",
		"/work/网关",
		"/work/网关/step2",
		"/work/xylring",
		"/work/snapover",
	}
	groups := buildRepoGroups(items)
	if len(groups) != 4 {
		t.Fatalf("expected 4 groups, got %d", len(groups))
	}

	// Check Group 0: SRC + 2 children
	if groups[0].root != "/work/SRC" || !slices.Equal(groups[0].children, []string{"/work/SRC/bapi", "/work/SRC/fapi"}) {
		t.Fatalf("group 0 mismatch: %+v", groups[0])
	}
	// Check Group 1: 网关 + 1 child
	if groups[1].root != "/work/网关" || !slices.Equal(groups[1].children, []string{"/work/网关/step2"}) {
		t.Fatalf("group 1 mismatch: %+v", groups[1])
	}
	// Check Group 2: xylring (standalone)
	if groups[2].root != "/work/xylring" || len(groups[2].children) != 0 {
		t.Fatalf("group 2 mismatch: %+v", groups[2])
	}
	// Check Group 3: snapover (standalone)
	if groups[3].root != "/work/snapover" || len(groups[3].children) != 0 {
		t.Fatalf("group 3 mismatch: %+v", groups[3])
	}
}

func TestOrderRepoItems(t *testing.T) {
	a := newApp()
	items := []string{
		"/work/SRC",
		"/work/SRC/bapi",
		"/work/SRC/fapi",
		"/work/网关",
		"/work/网关/step2",
		"/work/xylring",
		"/work/snapover",
	}

	// 1. Without custom order: preserves original layout
	ordered := a.orderRepoItems(items)
	if !slices.Equal(ordered, items) {
		t.Fatalf("expected unchanged order, got:\nwant: %v\ngot:  %v", items, ordered)
	}

	// 2. With custom order: moves entire groups (with children intact)
	a.repoOrder = []string{"/work/snapover", "/work/网关", "/work/SRC", "/work/xylring"}
	wantOrdered := []string{
		"/work/snapover",
		"/work/网关",
		"/work/网关/step2",
		"/work/SRC",
		"/work/SRC/bapi",
		"/work/SRC/fapi",
		"/work/xylring",
	}
	ordered = a.orderRepoItems(items)
	if !slices.Equal(ordered, wantOrdered) {
		t.Fatalf("expected custom reordered items, got:\nwant: %v\ngot:  %v", wantOrdered, ordered)
	}

	// 3. New repository not in repoOrder appears at end
	itemsWithNew := append(items, "/work/brand-new")
	ordered = a.orderRepoItems(itemsWithNew)
	wantWithNew := append(wantOrdered, "/work/brand-new")
	if !slices.Equal(ordered, wantWithNew) {
		t.Fatalf("expected new repository at end, got:\nwant: %v\ngot:  %v", wantWithNew, ordered)
	}
}

func TestReorderRepos(t *testing.T) {
	a := newApp()
	items := []string{
		"/work/SRC",
		"/work/SRC/bapi",
		"/work/SRC/fapi",
		"/work/网关",
		"/work/网关/step2",
		"/work/xylring",
		"/work/snapover",
	}

	// 1. Dragging a child repo (nestLevel > 0) is ignored
	a.reorderRepos(items, []int{1}, 0) // items[1] == "/work/SRC/bapi"
	if len(a.repoOrder) != 0 {
		t.Fatalf("dragging child repo should not change repoOrder: %v", a.repoOrder)
	}

	// 2. Dragging last top-level item (/work/snapover, row 6) to the top (to=0)
	a.reorderRepos(items, []int{6}, 0)
	wantOrder := []string{"/work/snapover", "/work/SRC", "/work/网关", "/work/xylring"}
	if !slices.Equal(a.repoOrder, wantOrder) {
		t.Fatalf("expected snapover at top, got: %v", a.repoOrder)
	}

	// Items should reflect the new order
	orderedItems := a.orderRepoItems(items)
	wantItems := []string{
		"/work/snapover",
		"/work/SRC",
		"/work/SRC/bapi",
		"/work/SRC/fapi",
		"/work/网关",
		"/work/网关/step2",
		"/work/xylring",
	}
	if !slices.Equal(orderedItems, wantItems) {
		t.Fatalf("orderRepoItems mismatch:\nwant: %v\ngot:  %v", wantItems, orderedItems)
	}

	// 3. Dragging top-level item (/work/SRC, which has 2 children) to the end
	// In orderedItems: snapover is at 0, SRC is at 1 (starts at row 1).
	a.reorderRepos(orderedItems, []int{1}, len(orderedItems))
	wantOrderAfterEnd := []string{"/work/snapover", "/work/网关", "/work/xylring", "/work/SRC"}
	if !slices.Equal(a.repoOrder, wantOrderAfterEnd) {
		t.Fatalf("expected SRC at end, got: %v", a.repoOrder)
	}
	orderedItems = a.orderRepoItems(items)
	wantItemsAfterEnd := []string{
		"/work/snapover",
		"/work/网关",
		"/work/网关/step2",
		"/work/xylring",
		"/work/SRC",
		"/work/SRC/bapi",
		"/work/SRC/fapi",
	}
	if !slices.Equal(orderedItems, wantItemsAfterEnd) {
		t.Fatalf("orderRepoItems after end drag mismatch:\nwant: %v\ngot:  %v", wantItemsAfterEnd, orderedItems)
	}

	// 4. Dropping inside another group snaps cleanly to group boundary
	// Reset to original items
	a.repoOrder = nil
	// Drag snapover (row 6) and drop into SRC's children (to=1, inside SRC [0..2])
	a.reorderRepos(items, []int{6}, 1)
	// to=1 <= mid(1), snaps before SRC (toGroup=0)
	if a.repoOrder[0] != "/work/snapover" {
		t.Fatalf("expected snapover snapped before SRC, got: %v", a.repoOrder)
	}
}

func TestRepoSidebarDragReorderUI(t *testing.T) {
	tab := newTab(1, "/tmp/work/alpha")
	tab.loaded = true
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/alpha", "/tmp/work/alpha/child", "/tmp/work/beta"},
		foundFrom: "/tmp/work",
	}

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// Initially alpha is first, beta is second
	if !tt.HasText("alpha") || !tt.HasText("child") || !tt.HasText("beta") {
		t.Fatalf("missing text in sidebar: %q", tt.Texts())
	}
	items := a.repoItems()
	if !slices.Equal(items, []string{"/tmp/work/alpha", "/tmp/work/alpha/child", "/tmp/work/beta"}) {
		t.Fatalf("initial items unexpected: %v", items)
	}

	// Verify Reorder callback exists on repoListState
	if a.repoListState.Reorder == nil {
		t.Fatal("expected repoListState.Reorder to be registered")
	}

	// Simulate dragging beta (row 2) to before alpha (to 0)
	a.repoListState.Reorder([]int{2}, 0)
	tt.Frame()

	// New items should have beta first, followed by alpha and its child
	newItems := a.repoItems()
	want := []string{"/tmp/work/beta", "/tmp/work/alpha", "/tmp/work/alpha/child"}
	if !slices.Equal(newItems, want) {
		t.Fatalf("reordered items mismatch:\nwant: %v\ngot:  %v", want, newItems)
	}
}
