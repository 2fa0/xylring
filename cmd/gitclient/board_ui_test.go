package main

import (
	"testing"

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
	a.board.newTitle = "完善看板交互"
	a.board.newDesc = "支持备注与多提交绑定"
	if err := tt.Click("创建"); err != nil {
		t.Fatalf("click create: %v", err)
	}
	tt.Frame()
	if a.board.showNewModal || a.board.selectedID != "T-1" {
		t.Fatalf("task not created/selected: modal=%v sel=%q", a.board.showNewModal, a.board.selectedID)
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

	_, created, err := boardAddTask("English Task", "English description", boardDoing, []string{"/tmp/example"})
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
			// Test filter empty banner + clear filter
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
