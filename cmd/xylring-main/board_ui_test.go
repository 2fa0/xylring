package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

func TestBoardUIWorkflowAndDrawer(t *testing.T) {
	withTempBoardConfig(t)
	updates := make(chan func(), 64)
	tab := newTab(1, "/tmp/demo-repo")
	tab.loaded, tab.branch = true, "main"
	tab.commits = []commit{{hash: hashN(1), author: "Ada", subject: "Initial commit", when: 1700000000}}
	tab.graph.add(&tab.commits[0])

	a := &app{
		tabs:            []*repoTab{tab},
		bottomFullWidth: true,
		dispatch:        func(fn func()) { updates <- fn },
	}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	defer tab.cancel()

	tt.Frame()
	drainUpdates(updates)

	if err := tt.Click("看板"); err != nil {
		t.Fatalf("click board nav: %v", err)
	}
	tt.Frame()
	if !a.showBoard || !tt.HasText("待办") || !tt.HasText("进行中") || !tt.HasText("待审阅") || !tt.HasText("已完成") {
		t.Fatalf("board columns missing, showBoard=%v texts=%q", a.showBoard, tt.Texts())
	}

	// Create task via modal
	if err := tt.Click("新建任务"); err != nil {
		t.Fatalf("click new task: %v", err)
	}
	tt.Frame()
	if !a.board.showNewModal || !tt.HasText("新建看板任务") {
		t.Fatalf("new task modal did not open: %q", tt.Texts())
	}
	if len(a.board.newRepos) != 0 {
		t.Fatalf("new task modal should not preselect repos by default, got %v", a.board.newRepos)
	}
	a.board.newTitle = "完善看板交互"
	a.board.newDesc = "支持备注与多提交绑定"
	if err := tt.Click("创建"); err != nil {
		t.Fatalf("click create: %v", err)
	}
	tt.Frame()
	if a.board.showNewModal || a.board.selectedID != "" {
		t.Fatalf("task creation should close modal without opening drawer: modal=%v sel=%q", a.board.showNewModal, a.board.selectedID)
	}
	if !tt.HasText("完善看板交互") {
		t.Fatalf("created task card missing: %q", tt.Texts())
	}
	if tt.HasText("任务描述") {
		t.Fatalf("drawer should not open by default after creating task: %q", tt.Texts())
	}

	// Open drawer by clicking the created task card
	if err := tt.Click("完善看板交互"); err != nil {
		t.Fatalf("click created task: %v", err)
	}
	tt.Frame()
	if a.board.selectedID != "T-1" {
		t.Fatalf("expected task selected after click, got sel=%q", a.board.selectedID)
	}
	if !tt.HasText("任务描述") || !tt.HasText("备注") {
		t.Fatalf("drawer should be open: %q", tt.Texts())
	}

	// Add note in drawer
	a.board.noteDraft = "开始验证抽屉备注"
	tt.Frame()
	if err := tt.Click("添加备注"); err != nil {
		t.Fatalf("click add note: %v", err)
	}
	tt.Frame()
	if !tt.HasText("开始验证抽屉备注") {
		t.Fatalf("note should appear in drawer: %q", tt.Texts())
	}

	// Quick toggle done from card check button
	if err := tt.Click("标记为已完成"); err != nil {
		t.Fatalf("click check done: %v", err)
	}
	tt.Frame()
	bf, err := loadBoardFile()
	if err != nil || len(bf.Tasks) != 1 || bf.Tasks[0].Status != boardDone {
		t.Fatalf("expected task done, got %+v err=%v", bf.Tasks, err)
	}
}

func TestCommitRowMenuLinksCommitToBoardTask(t *testing.T) {
	withTempBoardConfig(t)
	tab := graphTabForWrite()
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	defer retireTester(tt)
	defer tab.cancel()

	tt.Frame()
	if err := tt.RightClick("画出提交图谱"); err != nil {
		t.Fatalf("right click commit: %v", err)
	}
	if err := tt.ChooseMenuItem("关联到看板任务", "新建任务并关联…"); err != nil {
		t.Fatalf("choose create and link: %v", err)
	}
	tt.Frame()

	bf, err := loadBoardFile()
	if err != nil || len(bf.Tasks) != 1 {
		t.Fatalf("expected 1 board task, got %+v err=%v", bf.Tasks, err)
	}
	if bf.Tasks[0].Title != "画出提交图谱" || len(bf.Tasks[0].Commits) != 1 {
		t.Fatalf("unexpected linked task: %+v", bf.Tasks[0])
	}
}

