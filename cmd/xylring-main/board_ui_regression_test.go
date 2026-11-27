package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestBoardCardChecklistKeepsExternalDescriptionEdits covers a card click that
// arrives after another client edited the same task: the marker is flipped in
// the stored text instead of replacing it with the card's old copy.
func TestBoardCardChecklistKeepsExternalDescriptionEdits(t *testing.T) {
	withTempBoardConfig(t)
	bf, task, err := boardAddTask("Concurrent audit", "- [ ] This is the original item", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{showBoard: true, board: boardState{loaded: true, file: bf}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()
	edited := "An external edit that must survive\n- [ ] This is the original item"
	if _, _, err := boardUpdateTask(task.ID, boardTaskPatch{Description: &edited}); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("This is the original item"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	saved, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if !hasSubstr(saved.Tasks[0].Description, "An external edit") {
		t.Fatalf("external text lost, saved %q", saved.Tasks[0].Description)
	}
	if !hasSubstr(saved.Tasks[0].Description, "- [x] This is the original item") {
		t.Fatalf("click did not flip the marker, saved %q", saved.Tasks[0].Description)
	}
}

// TestBoardDrawerChecklistUsesDraftLines covers a draft that inserted a line in
// the drawer: the checklist the user clicks and the write-back use that draft,
// so the item flipped is the one under the pointer, and the marker is written
// once.
func TestBoardDrawerChecklistUsesDraftLines(t *testing.T) {
	withTempBoardConfig(t)
	bf, task, err := boardAddTask("Draft audit", "- [ ] Alpha item\n- [ ] Beta item", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{showBoard: true, board: boardState{loaded: true, file: bf}}
	a.selectBoardTask(task.ID)
	tt := ui.NewTester(a.view, 1280, 900)
	defer retireTester(tt)
	tt.Frame()
	field, ok := tt.AccessByRole(ui.RoleTextField, "任务描述")
	if !ok {
		t.Fatal("description field not found")
	}
	tt.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tt.Frame()
	a.board.descDraft = "- [ ] Added item\n- [ ] Alpha item\n- [ ] Beta item"
	tt.Frame()
	clicked := false
	for _, elem := range tt.AccessAll() {
		if elem.Name == "Beta item" && elem.Bounds.X > 640 {
			tt.ClickAt(elem.Bounds.X+elem.Bounds.W/2, elem.Bounds.Y+elem.Bounds.H/2)
			clicked = true
			break
		}
	}
	if !clicked {
		t.Fatalf("drawer checklist button not found: %q", tt.Texts())
	}
	tt.Frame()
	saved, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	desc := saved.Tasks[0].Description
	if !hasSubstr(desc, "- [x] Beta item") {
		t.Fatalf("expected Beta checked in the saved text, got %q", desc)
	}
	if hasSubstr(desc, "- [x] Alpha item") {
		t.Fatalf("wrong checklist line flipped, saved %q", desc)
	}
	if !hasSubstr(a.board.descDraft, "- [ ] Added item") || !hasSubstr(a.board.descDraft, "- [x] Beta item") {
		t.Fatalf("draft lost the inserted line or the flip: %q", a.board.descDraft)
	}
}

// TestBoardRefreshFollowsHighlightAndRelativeTime covers the two things that
// make a still board go stale: an external-update highlight ending, and a
// relative time moving into the next minute.
func TestBoardRefreshFollowsHighlightAndRelativeTime(t *testing.T) {
	highlight := time.Now().Add(-11 * time.Second)
	a := &app{showBoard: true, board: boardState{recentUpdated: map[string]time.Time{"T-1": highlight}}}
	if got, want := a.boardNextRefreshAt(time.Now(), &boardWallLayout{}), highlight.Add(12*time.Second); !got.Equal(want) {
		t.Fatalf("highlight repaint at %v, want %v", got, want)
	}
	// Past the twelve seconds, a board without relative times has no reason to
	// repaint itself.
	expired := highlight.Add(13 * time.Second)
	if got := a.boardNextRefreshAt(expired, &boardWallLayout{}); !got.IsZero() {
		t.Fatalf("expired highlight still asks for a repaint at %v", got)
	}
	// "Just now" becomes "1 min" when the minute turns.
	quiet := &app{showBoard: true}
	if got, want := quiet.boardNextRefreshAt(expired, &boardWallLayout{relative: true}), expired.Truncate(time.Minute).Add(time.Minute); !got.Equal(want) {
		t.Fatalf("relative time repaint at %v, want %v", got, want)
	}
	// A closed or hidden board keeps no timer running.
	hidden := &app{board: boardState{recentUpdated: map[string]time.Time{"T-1": time.Now()}}}
	hidden.boardScheduleRefresh(&boardWallLayout{})
	if hidden.board.refreshPending {
		t.Fatal("a hidden board scheduled a repaint")
	}
}

// TestBoardDrawerChecklistTogglesADraftOnlyItem covers an item typed into the
// drawer but not saved yet: ticking it stays in the draft instead of failing
// as if another program had changed the task.
func TestBoardDrawerChecklistTogglesADraftOnlyItem(t *testing.T) {
	withTempBoardConfig(t)
	bf, task, err := boardAddTask("Draft only", "- [ ] Alpha item", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{showBoard: true, board: boardState{loaded: true, file: bf}}
	a.selectBoardTask(task.ID)
	a.board.descDraft = "- [ ] Added item\n- [ ] Alpha item"
	item, ok := findBoardChecklistItem(parseBoardChecklist(a.board.descDraft), 0, "Added item")
	if !ok {
		t.Fatal("draft item not found")
	}
	a.toggleBoardChecklistItem(task.ID, item)
	if a.banner != "" {
		t.Fatalf("ticking a draft item reported an error: %q", a.banner)
	}
	if !hasSubstr(a.board.descDraft, "- [x] Added item") || !hasSubstr(a.board.descDraft, "- [ ] Alpha item") {
		t.Fatalf("draft lost the flip: %q", a.board.descDraft)
	}
	saved, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if desc := saved.Tasks[0].Description; desc != "- [ ] Alpha item" {
		t.Fatalf("a draft-only item reached the file: %q", desc)
	}
}

func TestBoardCardActionsAndFilterStatesAreExposed(t *testing.T) {
	task := boardTask{
		ID: "T-1", Title: "Semantic audit", Status: boardTodo,
		Description: "- [x] Checked item\n- [ ] Open item",
	}
	a := &app{showBoard: true, board: boardState{loaded: true, doneExpanded: true, file: boardFile{Tasks: []boardTask{task}}}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Advance(time.Second)
	tt.Frame()
	for name, want := range map[string]string{
		"Checked item": "checked",
		"Open item":    "unchecked",
		"全部未完成":        "selected",
		"已完成":          "expanded",
		"标记为已完成":       "action",
		"复制 Agent 上下文": "action",
	} {
		elem, ok := tt.Access(name)
		if !ok {
			t.Fatalf("missing %q in the accessibility tree: %q", name, tt.Texts())
		}
		switch want {
		case "checked":
			if !elem.Checked {
				t.Fatalf("%q does not say it is checked: %+v", name, elem)
			}
		case "unchecked":
			if elem.Checked {
				t.Fatalf("%q says it is checked: %+v", name, elem)
			}
		case "selected":
			if !elem.Selected {
				t.Fatalf("%q does not expose the selected state: %+v", name, elem)
			}
		case "expanded":
			if !elem.Expanded {
				t.Fatalf("%q does not expose the expanded state: %+v", name, elem)
			}
		}
	}
}

func TestBoardRestoreFromDoneClearsArchive(t *testing.T) {
	withTempBoardConfig(t)
	_, task, err := boardAddTask("Old archived task", "", boardDone, nil)
	if err != nil {
		t.Fatal(err)
	}
	archived := true
	bf, _, err := boardUpdateTask(task.ID, boardTaskPatch{Archived: &archived})
	if err != nil {
		t.Fatal(err)
	}
	a := &app{showBoard: true, board: boardState{loaded: true, file: bf, doneExpanded: true, showArchived: true}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()
	if err := tt.Click("移回待办"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	saved, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Tasks[0].Archived {
		t.Fatalf("restored task is still archived, so the default board hides it: %+v", saved.Tasks[0])
	}
	if saved.Tasks[0].Status != boardTodo {
		t.Fatalf("restored task status = %q", saved.Tasks[0].Status)
	}
	if !boardMatchesFilter(saved.Tasks[0], "", "", false) {
		t.Fatalf("restored task is not visible on the default board: %+v", saved.Tasks[0])
	}
}

func TestBoardSearchShowsDoneMatchesWithoutChangingTheFold(t *testing.T) {
	task := boardTask{ID: "T-1", Title: "Completed search needle", Status: boardDone}
	a := &app{showBoard: true, board: boardState{loaded: true, file: boardFile{Tasks: []boardTask{task}}, search: "needle"}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText(task.Title) {
		t.Fatalf("matching done task is hidden by the collapsed section during search: %q", tt.Texts())
	}
	// The fold the user chose is untouched, so clearing the search hides it again.
	a.board.search = ""
	tt.Frame()
	if tt.HasText(task.Title) {
		t.Fatalf("clearing the search opened the completed fold: %q", tt.Texts())
	}
	if a.board.doneExpanded {
		t.Fatal("search changed the stored collapse preference")
	}
}

func TestBoardFilteredStatusHasOneAccurateEmptyState(t *testing.T) {
	review := boardTask{ID: "T-1", Title: "Found match in review", Status: boardReview}
	done := boardTask{ID: "T-2", Title: "Found match done", Status: boardDone}
	for _, tc := range []struct {
		name   string
		tasks  []boardTask
		status boardStatus
		want   string
	}{
		{"status hides the match", []boardTask{review}, boardTodo, "没有匹配的任务"},
		{"completed match shows up", []boardTask{done}, boardTodo, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{showBoard: true, board: boardState{loaded: true, file: boardFile{Tasks: tc.tasks}, search: "Found", statusFilter: tc.status}}
			tt := ui.NewTester(a.view, 1280, 820)
			defer retireTester(tt)
			tt.Frame()
			if tc.want != "" {
				if !tt.HasText(tc.want) {
					t.Fatalf("no %q empty state: %q", tc.want, tt.Texts())
				}
				if tt.HasText("当前状态没有任务") || tt.HasText("暂无任务") {
					t.Fatalf("more than one empty state speaks for the board: %q", tt.Texts())
				}
			} else if !tt.HasText(done.Title) {
				t.Fatalf("completed match found by search is not shown: %q", tt.Texts())
			}
		})
	}
}

func TestBoardQuickCaptureConfirmsTaskNumberUnderSearch(t *testing.T) {
	withTempBoardConfig(t)
	a := &app{showBoard: true, board: boardState{loaded: true, search: "unrelated search"}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()
	tt.ClickAt(0.5, 0.5) // focus nothing, just a settled frame
	a.board.quickTitle = "New quick task"
	tt.Frame()
	a.submitQuickBoardTask()
	tt.Frame()
	bf, err := loadBoardFile()
	if err != nil || len(bf.Tasks) != 1 {
		t.Fatalf("expected the write, got %+v err=%v", bf, err)
	}
	expect := fmt.Sprintf(tr("已创建任务 %s"), bf.Tasks[0].ID)
	if !tt.HasText(expect) {
		t.Fatalf("no confirmation with the task number under a search that hides it: %q", tt.Texts())
	}
	if a.board.quickTitle != "" {
		t.Fatalf("quick input kept the title after a successful create: %q", a.board.quickTitle)
	}
}

// TestBoardFocusBarFitsNarrowWindows checks that every focus bar control is
// inside the viewport when the project sidebar takes a third of the window, in
// English and Chinese.
func TestBoardFocusBarFitsNarrowWindows(t *testing.T) {
	for _, english := range []bool{false, true} {
		for _, width := range []int{640, 760, 901, 960, 961, 1050} {
			t.Run(fmt.Sprintf("english=%v_width=%d", english, width), func(t *testing.T) {
				previous := englishUI.Swap(english)
				defer englishUI.Store(previous)
				tab := newTab(1, "/tmp/audit-narrow-repo")
				defer tab.cancel()
				a := &app{showBoard: true, tabs: []*repoTab{tab}, repoSplit: 320, board: boardState{loaded: true}}
				tt := ui.NewTester(a.view, width, 760)
				defer retireTester(tt)
				tt.Frame()
				for _, name := range []string{"全部未完成", "待审阅", "进行中", "待办"} {
					bounds, ok := tt.Find(tr(name))
					if !ok || bounds.X < 0 || bounds.X+bounds.W > float32(width)+0.5 {
						t.Fatalf("status %q outside the window at width %d: %+v present=%v", tr(name), width, bounds, ok)
					}
				}
				for _, name := range []string{"默认排序", "快速新建任务"} {
					elem, ok := tt.Access(tr(name))
					if ok && (elem.Bounds.X < 0 || elem.Bounds.X+elem.Bounds.W > float32(width)+0.5) {
						t.Fatalf("control %q outside the window at width %d: %+v", tr(name), width, elem.Bounds)
					}
				}
			})
		}
	}
}

// TestBoardCardDropReordersManualOrder covers the card-to-card drop in the
// default sort, the only one where manual order exists.
func TestBoardCardDropReordersManualOrder(t *testing.T) {
	withTempBoardConfig(t)
	_, upper, err := boardAddTask("Upper card", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, lower, err := boardAddTask("Lower card", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	// New tasks go first, so the stored order starts lower, upper.
	a := &app{showBoard: true, board: boardState{loaded: true, file: boardFile{Tasks: []boardTask{lower, upper}}}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Advance(time.Second)
	tt.Frame()
	src, ok := tt.Find("Upper card")
	if !ok {
		t.Fatal("missing source card")
	}
	dst, ok := tt.Find("Lower card")
	if !ok {
		t.Fatal("missing target card")
	}
	tt.Press(src.X+src.W/2, src.Y+src.H/2)
	for k := 1; k <= 8; k++ {
		p := float32(k) / 8
		tt.Move(src.X+src.W/2+(dst.X-src.X)*p, src.Y+src.H/2+(dst.Y+src.H/2-src.Y-src.H/2)*p)
	}
	// Drop on the lower half of the card to place the dragged one after it.
	tt.Release(dst.X+dst.W/2, dst.Y+dst.H*0.8)
	tt.Frame()
	saved, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, task := range saved.Tasks {
		order = append(order, task.ID)
	}
	if want := []string{lower.ID, upper.ID}; !slices.Equal(order, want) {
		t.Fatalf("order after drop = %v, want %v", order, want)
	}
	if saved.Tasks[0].Status != boardTodo || saved.Tasks[1].Status != boardTodo {
		t.Fatalf("reorder changed a status: %+v", saved.Tasks)
	}
}

func TestBoardCardDropIsIgnoredOutsideDefaultSort(t *testing.T) {
	withTempBoardConfig(t)
	_, upper, err := boardAddTask("Sorted upper", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, lower, err := boardAddTask("Sorted lower", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	for mode := range map[boardSortMode]bool{boardSortRecent: true, boardSortPriority: true, boardSortDue: true} {
		if (&app{board: boardState{sortMode: mode}}).boardCardDropAllowed(boardTaskDrag{taskID: upper.ID}, lower) {
			t.Fatalf("sort %q offered a manual drop target that the sort would undo", mode)
		}
	}
	if !(&app{board: boardState{}}).boardCardDropAllowed(boardTaskDrag{taskID: upper.ID}, lower) {
		t.Fatal("the default sort should allow manual reordering")
	}
	// A pinned card and a regular one never trade places: their groups sort apart.
	if (&app{board: boardState{}}).boardCardDropAllowed(boardTaskDrag{taskID: upper.ID, pinned: true}, lower) {
		t.Fatal("drop across pin groups should not be offered")
	}
}

func TestBoardPinnedBranchFollowsRepositoryFilter(t *testing.T) {
	repoA := "/tmp/branch-audit-a"
	repoB := "/tmp/branch-audit-b"
	tabA := newTab(1, repoA)
	tabA.branch, tabA.loaded = "feature-alpha", true
	defer tabA.cancel()
	tabB := newTab(2, repoB)
	tabB.branch, tabB.loaded = "main", true
	defer tabB.cancel()
	task := boardTask{
		ID: "T-1", Title: "Multi repo pinned", Status: boardDoing, Pinned: true,
		Repos: []string{repoA, repoB},
	}
	a := &app{showBoard: true, tabs: []*repoTab{tabA, tabB}, board: boardState{loaded: true, file: boardFile{Tasks: []boardTask{task}}}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()
	expect := repoTitle(repoA) + " · feature-alpha"
	if !tt.HasText(expect) {
		t.Fatalf("pinned card should show its first repository's branch %q: %q", expect, tt.Texts())
	}
	a.board.repoFilter = repoB
	tt.Frame()
	expect = repoTitle(repoB) + " · main"
	if !tt.HasText(expect) {
		t.Fatalf("filtering the second repository should show its branch %q: %q", expect, tt.Texts())
	}
}

func TestBoardCommitChipsWithSameHashInTwoRepos(t *testing.T) {
	hash := strings.Repeat("1", 40)
	task := boardTask{
		ID: "T-1", Title: "Shared commit", Status: boardTodo,
		Commits: []boardCommitLink{
			{Repo: "/tmp/repo-one", Hash: hash, Subject: "original", AddedAt: time.Now().Add(-time.Hour)},
			{Repo: "/tmp/repo-two", Hash: hash, Subject: "clone", AddedAt: time.Now()},
		},
	}
	a := &app{showBoard: true, board: boardState{loaded: true, file: boardFile{Tasks: []boardTask{task}}}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()
	tt.Frame()
	// Both links share a hash, so their chips must still be separate elements.
	count := 0
	for _, elem := range tt.AccessAll() {
		if strings.Contains(elem.Name, "1111111") {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("expected two commit chips for the same hash, found %d: %q", count, tt.Texts())
	}
}

// TestBoardCommitClickOpensTheLinkedRepository checks the click path against
// real repositories: each commit chip opens the repository it names and selects
// the commit there, even when the task links two repositories.
func TestBoardCommitClickOpensTheLinkedRepository(t *testing.T) {
	withTempBoardConfig(t)
	repoA, gitA := initTestRepo(t)
	gitA("commit", "--allow-empty", "-m", "first linked commit")
	hashA := strings.TrimSpace(string(gitA("rev-parse", "HEAD")))
	repoB, gitB := initTestRepo(t)
	gitB("commit", "--allow-empty", "-m", "second linked commit")
	hashB := strings.TrimSpace(string(gitB("rev-parse", "HEAD")))

	bf, task, err := boardAddTask("Jump target", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []boardCommitLink{
		{Repo: repoA, Hash: hashA, Subject: "first linked commit", AddedAt: time.Now().Add(-time.Minute)},
		{Repo: repoB, Hash: hashB, Subject: "second linked commit", AddedAt: time.Now()},
	} {
		if bf, _, err = boardLinkCommit(task.ID, link); err != nil {
			t.Fatal(err)
		}
	}
	updates := make(chan func(), 256)
	a := &app{
		showBoard: true,
		dispatch:  func(fn func()) { updates <- fn },
		board:     boardState{loaded: true, file: bf},
	}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	openChip := func(chip, wantRepo, wantHash string) {
		t.Helper()
		if !a.showBoard {
			a.openBoard()
		}
		for i := 0; i < 3; i++ {
			tt.Frame()
			drainUpdates(updates)
		}
		tt.Advance(time.Second)
		tt.Frame()
		drainUpdates(updates)
		if elem, ok := tt.AccessByRole(ui.RoleButton, chip); !ok || elem.Bounds.W <= 0 {
			t.Fatalf("commit chip %q has no clickable box: %q", chip, tt.Texts())
		} else {
			tt.ClickAt(elem.Bounds.X+elem.Bounds.W/2, elem.Bounds.Y+elem.Bounds.H/2)
		}
		for i := 0; i < 100; i++ {
			tt.Frame()
			drainUpdates(updates)
			tab := a.activeTab()
			if tab == nil || normalizeBoardRepo(tab.path) != normalizeBoardRepo(wantRepo) ||
				!tab.loaded || tab.loading || len(tab.commits) == 0 {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if tab.selected < 0 || tab.rowData(tab.selected) == nil {
				t.Fatalf("no commit selected after jumping to %s: selected=%d", wantRepo, tab.selected)
			}
			if got := hashHex(tab.rowData(tab.selected).hash); got != wantHash {
				t.Fatalf("selected commit %s, want %s", got, wantHash)
			}
			return
		}
		t.Fatalf("clicking %q did not open %s, tabs=%d texts=%q", chip, wantRepo, len(a.tabs), tt.Texts())
	}
	openChip(hashB[:7]+" second linked commit", repoB, hashB)
	openChip(hashA[:7]+" first linked commit", repoA, hashA)
}

func TestBoardQuickCaptureKeepsTheDraftWhenTheWriteFails(t *testing.T) {
	withTempBoardConfig(t)
	path, err := boardConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	// A directory where the file goes makes every write fail.
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	a := &app{showBoard: true, board: boardState{loaded: true}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	a.board.quickTitle = "Cannot be saved"
	a.submitQuickBoardTask()
	tt.Frame()
	if a.board.quickTitle != "Cannot be saved" {
		t.Fatalf("failed create lost the draft: %q", a.board.quickTitle)
	}
	if a.board.quickSaving {
		t.Fatal("a failed create should not stay busy")
	}
}

func TestBoardWallStatusFilterSwitchKeepsBalancedColumns(t *testing.T) {
	tasks := []boardTask{
		{ID: "T-1", Title: "Review task alpha", Status: boardReview},
		{ID: "T-2", Title: "Review task beta", Status: boardReview},
		{ID: "T-3", Title: "Doing task gamma", Status: boardDoing},
		{ID: "T-4", Title: "Todo task delta", Status: boardTodo},
		{ID: "T-5", Title: "Todo task epsilon", Status: boardTodo},
		{ID: "T-6", Title: "Todo task zeta", Status: boardTodo},
	}
	a := &app{showBoard: true, board: boardState{loaded: true, file: boardFile{Tasks: tasks}}}
	tt := ui.NewTester(a.view, 1400, 820)
	defer retireTester(tt)
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	for _, tab := range []string{"待审阅", "进行中", "待办", "全部未完成"} {
		if err := tt.Click(tab); err != nil {
			t.Fatalf("click %q failed: %v", tab, err)
		}
		for i := 0; i < 3; i++ {
			tt.Frame()
		}
	}
	for _, task := range tasks {
		if _, ok := a.board.wallHeights[task.ID]; !ok {
			t.Fatalf("cached height for %s was evicted across filter switch", task.ID)
		}
	}
	gotLens := make([]int, len(a.board.wall.columns))
	for i, col := range a.board.wall.columns {
		gotLens[i] = len(col)
	}
	wantLens := []int{2, 2, 1, 1}
	if !slices.Equal(gotLens, wantLens) {
		t.Fatalf("columns after switching back = %v, want %v", gotLens, wantLens)
	}
}

func TestBoardWallRebalancesColumnsAfterMeasuringNewCards(t *testing.T) {
	tasks := []boardTask{
		{ID: "T-1", Title: "Review task alpha", Status: boardReview},
		{ID: "T-2", Title: "Review task beta", Status: boardReview},
		{ID: "T-3", Title: "Doing task gamma", Status: boardDoing},
		{ID: "T-4", Title: "Todo task delta", Status: boardTodo},
		{ID: "T-5", Title: "Todo task epsilon", Status: boardTodo},
		{ID: "T-6", Title: "Todo task zeta", Status: boardTodo},
	}
	a := &app{showBoard: true, board: boardState{loaded: true, statusFilter: boardTodo, file: boardFile{Tasks: tasks}}}
	tt := ui.NewTester(a.view, 1400, 820)
	defer retireTester(tt)
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if err := tt.Click("全部未完成"); err != nil {
		t.Fatalf("click all failed: %v", err)
	}
	for i := 0; i < 4; i++ {
		tt.Frame()
	}
	gotLens := make([]int, len(a.board.wall.columns))
	for i, col := range a.board.wall.columns {
		gotLens[i] = len(col)
	}
	wantLens := []int{2, 2, 1, 1}
	if !slices.Equal(gotLens, wantLens) {
		t.Fatalf("columns after measuring new cards = %v, want %v", gotLens, wantLens)
	}
}

func TestBoardCardSignatureTracksPriorityAndDescription(t *testing.T) {
	base := boardTask{ID: "T-1", Title: "Service cleanup", Status: boardTodo}
	baseSig := boardCardSignature(base, 0)

	withPrio := base
	withPrio.Priority = boardPriorityUrgent
	if boardCardSignature(withPrio, 0) == baseSig {
		t.Fatal("boardCardSignature did not change when priority changed")
	}

	withDesc := base
	withDesc.Description = "Detail line for the card"
	if boardCardSignature(withDesc, 0) == baseSig {
		t.Fatal("boardCardSignature did not change when description changed")
	}
}

func TestBoardWallRestoringSelectedDoneTaskKeepsBalancedColumns(t *testing.T) {
	tasks := []boardTask{
		{ID: "T-1", Title: "Review task alpha", Status: boardReview},
		{ID: "T-2", Title: "Review task beta", Status: boardReview},
		{ID: "T-3", Title: "Doing task gamma", Status: boardDoing},
		{ID: "T-4", Title: "Todo task delta", Status: boardTodo},
		{ID: "T-5", Title: "Todo task epsilon", Status: boardTodo},
		{ID: "T-6", Title: "Todo task zeta", Status: boardTodo},
		{ID: "T-7", Title: "Done task eta", Status: boardDone},
	}
	a := &app{showBoard: true, board: boardState{loaded: true, doneExpanded: true, file: boardFile{Tasks: tasks}}}
	tt := ui.NewTester(a.view, 1400, 820)
	defer retireTester(tt)
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	a.selectBoardTask("T-7")
	tt.Frame()
	tt.Frame()

	restored := []boardTask{
		{ID: "T-1", Title: "Review task alpha", Status: boardReview},
		{ID: "T-7", Title: "Done task eta", Status: boardReview},
		{ID: "T-2", Title: "Review task beta", Status: boardReview},
		{ID: "T-3", Title: "Doing task gamma", Status: boardDoing},
		{ID: "T-4", Title: "Todo task delta", Status: boardTodo},
		{ID: "T-5", Title: "Todo task epsilon", Status: boardTodo},
		{ID: "T-6", Title: "Todo task zeta", Status: boardTodo},
	}
	a.applyBoardFile(boardFile{Tasks: restored}, nil)
	for i := 0; i < 4; i++ {
		tt.Frame()
	}

	a.closeBoardDrawer()
	for i := 0; i < 3; i++ {
		tt.Frame()
	}

	h1, ok1 := a.board.wallHeights["T-1"]
	h7, ok7 := a.board.wallHeights["T-7"]
	if !ok1 || !ok7 {
		t.Fatalf("expected measured heights for T-1 and T-7, got ok1=%v ok7=%v", ok1, ok7)
	}
	if max(h1.height-h7.height, h7.height-h1.height) > 0.5 {
		t.Fatalf("selected border changed card height: T-1=%.2f T-7=%.2f", h1.height, h7.height)
	}
	gotLens := make([]int, len(a.board.wall.columns))
	for i, col := range a.board.wall.columns {
		gotLens[i] = len(col)
	}
	wantLens := []int{2, 2, 2, 1}
	if !slices.Equal(gotLens, wantLens) {
		t.Fatalf("columns after restoring selected done task = %v, want %v", gotLens, wantLens)
	}
}