func TestEnglishBoardUIHasNoChineseApplicationText(t *testing.T) {
	withTempBoardConfig(t)
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })

	_, created, err := boardAddTaskWithOptions(boardAddTaskOptions{
		Title:       "English Task",
		Description: "English description",
		Status:      boardDoing,
		Priority:    boardPriorityUrgentImportant,
		DueDate:     time.Now().Format("2006-01-02"),
		Repos:       []string{"/tmp/example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = boardLinkCommit(created.ID, boardCommitLink{
		Repo:    "/tmp/example",
		Hash:    "1111111111111111111111111111111111111111",
		Subject: "Commit subject",
		Agent:   "Codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddNote(created.ID, "Progress note", "human", ""); err != nil {
		t.Fatal(err)
	}

	updates := make(chan func(), 64)
	a := &app{
		showBoard: true,
		dispatch:  func(fn func()) { updates <- fn },
	}
	a.ensureBoardLoaded()
	a.selectBoardTask(created.ID)

	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()
	drainUpdates(updates)
	assertEnglishUITexts(t, tt)
	if !tt.HasText("In Progress") || !tt.HasText("Notes") {
		t.Fatalf("expected English board UI labels: %q", tt.Texts())
	}
}

func TestBoardUnifiedChromeDrawerAndPreviews(t *testing.T) {
	withTempBoardConfig(t)
	updates := make(chan func(), 64)
	tab := newTab(1, "/tmp/xylring")
	tab.loaded, tab.branch = true, "main"
	tab.commits = []commit{{hash: hashN(1), author: "Ada", subject: "Initial commit", when: 1700000000}}
	tab.graph.add(&tab.commits[0])

	_, t1, err := boardAddTask("重构看板顶部导航与卡片层级", "合并顶栏并减少胶囊边框", boardDoing, []string{"/tmp/xylring"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = boardLinkCommit(t1.ID, boardCommitLink{
		Repo:    "/tmp/xylring",
		Hash:    "1111111111111111111111111111111111111111",
		Subject: "feat(board): unify top chrome toolbar",
		Agent:   "Antigravity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddNote(t1.ID, "已完成初版骨架，待检查暗色模式对比度。", "Antigravity", "Gemini 4"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("补充空搜索筛选提示", "", boardTodo, []string{"/tmp/other-repo"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("合并提交跳转定位", "", boardReview, []string{"/tmp/xylring"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("清理旧的文案翻译键", "", boardDone, []string{"/tmp/xylring"}); err != nil {
		t.Fatal(err)
	}

	for _, dark := range []bool{false, true} {
		a := &app{
			tabs:            []*repoTab{tab},
			showBoard:       true,
			bottomFullWidth: true,
			dispatch:        func(fn func()) { updates <- fn },
			board: boardState{
				commitValid: map[string]bool{
					boardCommitCacheKey("/tmp/xylring", "1111111111111111111111111111111111111111"): true,
				},
			},
		}
		a.ensureBoardLoaded()
		a.selectBoardTask(t1.ID)

		tt := ui.NewTester(a.view, 1280, 820)
		tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 72, Right: 10})
		tt.SetDark(dark)
		tt.Frame()
		drainUpdates(updates)
		tt.Frame()

		// Verify top chrome toolbar controls do not overlap the sidebar
		newBtn, ok := tt.AccessByRole(ui.RoleButton, "新建任务")
		if !ok {
			t.Fatalf("missing primary button in chrome: %q", tt.Texts())
		}
		if newBtn.Bounds.Y+newBtn.Bounds.H > 46 {
			t.Fatalf("new task button should be inside top chrome bar, bounds=%+v", newBtn.Bounds)
		}
		filterBtn, ok := tt.Find("全部项目")
		if !ok || filterBtn.X < defaultSidebarWidth {
			t.Fatalf("repo filter should clear sidebar: ok=%v bounds=%+v", ok, filterBtn)
		}

		mode := "light"
		if dark {
			mode = "dark"
		}
		saveLobePreview(t, tt, "board-"+mode)

		if !dark {
			// Test segmented status control in drawer updates task status
			segItem, ok := tt.AccessByRole(ui.RoleRadio, "待审阅")
			if !ok {
				t.Fatalf("missing status radio segment: %q", tt.Texts())
			}
			tt.ClickAt(segItem.Bounds.X+segItem.Bounds.W/2, segItem.Bounds.Y+segItem.Bounds.H/2)
			tt.Frame()
			bf, err := loadBoardFile()
			if err != nil {
				t.Fatal(err)
			}
			if idx := findBoardTaskIndex(bf.Tasks, t1.ID); idx < 0 || bf.Tasks[idx].Status != boardReview {
				t.Fatalf("expected %s status to be review, got %+v", t1.ID, bf.Tasks)
			}

			// Test Escape closes drawer
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			if a.board.selectedID != "" {
				t.Fatalf("expected Escape to close drawer, selectedID=%q", a.board.selectedID)
			}
			saveLobePreview(t, tt, "board-overview-light")

			// Verify all 4 columns are visible side-by-side at 1280px when drawer is closed
			doneHeader, ok := tt.Find("已完成")
			if !ok || doneHeader.X+doneHeader.W > 1280 {
				t.Fatalf("expected done column fully inside 1280px window, ok=%v bounds=%+v", ok, doneHeader)
			}

			// Test filter empty banner + clear filter with drawer closed
			a.board.search = "nonexistent-query-xyz"
			tt.Frame()
			if !tt.HasText("没有匹配的任务") {
				t.Fatalf("expected filter empty banner: %q", tt.Texts())
			}
			if err := tt.Click("清除筛选"); err != nil {
				t.Fatalf("click clear filter: %v", err)
			}
			tt.Frame()
			if a.board.search != "" {
				t.Fatalf("expected search cleared, got %q", a.board.search)
			}
		}
		retireTester(tt)
	}
	tab.cancel()
}

func TestBoardCardDragDropBetweenColumns(t *testing.T) {
	withTempBoardConfig(t)
	_, created, err := boardAddTask("可拖拽任务卡片", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{
		showBoard: true,
		dispatch:  func(fn func()) { fn() },
	}
	a.ensureBoardLoaded()

	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	cardBounds, ok := tt.Find("可拖拽任务卡片")
	if !ok {
		t.Fatalf("missing task card: %q", tt.Texts())
	}
	doingCol, ok := tt.Find("进行中")
	if !ok {
		t.Fatalf("missing doing column: %q", tt.Texts())
	}

	fromX := cardBounds.X + cardBounds.W/2
	fromY := cardBounds.Y + cardBounds.H/2
	toX := doingCol.X + doingCol.W/2
	toY := doingCol.Y + 140

	tt.Press(fromX, fromY)
	for k := 1; k <= 4; k++ {
		f := float32(k) / 4
		tt.Move(fromX+(toX-fromX)*f, fromY+(toY-fromY)*f)
	}
	tt.Release(toX, toY)
	tt.Frame()

	bf, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	idx := findBoardTaskIndex(bf.Tasks, created.ID)
	if idx < 0 || bf.Tasks[idx].Status != boardDoing {
		t.Fatalf("expected %s to move to doing after drag-drop, got %+v", created.ID, bf.Tasks)
	}
	if a.board.selectedID != "" {
		t.Fatalf("dragging card should not open drawer, selectedID=%q", a.board.selectedID)
	}
}

func TestBoardCardDragReorderWithinAndAcrossColumns(t *testing.T) {
	withTempBoardConfig(t)
	if _, _, err := boardAddTask("待办任务三", "", boardTodo, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("待办任务二", "", boardTodo, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("待办任务一", "", boardTodo, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("进行中任务二", "", boardDoing, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("进行中任务一", "", boardDoing, nil); err != nil {
		t.Fatal(err)
	}

	a := &app{
		showBoard: true,
		dispatch:  func(fn func()) { fn() },
	}
	a.ensureBoardLoaded()

	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	dragBetween := func(fromX, fromY, toX, toY float32) {
		tt.Press(fromX, fromY)
		for k := 1; k <= 4; k++ {
			f := float32(k) / 4
			tt.Move(fromX+(toX-fromX)*f, fromY+(toY-fromY)*f)
		}
		tt.Release(toX, toY)
		tt.Frame()
	}

	titlesInCol := func(st boardStatus) []string {
		bf, err := loadBoardFile()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, task := range bf.Tasks {
			if task.Status == st {
				out = append(out, task.Title)
			}
		}
		return out
	}

	// 1. Drag adjacent first card ("待办任务一") onto second card ("待办任务二") -> swaps them.
	c1, ok1 := tt.Find("待办任务一")
	c2, ok2 := tt.Find("待办任务二")
	if !ok1 || !ok2 {
		t.Fatalf("missing todo cards: %q", tt.Texts())
	}
	dragBetween(c1.X+c1.W/2, c1.Y+c1.H/2, c2.X+c2.W/2, c2.Y+c2.H/2)
	if got := titlesInCol(boardTodo); !slices.Equal(got, []string{"待办任务二", "待办任务一", "待办任务三"}) {
		t.Fatalf("after swapping first two cards, got %v", got)
	}

	// 2. Drag last card ("待办任务三") up onto top edge of first card ("待办任务二") -> moves to top.
	c3, ok3 := tt.Find("待办任务三")
	c2, ok2 = tt.Find("待办任务二")
	if !ok2 || !ok3 {
		t.Fatalf("missing todo cards before upward drag: %q", tt.Texts())
	}
	dragBetween(c3.X+c3.W/2, c3.Y+c3.H/2, c2.X+c2.W/2, c2.Y-4)
	if got := titlesInCol(boardTodo); !slices.Equal(got, []string{"待办任务三", "待办任务二", "待办任务一"}) {
		t.Fatalf("after dragging card up to top, got %v", got)
	}

	// 3. Drag first card ("待办任务三") down into empty scroll area below all cards in the same column -> moves to end.
	c3, ok3 = tt.Find("待办任务三")
	c1, ok1 = tt.Find("待办任务一")
	if !ok3 || !ok1 {
		t.Fatalf("missing todo cards before empty-space drag: %q", tt.Texts())
	}
	dragBetween(c3.X+c3.W/2, c3.Y+c3.H/2, c1.X+c1.W/2, c1.Y+c1.H+100)
	if got := titlesInCol(boardTodo); !slices.Equal(got, []string{"待办任务二", "待办任务一", "待办任务三"}) {
		t.Fatalf("after dragging card to end of column, got %v", got)
	}

	// 4. Drag "待办任务一" across columns onto top edge of "进行中任务二" -> lands between 进行中任务一 and 进行中任务二.
	c1, ok1 = tt.Find("待办任务一")
	d2, okD2 := tt.Find("进行中任务二")
	if !ok1 || !okD2 {
		t.Fatalf("missing cards for cross-column positioned drop: %q", tt.Texts())
	}
	dragBetween(c1.X+c1.W/2, c1.Y+c1.H/2, d2.X+d2.W/2, d2.Y-4)
	if got := titlesInCol(boardTodo); !slices.Equal(got, []string{"待办任务二", "待办任务三"}) {
		t.Fatalf("expected 待办任务一 removed from todo, got %v", got)
	}
	if got := titlesInCol(boardDoing); !slices.Equal(got, []string{"进行中任务一", "待办任务一", "进行中任务二"}) {
		t.Fatalf("expected 待办任务一 inserted before 进行中任务二, got %v", got)
	}
	if a.board.selectedID != "" {
		t.Fatalf("dragging card should not open drawer, selectedID=%q", a.board.selectedID)
	}
}

func TestBoardBackgroundDraftWritesKeepSubmissionOrder(t *testing.T) {
	withTempBoardConfig(t)
	bf, task, err := boardAddTask("original", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan func(), 4)
	a := &app{win: &mygo.Window{}, dispatch: func(fn func()) { updates <- fn }}
	a.applyBoardFile(bf, nil)
	a.selectBoardTask(task.ID)

	a.board.titleDraft = "first draft"
	a.commitBoardDrafts()
	a.board.titleDraft = "original"
	a.board.descDraft = "latest description"
	a.commitBoardDrafts()
	if a.board.jobs != 2 {
		t.Fatal("both edits should be queued without waiting for disk")
	}
	waitUpdates(t, updates, func() bool { return a.board.jobs == 0 })
	stored, err := loadBoardFile()
	if err != nil || len(stored.Tasks) != 1 || stored.Tasks[0].Title != "original" || stored.Tasks[0].Description != "latest description" {
		t.Fatalf("stored task after queued edits: %+v, %v", stored.Tasks, err)
	}
}

func TestBoardExternalSyncKeepsUnsavedDrafts(t *testing.T) {
	withTempBoardConfig(t)
	a := &app{showBoard: true, dispatch: func(func()) {}}
	a.ensureBoardLoaded()
	bf, created, err := boardAddTask("外部同步测试", "原始描述", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.applyBoardFile(bf, nil)
	a.selectBoardTask(created.ID)

	a.board.titleDraft = "正在输入的标题"
	a.board.descDraft = "正在输入的描述"

	// Another process (CLI or a second window) rewrites the file underneath us.
	external, _, err := boardAddNote(created.ID, "另一个进程的备注", "Antigravity", "Gemini")
	if err != nil {
		t.Fatal(err)
	}
	a.applyBoardFile(external, nil)

	if a.board.titleDraft != "正在输入的标题" || a.board.descDraft != "正在输入的描述" {
		t.Fatalf("external sync dropped unsaved drafts: title=%q desc=%q", a.board.titleDraft, a.board.descDraft)
	}

	a.commitBoardDrafts()
	stored, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	idx := findBoardTaskIndex(stored.Tasks, created.ID)
	if idx < 0 || stored.Tasks[idx].Title != "正在输入的标题" || stored.Tasks[idx].Description != "正在输入的描述" {
		t.Fatalf("typed drafts were not saved: %+v", stored.Tasks)
	}
}

func TestBoardNoteDraftIsKeptPerTask(t *testing.T) {
	withTempBoardConfig(t)
	a := &app{showBoard: true, dispatch: func(func()) {}}
	a.ensureBoardLoaded()
	bf, first, err := boardAddTask("第一个任务", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	bf, second, err := boardAddTask("第二个任务", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.applyBoardFile(bf, nil)

	a.selectBoardTask(first.ID)
	a.board.noteDraft = "第一个任务的备注草稿"
	a.closeBoardDrawer()
	a.selectBoardTask(first.ID)
	if a.board.noteDraft != "第一个任务的备注草稿" {
		t.Fatalf("closing the drawer dropped the note draft: %q", a.board.noteDraft)
	}

	a.selectBoardTask(second.ID)
	if a.board.noteDraft != "" {
		t.Fatalf("another task should start with an empty note, got %q", a.board.noteDraft)
	}
	a.board.noteDraft = "第二个任务的备注草稿"
	a.selectBoardTask(first.ID)
	if a.board.noteDraft != "第一个任务的备注草稿" {
		t.Fatalf("note draft not restored per task: %q", a.board.noteDraft)
	}
	a.selectBoardTask(second.ID)
	if a.board.noteDraft != "第二个任务的备注草稿" {
		t.Fatalf("note draft not restored per task: %q", a.board.noteDraft)
	}
}

func TestBoardEmptyTitleDraftReverts(t *testing.T) {
	withTempBoardConfig(t)
	a := &app{showBoard: true, dispatch: func(func()) {}}
	a.ensureBoardLoaded()
	bf, created, err := boardAddTask("标题不能清空", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.applyBoardFile(bf, nil)
	a.selectBoardTask(created.ID)

	a.board.titleDraft = "   "
	a.commitBoardDrafts()
	if a.board.titleDraft != "标题不能清空" {
		t.Fatalf("cleared title should fall back to the stored one, got %q", a.board.titleDraft)
	}
	if a.banner != tr("任务标题不能为空") {
		t.Fatalf("expected a banner about the empty title, got %q", a.banner)
	}
}

func TestBoardDeleteAsksBeforeRemoving(t *testing.T) {
	withTempBoardConfig(t)
	a := &app{showBoard: true, dispatch: func(func()) {}}
	a.ensureBoardLoaded()
	_, created, err := boardAddTask("等待确认删除", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.reloadBoardSync()
	a.selectBoardTask(created.ID)

	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	if err := tt.Click("任务操作"); err != nil {
		t.Fatalf("click drawer actions: %v", err)
	}
	if err := tt.ChooseMenuItem("删除任务"); err != nil {
		t.Fatalf("click delete: %v", err)
	}
	tt.Frame()
	if a.board.deleteTaskID != created.ID {
		t.Fatalf("expected a confirmation step, deleteTaskID=%q", a.board.deleteTaskID)
	}
	if bf, err := loadBoardFile(); err != nil || findBoardTaskIndex(bf.Tasks, created.ID) < 0 {
		t.Fatalf("task must survive until confirmed: %+v err=%v", bf.Tasks, err)
	}
	cancelBtn, ok1 := tt.AccessByRole(ui.RoleButton, "取消")
	confirmBtn, ok2 := tt.AccessByRole(ui.RoleButton, "确认删除")
	if !ok1 || !ok2 {
		t.Fatalf("missing delete modal buttons: ok1=%v ok2=%v", ok1, ok2)
	}
	if abs := func(v float32) float32 {
		if v < 0 {
			return -v
		}
		return v
	}; abs(cancelBtn.Bounds.Y-confirmBtn.Bounds.Y) > 0.5 || abs(cancelBtn.Bounds.H-confirmBtn.Bounds.H) > 0.5 {
		t.Fatalf("delete modal buttons misaligned: cancel=%+v confirm=%+v", cancelBtn.Bounds, confirmBtn.Bounds)
	}
	saveLobePreview(t, tt, "board-delete-confirm")

	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click cancel: %v", err)
	}
	tt.Frame()
	if a.board.deleteTaskID != "" {
		t.Fatalf("cancel should close the confirmation, deleteTaskID=%q", a.board.deleteTaskID)
	}
	if bf, err := loadBoardFile(); err != nil || findBoardTaskIndex(bf.Tasks, created.ID) < 0 {
		t.Fatalf("cancel must keep the task: %+v err=%v", bf.Tasks, err)
	}

	if err := tt.Click("任务操作"); err != nil {
		t.Fatalf("click drawer actions again: %v", err)
	}
	if err := tt.ChooseMenuItem("删除任务"); err != nil {
		t.Fatalf("click delete again: %v", err)
	}
	tt.Frame()
	if err := tt.Click("确认删除"); err != nil {
		t.Fatalf("click confirm: %v", err)
	}
	tt.Frame()
	bf, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if findBoardTaskIndex(bf.Tasks, created.ID) >= 0 {
		t.Fatalf("task should be deleted after confirming: %+v", bf.Tasks)
	}
	if a.board.selectedID != "" || a.board.deleteTaskID != "" {
		t.Fatalf("delete should close drawer and dialog: sel=%q del=%q", a.board.selectedID, a.board.deleteTaskID)
	}
}

func TestLinkBoardCommitModal(t *testing.T) {
	withTempBoardConfig(t)
	updates := make(chan func(), 64)
	repo1 := "/Users/example/Documents/company/SRC代码/zasrc-bapi-front"
	repo2 := "/Users/example/Documents/company/xylring"
	tab1 := newTab(1, repo1)
	tab1.loaded, tab1.branch = true, "master"
	tab1.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "feat: [TASK-FE-14169] (release) 增加版本更新提示与静态资源缓存策略", when: 1700005000},
		{hash: hashN(2), author: "Ada", subject: "feat: [TASK-FE-13784] (sla) 默认修复期限按工作日计算", when: 1700004000},
		{hash: hashN(3), author: "Bob", subject: "chore: [TASK-FE-13784] (repo) 忽略 .ai-tracks 目录", when: 1700003000},
		{hash: hashN(4), author: "Bob", subject: "fix: [TASK-FE-13784] (detail) 避免动作面板请求竞态覆盖", when: 1700002000},
		{hash: hashN(5), author: "Ada", subject: "feat: [TASK-FE-13784] (detail) 统一详情视角并支持只读视角", when: 1700001000},
		{hash: hashN(6), author: "CI", subject: "Merge branch 'ZA-PR-1021130_13784' into 'master' from ship", when: 1700000000},
	}
	tab2 := newTab(2, repo2)
	tab2.loaded, tab2.branch = true, "main"
	tab2.commits = []commit{
		{hash: hashN(7), author: "Ada", subject: "fix(board): polish link commit modal layout", when: 1700006000},
	}
	defer tab1.cancel()
	defer tab2.cancel()

	_, created, err := boardAddTask("优化关联提交对话框", "", boardDoing, []string{repo1, repo2})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = boardLinkCommit(created.ID, boardCommitLink{
		Repo:    normalizeBoardRepo(repo1),
		Hash:    hashHex(hashN(3)),
		Subject: tab1.commits[2].subject,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, dark := range []bool{false, true} {
		a := &app{
			tabs:            []*repoTab{tab1, tab2},
			showBoard:       true,
			bottomFullWidth: true,
			dispatch:        func(fn func()) { updates <- fn },
		}
		a.ensureBoardLoaded()
		a.selectBoardTask(created.ID)
		a.openLinkCommitModal()

		tt := ui.NewTester(a.view, 900, 720)
		tt.SetDark(dark)
		tt.Frame()
		drainUpdates(updates)
		tt.Frame()

		dialog, ok := tt.AccessByRole(ui.RoleDialog, "关联提交到任务")
		if !ok {
			retireTester(tt)
			t.Fatalf("missing link commit dialog: %q", tt.Texts())
		}
		if dialog.Bounds.X < 0 || dialog.Bounds.Y < 0 || dialog.Bounds.X+dialog.Bounds.W > 900 || dialog.Bounds.Y+dialog.Bounds.H > 720 {
			retireTester(tt)
			t.Fatalf("dialog clipped: %+v", dialog.Bounds)
		}

		// Pick a recent commit and verify selection + clear + submit button enablement
		pickLabel := hashHex(hashN(1))[:7] + " " + tab1.commits[0].subject
		if err := tt.Click(pickLabel); err != nil {
			retireTester(tt)
			t.Fatalf("click recent commit: %v", err)
		}
		tt.Frame()
		if a.board.linkRef != hashHex(hashN(1)) {
			retireTester(tt)
			t.Fatalf("expected linkRef=%s, got %q", hashHex(hashN(1)), a.board.linkRef)
		}
		submitBtn, ok := tt.AccessByRole(ui.RoleButton, "关联")
		if !ok || submitBtn.Disabled {
			retireTester(tt)
			t.Fatalf("expected submit button enabled after picking commit: ok=%v disabled=%v", ok, submitBtn.Disabled)
		}

		mode := "light"
		if dark {
			mode = "dark"
		}
		saveLobePreview(t, tt, "board-link-commit-"+mode)

		if !dark {
			if err := tt.Click("清空"); err != nil {
				retireTester(tt)
				t.Fatalf("click clear ref: %v", err)
			}
			tt.Frame()
			if a.board.linkRef != "" {
				retireTester(tt)
				t.Fatalf("expected linkRef cleared, got %q", a.board.linkRef)
			}
		}
		retireTester(tt)
	}
}

func TestBoardNoteEditDeleteAndReorder(t *testing.T) {
	withTempBoardConfig(t)
	_, created, err := boardAddTask("备注交互测试", "", boardDoing, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddNote(created.ID, "第一条备注", "human", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddNote(created.ID, "第二条备注", "human", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddNote(created.ID, "第三条备注", "human", ""); err != nil {
		t.Fatal(err)
	}
	oldCreatedAt := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := mutateBoardFile(func(f *boardFile) error {
		for j := range f.Tasks[0].Notes {
			f.Tasks[0].Notes[j].CreatedAt = oldCreatedAt
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	oldTimeLabel := oldCreatedAt.Local().Format("01-02 15:04")

	a := &app{
		showBoard:       true,
		bottomFullWidth: true,
		dispatch:        func(fn func()) { fn() },
	}
	a.ensureBoardLoaded()
	a.selectBoardTask(created.ID)

	tt := ui.NewTester(a.view, 1280, 900)
	defer retireTester(tt)
	tt.Frame()

	// Drag first note down over the third note (near bottom edge) to reorder
	firstNote, ok1 := tt.Find("第一条备注")
	thirdNote, ok3 := tt.Find("第三条备注")
	if !ok1 || !ok3 {
		t.Fatalf("missing notes: %q", tt.Texts())
	}
	fromX, fromY := firstNote.X+firstNote.W/2, firstNote.Y+firstNote.H/2
	toX, toY := thirdNote.X+thirdNote.W/2, thirdNote.Y+thirdNote.H+2
	tt.Press(fromX, fromY)
	for k := 1; k <= 4; k++ {
		f := float32(k) / 4
		tt.Move(fromX+(toX-fromX)*f, fromY+(toY-fromY)*f)
	}
	saveLobePreview(t, tt, "board-notes-dragging")
	tt.Release(toX, toY)
	tt.Frame()

	bf, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(bf.Tasks[0].Notes) != 3 ||
		bf.Tasks[0].Notes[0].Text != "第二条备注" ||
		bf.Tasks[0].Notes[1].Text != "第三条备注" ||
		bf.Tasks[0].Notes[2].Text != "第一条备注" {
		t.Fatalf("expected notes reordered downward after drag, got %+v", bf.Tasks[0].Notes)
	}

	// Drag last note ("第一条备注") back up onto the first note ("第二条备注")
	firstNote, _ = tt.Find("第一条备注")
	topNote, _ := tt.Find("第二条备注")
	fromX, fromY = firstNote.X+firstNote.W/2, firstNote.Y+firstNote.H/2
	toX, toY = topNote.X+topNote.W/2, topNote.Y-2
	tt.Press(fromX, fromY)
	for k := 1; k <= 4; k++ {
		f := float32(k) / 4
		tt.Move(fromX+(toX-fromX)*f, fromY+(toY-fromY)*f)
	}
	tt.Release(toX, toY)
	tt.Frame()

	bf, err = loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if bf.Tasks[0].Notes[0].Text != "第一条备注" || bf.Tasks[0].Notes[1].Text != "第二条备注" {
		t.Fatalf("expected notes reordered upward after drag, got %+v", bf.Tasks[0].Notes)
	}

	// Edit the first note ("第一条备注")
	if err := tt.Click("编辑备注"); err != nil {
		t.Fatalf("click edit note: %v", err)
	}
	tt.Frame()
	if a.board.editingNoteID == "" {
		t.Fatalf("expected editingNoteID to be set")
	}
	saveLobePreview(t, tt, "board-notes-edit")

	a.board.editingNoteText = "第一条备注（已编辑）"
	tt.Frame()
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("click save note: %v", err)
	}
	tt.Frame()
	if a.board.editingNoteID != "" {
		t.Fatalf("expected editingNoteID cleared after save, got %q", a.board.editingNoteID)
	}
	bf, err = loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if bf.Tasks[0].Notes[0].Text != "第一条备注（已编辑）" {
		t.Fatalf("expected edited note persisted, got %+v", bf.Tasks[0].Notes)
	}
	editedTimeLabel := bf.Tasks[0].Notes[0].displayTime().Local().Format("01-02 15:04")
	if editedTimeLabel == oldTimeLabel {
		t.Fatalf("expected edited note time %q to differ from old time %q", editedTimeLabel, oldTimeLabel)
	}
	if !tt.HasText(editedTimeLabel) {
		t.Fatalf("expected updated note timestamp %q in UI: %q", editedTimeLabel, tt.Texts())
	}

	// Delete the first note: clicking the trash icon must ask for confirmation first.
	if err := tt.Click("删除备注"); err != nil {
		t.Fatalf("click delete note: %v", err)
	}
	tt.Frame()
	if a.board.deleteNoteTaskID != created.ID || a.board.deleteNoteID == "" {
		t.Fatalf("expected note delete confirmation step, task=%q note=%q", a.board.deleteNoteTaskID, a.board.deleteNoteID)
	}
	bf, err = loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(bf.Tasks[0].Notes) != 3 {
		t.Fatalf("note must survive until confirmed, got %+v", bf.Tasks[0].Notes)
	}
	cancelBtn, ok1 := tt.AccessByRole(ui.RoleButton, "取消")
	confirmBtn, ok2 := tt.AccessByRole(ui.RoleButton, "确认删除")
	if !ok1 || !ok2 {
		t.Fatalf("missing note delete modal buttons: ok1=%v ok2=%v", ok1, ok2)
	}
	if abs := func(v float32) float32 {
		if v < 0 {
			return -v
		}
		return v
	}; abs(cancelBtn.Bounds.Y-confirmBtn.Bounds.Y) > 0.5 || abs(cancelBtn.Bounds.H-confirmBtn.Bounds.H) > 0.5 {
		t.Fatalf("note delete modal buttons misaligned: cancel=%+v confirm=%+v", cancelBtn.Bounds, confirmBtn.Bounds)
	}
	saveLobePreview(t, tt, "board-delete-note-confirm")

	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click cancel note delete: %v", err)
	}
	tt.Frame()
	if a.board.deleteNoteTaskID != "" || a.board.deleteNoteID != "" {
		t.Fatalf("cancel should close note delete confirmation: task=%q note=%q", a.board.deleteNoteTaskID, a.board.deleteNoteID)
	}
	bf, err = loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(bf.Tasks[0].Notes) != 3 {
		t.Fatalf("cancel must keep all notes, got %+v", bf.Tasks[0].Notes)
	}

	if err := tt.Click("删除备注"); err != nil {
		t.Fatalf("click delete note again: %v", err)
	}
	tt.Frame()
	if err := tt.Click("确认删除"); err != nil {
		t.Fatalf("click confirm note delete: %v", err)
	}
	tt.Frame()
	bf, err = loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(bf.Tasks[0].Notes) != 2 || bf.Tasks[0].Notes[0].Text != "第二条备注" {
		t.Fatalf("expected first note deleted, got %+v", bf.Tasks[0].Notes)
	}
	if a.board.deleteNoteTaskID != "" || a.board.deleteNoteID != "" {
		t.Fatalf("confirm should close note delete confirmation: task=%q note=%q", a.board.deleteNoteTaskID, a.board.deleteNoteID)
	}
}

func TestBoardDrawerActionsMenu(t *testing.T) {
	withTempBoardConfig(t)
	_, created, err := boardAddTask("整理抽屉顶栏菜单", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{
		showBoard: true,
		dispatch:  func(fn func()) { fn() },
	}
	a.ensureBoardLoaded()
	a.selectBoardTask(created.ID)

	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	// Standalone archive/delete icon buttons should be consolidated under 任务操作.
	if _, ok := tt.AccessByRole(ui.RoleButton, "归档任务"); ok {
		t.Fatalf("expected standalone archive icon button removed from drawer header")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "删除任务"); ok {
		t.Fatalf("expected standalone delete icon button removed from drawer header")
	}

	if err := tt.Click("任务操作"); err != nil {
		t.Fatalf("click drawer actions button: %v", err)
	}
	if err := tt.ChooseMenuItem("归档任务"); err != nil {
		t.Fatalf("choose archive task: %v", err)
	}
	tt.Frame()

	bf, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if !bf.Tasks[0].Archived {
		t.Fatalf("expected task archived, got %+v", bf.Tasks[0])
	}
	if !tt.HasText("已归档") {
		t.Fatalf("expected archived badge in drawer header: %q", tt.Texts())
	}

	if err := tt.Click("任务操作"); err != nil {
		t.Fatalf("click drawer actions button for unarchive: %v", err)
	}
	if err := tt.ChooseMenuItem("取消归档"); err != nil {
		t.Fatalf("choose unarchive task: %v", err)
	}
	tt.Frame()

	bf, err = loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if bf.Tasks[0].Archived {
		t.Fatalf("expected task unarchived, got %+v", bf.Tasks[0])
	}

	if err := tt.Click("任务操作"); err != nil {
		t.Fatalf("click drawer actions button for delete: %v", err)
	}
	if err := tt.ChooseMenuItem("删除任务"); err != nil {
		t.Fatalf("choose delete task: %v", err)
	}
	tt.Frame()
	if a.board.deleteTaskID != created.ID {
		t.Fatalf("expected delete confirmation for %s, got %q", created.ID, a.board.deleteTaskID)
	}
}

func TestBoardBackupRestoreImportAndStorageModals(t *testing.T) {
	withTempBoardConfig(t)
	if _, _, err := boardAddTask("原始任务", "", boardTodo, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("误删任务", "", boardDoing, nil); err != nil {
		t.Fatal(err)
	}
	exportPath := filepath.Join(t.TempDir(), "backup.json")
	bundle, err := boardExport(context.Background(), boardExportOptions{IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBoardExportFile(exportPath, bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := boardDeleteTask("T-2"); err != nil {
		t.Fatal(err)
	}

	a := &app{showBoard: true, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	// Open storage modal via toolbar more menu
	if err := tt.Click("更多操作"); err != nil {
		t.Fatalf("click board more menu: %v", err)
	}
	if err := tt.ChooseMenuItem("数据目录…"); err != nil {
		t.Fatalf("choose data dir menu: %v", err)
	}
	tt.Frame()
	if !a.board.showStorageModal || !tt.HasText("看板数据目录") {
		t.Fatalf("storage modal did not open: %q", tt.Texts())
	}
	a.board.showStorageModal = false
	tt.Frame()

	// Open auto backup restore modal and restore T-2
	if err := tt.Click("更多操作"); err != nil {
		t.Fatalf("click board more menu for restore: %v", err)
	}
	if err := tt.ChooseMenuItem("从自动备份恢复…"); err != nil {
		t.Fatalf("choose restore backup menu: %v", err)
	}
	tt.Frame()
	if !a.board.showBackupsModal || !tt.HasText("恢复自动备份") || !tt.HasText("删除前") {
		t.Fatalf("backups modal missing expected items: %q", tt.Texts())
	}
	if err := tt.Click("恢复"); err != nil {
		t.Fatalf("click restore button: %v", err)
	}
	tt.Frame()
	if a.board.showBackupsModal || len(a.board.file.Tasks) != 2 {
		t.Fatalf("expected 2 tasks restored, modal=%v tasks=%d", a.board.showBackupsModal, len(a.board.file.Tasks))
	}

	// Open import modal from prepared plan (merge with conflict)
	plan, err := buildBoardImportPlan(context.Background(), exportPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.board.importPlan = plan
	a.board.importModeIdx = 0
	a.board.showImportModal = true
	tt.Frame()
	if !tt.HasText("导入看板备份") || !tt.HasText("2 个任务编号冲突，导入时将自动重新编号。") {
		t.Fatalf("import modal conflict notice missing: %q", tt.Texts())
	}
	if err := tt.Click("确认导入"); err != nil {
		t.Fatalf("click confirm import: %v", err)
	}
	tt.Frame()
	if a.board.showImportModal || len(a.board.file.Tasks) != 4 {
		t.Fatalf("expected 4 tasks after merge import, modal=%v tasks=%d", a.board.showImportModal, len(a.board.file.Tasks))
	}

	// Switch dir conflict modal
	targetDir := filepath.Join(t.TempDir(), "existing-board")
	if err := writeBoardFileAtomic(filepath.Join(targetDir, "board.json"), boardFile{
		Version: 1,
		NextSeq: 2,
		Tasks:   []boardTask{{ID: "T-1", Title: "远端任务", Status: boardTodo}},
	}); err != nil {
		t.Fatal(err)
	}
	a.beginSwitchBoardDir(targetDir)
	tt.Frame()
	if !a.board.showSwitchDirModal || !tt.HasText("目标目录已有看板数据") {
		t.Fatalf("switch dir conflict modal did not open: %q", tt.Texts())
	}
	if err := tt.Click("使用目标数据"); err != nil {
		t.Fatalf("click use target data: %v", err)
	}
	tt.Frame()
	if a.board.showSwitchDirModal || len(a.board.file.Tasks) != 1 || a.board.file.Tasks[0].Title != "远端任务" {
		t.Fatalf("expected switched to target dir board, modal=%v tasks=%+v", a.board.showSwitchDirModal, a.board.file.Tasks)
	}
}

func TestEnglishBoardBackupAndStorageModals(t *testing.T) {
	withTempBoardConfig(t)
	oldEn := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(oldEn) })

	a := &app{
		settings: settingsState{open: true},
		board: boardState{
			showImportModal: true,
			importModeIdx:   1,
			importPlan: boardImportPlan{
				SourcePath: "/tmp/board-backup.json",
				TotalTasks: 2,
				RepoMappings: []boardRepoMapping{
					{OldPath: "/missing/demo-repo", Status: "missing"},
				},
			},
		},
		dispatch: func(func()) {},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		a.settingsModal(c)
		a.boardModals(c)
	}, 1100, 820)
	defer retireTester(tt)
	tt.SetLocale("en")
	tt.Frame()
	assertEnglishUITexts(t, tt)

	a.settings.open = false
	a.board.showImportModal = false
	a.board.showBackupsModal = true
	a.board.backups = []boardBackupInfo{{
		Name:      "board-20261117-120000-delete.json",
		Reason:    "delete",
		CreatedAt: time.Now(),
		TaskCount: 3,
	}}
	a.board.showStorageModal = true
	a.board.showSwitchDirModal = true
	a.board.switchTargetDir = "/tmp/other-board"
	a.board.switchTargetCount = 4
	tt.Frame()
	assertEnglishUITexts(t, tt)
}

func TestBoardMissingCustomDirShowsRecoveryActions(t *testing.T) {
	withTempBoardConfig(t)
	missing := filepath.Join(t.TempDir(), "vanished-board")
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeBoardLocationConfig(missing); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(missing); err != nil {
		t.Fatal(err)
	}

	a := &app{showBoard: true, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 760)
	defer retireTester(tt)
	tt.Frame()
	if a.board.loadErr == "" || !tt.HasText("恢复备份…") || !tt.HasText("数据目录…") {
		t.Fatalf("expected missing-dir banner and recovery buttons, loadErr=%q texts=%q", a.board.loadErr, tt.Texts())
	}
}

func TestBoardUIPriorityAndDueDatePickers(t *testing.T) {
	withTempBoardConfig(t)
	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	a := &app{showBoard: true, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	// 1. Open new task modal and choose priority + due date
	if err := tt.Click("新建任务"); err != nil {
		t.Fatalf("click new task: %v", err)
	}
	tt.Frame()
	if !tt.HasText("重要紧急") || !tt.HasText("截止日期") {
		t.Fatalf("new task modal missing priority/due fields: %q", tt.Texts())
	}

	if err := tt.Click("请选择"); err != nil {
		t.Fatalf("open priority picker: %v", err)
	}
	tt.Frame()
	if !tt.HasText("重要且紧急") || !tt.HasText("重要不紧急") || !tt.HasText("紧急不重要") || !tt.HasText("不重要不紧急") {
		t.Fatalf("priority popover missing quadrants: %q", tt.Texts())
	}
	if err := tt.Click("重要且紧急"); err != nil {
		t.Fatalf("choose priority: %v", err)
	}
	tt.Frame()
	if a.board.newPriority != boardPriorityUrgentImportant || a.board.newPriorityOpen {
		t.Fatalf("priority not applied in modal: prio=%q open=%v", a.board.newPriority, a.board.newPriorityOpen)
	}

	if err := tt.Click("请选择日期"); err != nil {
		t.Fatalf("open due date picker: %v", err)
	}
	tt.Frame()
	if err := tt.Click("今天"); err != nil {
		t.Fatalf("choose today shortcut: %v", err)
	}
	tt.Frame()
	if a.board.newDueDate != today || a.board.newDueOpen {
		t.Fatalf("due date not applied in modal: due=%q open=%v", a.board.newDueDate, a.board.newDueOpen)
	}

	a.board.newTitle = "补齐截止日期与重要紧急"
	tt.Frame()
	if err := tt.Click("创建"); err != nil {
		t.Fatalf("click create: %v", err)
	}
	tt.Frame()

	bf, err := loadBoardFile()
	if err != nil || len(bf.Tasks) != 1 {
		t.Fatalf("loadBoardFile: %+v err=%v", bf, err)
	}
	if bf.Tasks[0].Priority != boardPriorityUrgentImportant || bf.Tasks[0].DueDate != today {
		t.Fatalf("created task missing priority/due: %+v", bf.Tasks[0])
	}
	if !tt.HasText("重要且紧急") || !tt.HasText("今天") {
		t.Fatalf("card/drawer missing priority/due texts: %q", tt.Texts())
	}

	// 2. Open detail drawer and edit priority in it
	if err := tt.Click("补齐截止日期与重要紧急"); err != nil {
		t.Fatalf("open created task drawer: %v", err)
	}
	tt.Frame()
	prioBtn, ok := tt.AccessByRole(ui.RoleButton, "重要且紧急")
	if !ok {
		t.Fatalf("missing drawer priority button: %q", tt.Texts())
	}
	tt.ClickAt(prioBtn.Bounds.X+prioBtn.Bounds.W/2, prioBtn.Bounds.Y+prioBtn.Bounds.H/2)
	tt.Frame()
	if !a.board.drawerPriorityOpen {
		t.Fatalf("drawer priority popover did not open")
	}
	if err := tt.Click("重要不紧急"); err != nil {
		t.Fatalf("change drawer priority: %v", err)
	}
	tt.Frame()
	bf, err = loadBoardFile()
	if err != nil || bf.Tasks[0].Priority != boardPriorityImportant {
		t.Fatalf("drawer priority update failed: %+v err=%v", bf.Tasks, err)
	}

	// Clear priority from drawer popover
	prioBtn, ok = tt.AccessByRole(ui.RoleButton, "重要不紧急")
	if !ok {
		t.Fatalf("missing updated drawer priority button: %q", tt.Texts())
	}
	tt.ClickAt(prioBtn.Bounds.X+prioBtn.Bounds.W/2, prioBtn.Bounds.Y+prioBtn.Bounds.H/2)
	tt.Frame()
	if err := tt.Click("清除"); err != nil {
		t.Fatalf("clear drawer priority: %v", err)
	}
	tt.Frame()
	bf, err = loadBoardFile()
	if err != nil || bf.Tasks[0].Priority != boardPriorityNone {
		t.Fatalf("drawer priority clear failed: %+v err=%v", bf.Tasks, err)
	}

	// 3. Edit due date in detail drawer using tomorrow preset, then clear inline
	dueBtn, ok := tt.AccessByRole(ui.RoleButton, today)
	if !ok {
		t.Fatalf("missing drawer due date button: %q", tt.Texts())
	}
	tt.ClickAt(dueBtn.Bounds.X+dueBtn.Bounds.W/2, dueBtn.Bounds.Y+dueBtn.Bounds.H/2)
	tt.Frame()
	if err := tt.Click("明天"); err != nil {
		t.Fatalf("choose tomorrow: %v", err)
	}
	tt.Frame()
	bf, err = loadBoardFile()
	if err != nil || bf.Tasks[0].DueDate != tomorrow {
		t.Fatalf("drawer due date update failed: %+v err=%v", bf.Tasks, err)
	}

	if err := tt.Click("清除截止日期"); err != nil {
		t.Fatalf("clear due date inline: %v", err)
	}
	tt.Frame()
	bf, err = loadBoardFile()
	if err != nil || bf.Tasks[0].DueDate != "" {
		t.Fatalf("drawer due date clear failed: %+v err=%v", bf.Tasks, err)
	}

	// 4. Verify overdue date label shows in drawer
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if _, _, err := boardUpdateTask(bf.Tasks[0].ID, boardTaskPatch{DueDate: &yesterday}); err != nil {
		t.Fatal(err)
	}
	a.reloadBoardSync()
	tt.Frame()
	if !tt.HasText("已逾期") {
		t.Fatalf("expected overdue indicator for yesterday due date: %q", tt.Texts())
	}
}

func TestBoardUIDueDateCalendarInteractions(t *testing.T) {
	withTempBoardConfig(t)
	now := time.Now()
	today := now.Format("2006-01-02")
	_, created, err := boardAddTaskWithOptions(boardAddTaskOptions{
		Title:  "验证日历浮层交互",
		Status: boardTodo,
	})
	if err != nil {
		t.Fatalf("boardAddTaskWithOptions: %v", err)
	}

	a := &app{showBoard: true, dispatch: func(func()) {}}
	a.board.selectedID = created.ID
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	// 1. Trigger alignment: priority & due date triggers align at same X and width when unset
	prioBtn, ok := tt.AccessByRole(ui.RoleButton, "请选择")
	if !ok {
		t.Fatalf("missing priority button: %q", tt.Texts())
	}
	dueBtn, ok := tt.AccessByRole(ui.RoleButton, "请选择日期")
	if !ok {
		t.Fatalf("missing due date button: %q", tt.Texts())
	}
	if prioBtn.Bounds.X != dueBtn.Bounds.X || prioBtn.Bounds.W != dueBtn.Bounds.W {
		t.Fatalf("picker triggers misaligned: prio=%+v due=%+v", prioBtn.Bounds, dueBtn.Bounds)
	}

	// 2. Open calendar and click Today directly on the calendar grid (even when no due date was set)
	if err := tt.Click("请选择日期"); err != nil {
		t.Fatalf("open due date calendar: %v", err)
	}
	tt.Frame()
	if !a.board.drawerDueOpen {
		t.Fatal("expected drawer due calendar to open")
	}

	todayLabel := i18n.FormatDate("zh-Hans", now, i18n.DateOptions{Skeleton: "yMMMMd"})
	todayCell, ok := tt.AccessByRole(ui.RoleButton, todayLabel)
	if !ok {
		t.Fatalf("missing today day cell %q in calendar: %q", todayLabel, tt.Texts())
	}
	tt.ClickAt(todayCell.Bounds.X+todayCell.Bounds.W/2, todayCell.Bounds.Y+todayCell.Bounds.H/2)
	tt.Frame()

	if a.board.drawerDueOpen {
		t.Fatal("expected calendar popover to close after selecting day")
	}
	bf, err := loadBoardFile()
	if err != nil || bf.Tasks[0].DueDate != today {
		t.Fatalf("expected today %q to be saved when clicked on grid, got %+v err=%v", today, bf.Tasks, err)
	}

	// 3. Inline clear button stays inside the trigger bounds
	dueBtn, ok = tt.AccessByRole(ui.RoleButton, today)
	if !ok {
		t.Fatalf("missing updated due button %q: %q", today, tt.Texts())
	}
	clearBtn, ok := tt.AccessByRole(ui.RoleButton, "清除截止日期")
	if !ok {
		t.Fatalf("missing inline clear button: %q", tt.Texts())
	}
	if clearBtn.Bounds.X < dueBtn.Bounds.X || clearBtn.Bounds.X+clearBtn.Bounds.W > dueBtn.Bounds.X+dueBtn.Bounds.W {
		t.Fatalf("inline clear button %+v outside trigger %+v", clearBtn.Bounds, dueBtn.Bounds)
	}

	// 4. Open calendar, step to next month, switch via year/month picker, and select the 15th
	tt.ClickAt(dueBtn.Bounds.X+dueBtn.Bounds.W/2, dueBtn.Bounds.Y+dueBtn.Bounds.H/2)
	tt.Frame()
	if !a.board.drawerDueOpen {
		t.Fatal("expected calendar popover to reopen")
	}
	if err := tt.Click("下个月"); err != nil {
		t.Fatalf("click next month: %v", err)
	}
	tt.Frame()
	if !a.board.drawerDueOpen {
		t.Fatal("navigating month must keep calendar open")
	}

	nextMonth := time.Date(now.Year(), now.Month()+1, 15, 0, 0, 0, 0, time.Local)
	nextMonthCellLabel := i18n.FormatDate("zh-Hans", nextMonth, i18n.DateOptions{Skeleton: "yMMMMd"})
	if err := tt.Click(nextMonthCellLabel); err != nil {
		t.Fatalf("click 15th of next month (%q): %v", nextMonthCellLabel, err)
	}
	tt.Frame()
	if a.board.drawerDueOpen {
		t.Fatal("expected calendar popover to close after picking next month day")
	}
	bf, err = loadBoardFile()
	if err != nil || bf.Tasks[0].DueDate != nextMonth.Format("2006-01-02") {
		t.Fatalf("expected %q, got %+v err=%v", nextMonth.Format("2006-01-02"), bf.Tasks, err)
	}

	// 5. Switch into year & month picker grids, then clear from inside popover
	dueBtn, ok = tt.AccessByRole(ui.RoleButton, nextMonth.Format("2006-01-02"))
	if !ok {
		t.Fatalf("missing due button after month pick: %q", tt.Texts())
	}
	tt.ClickAt(dueBtn.Bounds.X+dueBtn.Bounds.W/2, dueBtn.Bounds.Y+dueBtn.Bounds.H/2)
	tt.Frame()
	if err := tt.Click(fmt.Sprintf("%d年", nextMonth.Year())); err != nil {
		t.Fatalf("open year grid: %v", err)
	}
	tt.Frame()
	decadeStart := (nextMonth.Year() / 10) * 10
	if !tt.HasText(fmt.Sprintf("%d", decadeStart)) || !tt.HasText(fmt.Sprintf("%d", decadeStart+9)) {
		t.Fatalf("year grid missing decade years: %q", tt.Texts())
	}
	if err := tt.Click(fmt.Sprintf("%d", nextMonth.Year())); err != nil {
		t.Fatalf("pick year in year grid: %v", err)
	}
	tt.Frame()
	if err := tt.Click(fmt.Sprintf("%d月", nextMonth.Month())); err != nil {
		t.Fatalf("open month grid: %v", err)
	}
	tt.Frame()
	if !tt.HasText("1月") || !tt.HasText("12月") {
		t.Fatalf("month grid missing months: %q", tt.Texts())
	}
	if err := tt.Click("6月"); err != nil {
		t.Fatalf("pick June in month grid: %v", err)
	}
	tt.Frame()
	if !a.board.drawerDueOpen {
		t.Fatal("picking month must keep popover open for day selection")
	}

	if err := tt.Click("清除"); err != nil {
		t.Fatalf("clear due date inside popover: %v", err)
	}
	tt.Frame()
	if a.board.drawerDueOpen {
		t.Fatal("expected popover to close after clearing")
	}
	bf, err = loadBoardFile()
	if err != nil || bf.Tasks[0].DueDate != "" {
		t.Fatalf("expected cleared due date, got %+v err=%v", bf.Tasks, err)
	}
}

func TestBoardDetailDrawerClearsTitleBar(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_DATA_HOME", home)

	_, task, err := boardAddTask("Windows 标题栏避让", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, bar := range []ui.TitleBar{
		{Height: 40, Left: 0, Right: 138},
		{Height: 52, Left: 0, Right: 138},
	} {
		a := &app{
			showBoard: true,
			dispatch:  func(fn func()) { fn() },
		}
		a.ensureBoardLoaded()
		a.selectBoardTask(task.ID)

		tt := ui.NewTester(a.view, 1280, 820)
		tt.SetTitleBar(bar)
		tt.Frame()

		minY := max(bar.Height, chromeMinHeight)
		closeBtn, ok := tt.AccessByRole(ui.RoleButton, "关闭详情")
		if !ok {
			t.Fatalf("missing drawer close button: %q", tt.Texts())
		}
		if closeBtn.Bounds.Y < minY {
			t.Fatalf("drawer close button overlaps titlebar (bar=%+v): bounds=%+v minY=%v", bar, closeBtn.Bounds, minY)
		}

		tt.ClickAt(closeBtn.Bounds.X+closeBtn.Bounds.W/2, closeBtn.Bounds.Y+closeBtn.Bounds.H/2)
		tt.Frame()
		if a.board.selectedID != "" {
			t.Fatalf("expected clicking close button to close drawer, selectedID=%q", a.board.selectedID)
		}
	}
}

func TestLeaveBoardReleasesFilteredColumns(t *testing.T) {
	withTempBoardConfig(t)
	a := &app{showBoard: true, dispatch: func(func()) {}}
	a.board.loaded = true
	bf := boardFile{Version: 1, NextSeq: 3, Tasks: []boardTask{
		{ID: "T-1", Title: "待办任务", Status: boardTodo},
		{ID: "T-2", Title: "已完成任务", Status: boardDone},
	}}
	a.applyBoardFile(bf, nil)

	cols, total := a.boardFilteredByStatus()
	if total != len(bf.Tasks) || len(cols) != len(boardStatuses) {
		t.Fatalf("columns not built: total=%d cols=%d", total, len(cols))
	}

	a.leaveBoard()
	if a.board.filtered != nil || a.board.filteredTotal != 0 {
		t.Fatalf("leaving the board should release the filtered copies")
	}
	if _, total := a.boardFilteredByStatus(); total != len(bf.Tasks) {
		t.Fatalf("reopening should rebuild the columns, got total=%d", total)
	}
}
