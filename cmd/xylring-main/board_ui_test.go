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

func TestBoardContextWallFocusTabsAndPinnedOrdering(t *testing.T) {
	withTempBoardConfig(t)
	_, tTodo, err := boardAddTask("普通待办任务", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, tDoing, err := boardAddTask("正在进行的任务", "", boardDoing, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, tReview, err := boardAddTask("等待审阅的任务", "", boardReview, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, tPinned, err := boardAddTask("置顶聚焦任务", "", boardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	pin := true
	if _, _, err := boardUpdateTask(tPinned.ID, boardTaskPatch{Pinned: &pin}); err != nil {
		t.Fatal(err)
	}
	_ = tTodo

	// Verify attention-weight ordering: Pinned > Review > Doing > Todo
	a := &app{
		showBoard: true,
		dispatch:  func(fn func()) { fn() },
	}
	a.ensureBoardLoaded()

	var wall []boardTask
	cols, _ := a.boardFilteredByStatus()
	for _, st := range []boardStatus{boardReview, boardDoing, boardTodo} {
		wall = append(wall, cols[boardStatusIndex(st)]...)
	}
	sortBoardWallTasks(wall)
	gotOrder := make([]string, len(wall))
	for i, task := range wall {
		gotOrder[i] = task.ID
	}
	wantOrder := []string{tPinned.ID, tReview.ID, tDoing.ID, tTodo.ID}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Fatalf("unexpected wall attention order: got %v want %v", gotOrder, wantOrder)
	}

	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	// All active tasks visible by default
	for _, title := range []string{"置顶聚焦任务", "等待审阅的任务", "正在进行的任务", "普通待办任务"} {
		if !tt.HasText(title) {
			t.Fatalf("expected %q visible in All Active view: %q", title, tt.Texts())
		}
	}

	// Filter to 待审阅
	if err := tt.Click("待审阅"); err != nil {
		t.Fatalf("click review focus pill: %v", err)
	}
	tt.Frame()
	if a.board.statusFilter != boardReview {
		t.Fatalf("expected statusFilter=review, got %q", a.board.statusFilter)
	}
	if !tt.HasText("等待审阅的任务") || tt.HasText("正在进行的任务") || tt.HasText("普通待办任务") {
		t.Fatalf("review filter mismatch: %q", tt.Texts())
	}

	// Filter to 进行中
	if err := tt.Click("进行中"); err != nil {
		t.Fatalf("click doing focus pill: %v", err)
	}
	tt.Frame()
	if a.board.statusFilter != boardDoing || !tt.HasText("正在进行的任务") || tt.HasText("等待审阅的任务") {
		t.Fatalf("doing filter mismatch: filter=%q texts=%q", a.board.statusFilter, tt.Texts())
	}

	// Return to 全部未完成
	if err := tt.Click("全部未完成"); err != nil {
		t.Fatalf("click all active pill: %v", err)
	}
	tt.Frame()
	if a.board.statusFilter != "" || !tt.HasText("置顶聚焦任务") || !tt.HasText("普通待办任务") {
		t.Fatalf("all active filter mismatch: filter=%q texts=%q", a.board.statusFilter, tt.Texts())
	}
}

func TestBoardContextWallCardChecklistCommitAndCopyContext(t *testing.T) {
	withTempBoardConfig(t)
	_, created, err := boardAddTask(
		"卡片直接交互验证",
		"- [ ] 补齐检查项测试\n- [x] 完成数据模型",
		boardDoing,
		[]string{"/tmp/xylring"},
	)
	if err != nil {
		t.Fatal(err)
	}
	commitHash := "abcdef1234567890abcdef1234567890abcdef12"
	if _, _, err := boardLinkCommit(created.ID, boardCommitLink{
		Repo:    "/tmp/xylring",
		Hash:    commitHash,
		Subject: "feat: context wall card",
		Agent:   "Codex",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddNote(created.ID, "断点：待验证内联勾选", "Codex", "GPT-5"); err != nil {
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

	// 1. Toggle checklist item directly on card without opening drawer
	if err := tt.Click("补齐检查项测试"); err != nil {
		t.Fatalf("click checklist item on card: %v", err)
	}
	tt.Frame()
	if a.board.selectedID != "" {
		t.Fatalf("toggling checklist item should not open drawer, selectedID=%q", a.board.selectedID)
	}
	bf, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	items := parseBoardChecklist(bf.Tasks[0].Description)
	if len(items) != 2 || !items[0].Checked {
		t.Fatalf("expected first checklist item checked after click, got %+v (desc=%q)", items, bf.Tasks[0].Description)
	}

	// 2. Copy Agent Context action on card copies formatted context without opening drawer
	if err := tt.Click("复制 Agent 上下文"); err != nil {
		t.Fatalf("click copy agent context: %v", err)
	}
	tt.Frame()
	if a.board.selectedID != "" {
		t.Fatalf("copying agent context should not open drawer, selectedID=%q", a.board.selectedID)
	}
	clip := tt.Clipboard()
	if clip == "" || !slices.Contains([]string{created.ID}, created.ID) || !hasSubstr(clip, created.ID) || !hasSubstr(clip, "abcdef1") || !hasSubstr(clip, "断点：待验证内联勾选") {
		t.Fatalf("unexpected copied agent context: %q", clip)
	}

	// 3. Pin action on card toggles Pinned without opening drawer
	if err := tt.Click("置顶聚焦"); err != nil {
		t.Fatalf("click pin on card: %v", err)
	}
	tt.Frame()
	if a.board.selectedID != "" {
		t.Fatalf("pinning card should not open drawer, selectedID=%q", a.board.selectedID)
	}
	bf, err = loadBoardFile()
	if err != nil || !bf.Tasks[0].Pinned {
		t.Fatalf("expected task pinned after click, got %+v err=%v", bf.Tasks, err)
	}

	// 4. Embedded recent commit chip click triggers openBoardCommit without opening drawer
	if err := tt.Click("abcdef1 feat: context wall card"); err != nil {
		t.Fatalf("click embedded commit chip: %v", err)
	}
	tt.Frame()
	if a.board.selectedID != "" {
		t.Fatalf("clicking embedded commit should not open drawer, selectedID=%q", a.board.selectedID)
	}
}

func hasSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestBoardContextWallDoneSectionCollapsedAndExpandable(t *testing.T) {
	withTempBoardConfig(t)
	if _, _, err := boardAddTask("活跃进行中任务", "", boardDoing, nil); err != nil {
		t.Fatal(err)
	}
	_, doneTask, err := boardAddTask("已完成低权重任务", "", boardDone, nil)
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

	// Collapsed by default: done task title is hidden
	if a.board.doneExpanded {
		t.Fatal("done section should be collapsed by default")
	}
	if tt.HasText("已完成低权重任务") {
		t.Fatalf("done task should not be visible while collapsed: %q", tt.Texts())
	}

	// Expand done section
	if err := tt.Click("已完成"); err != nil {
		t.Fatalf("click done section toggle: %v", err)
	}
	tt.Frame()
	if !a.board.doneExpanded || !tt.HasText("已完成低权重任务") || !tt.HasText("归档已完成任务") {
		t.Fatalf("done section should expand and show done task & archive action: expanded=%v texts=%q", a.board.doneExpanded, tt.Texts())
	}

	// Quick restore button moves done task back to todo without opening drawer
	if err := tt.Click("移回待办"); err != nil {
		t.Fatalf("click restore done task: %v", err)
	}
	tt.Frame()
	if a.board.selectedID != "" {
		t.Fatalf("restoring done task should not open drawer, selectedID=%q", a.board.selectedID)
	}
	bf, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	idx := findBoardTaskIndex(bf.Tasks, doneTask.ID)
	if idx < 0 || bf.Tasks[idx].Status != boardTodo {
		t.Fatalf("expected %s restored to todo, got %+v", doneTask.ID, bf.Tasks)
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

	// Drag first note down over the third note (near bottom edge) to reorder.
	// Note: the latest note also appears on the card front on the left, so derive
	// the third note's position inside the drawer from the first two drawer notes.
	firstNote, ok1 := tt.Find("第一条备注")
	secondNote, ok2 := tt.Find("第二条备注")
	if !ok1 || !ok2 {
		t.Fatalf("missing notes: %q", tt.Texts())
	}
	stepY := secondNote.Y - firstNote.Y
	fromX, fromY := firstNote.X+firstNote.W/2, firstNote.Y+firstNote.H/2
	toX, toY := firstNote.X+firstNote.W/2, secondNote.Y+stepY+firstNote.H+2
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
	topNote, okTop := tt.Find("第二条备注")
	midNote, okMid := tt.Find("第三条备注")
	if !okTop || !okMid {
		t.Fatalf("missing reordered notes: %q", tt.Texts())
	}
	stepY = midNote.Y - topNote.Y
	fromX, fromY = topNote.X+topNote.W/2, midNote.Y+stepY+topNote.H/2
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

func TestBoardInlineQuickCapture(t *testing.T) {
	withTempBoardConfig(t)
	a := &app{
		showBoard: true,
		dispatch:  func(fn func()) { fn() },
	}
	a.ensureBoardLoaded()
	a.board.statusFilter = boardDoing

	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	quickInput, ok := tt.AccessByRole(ui.RoleTextField, "快速新建任务")
	if !ok {
		t.Fatalf("missing inline quick capture input: %q", tt.Texts())
	}
	tt.ClickAt(quickInput.Bounds.X+quickInput.Bounds.W/2, quickInput.Bounds.Y+quickInput.Bounds.H/2)
	a.board.quickTitle = "极简速记新任务"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if a.board.quickTitle != "" {
		t.Fatalf("expected quickTitle cleared after submit, got %q", a.board.quickTitle)
	}
	if a.board.selectedID != "" {
		t.Fatalf("quick capture should not open drawer, selectedID=%q", a.board.selectedID)
	}
	bf, err := loadBoardFile()
	if err != nil || len(bf.Tasks) != 1 {
		t.Fatalf("expected 1 created task, got %+v err=%v", bf.Tasks, err)
	}
	if bf.Tasks[0].Title != "极简速记新任务" || bf.Tasks[0].Status != boardDoing {
		t.Fatalf("unexpected quick captured task: %+v", bf.Tasks[0])
	}
	if !tt.HasText("极简速记新任务") {
		t.Fatalf("quick captured task card missing: %q", tt.Texts())
	}
}

func TestBoardHeroSectionAndChecklistProgress(t *testing.T) {
	withTempBoardConfig(t)
	_, regularTask, err := boardAddTask("普通瀑布流任务", "", boardReview, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, heroTask, err := boardAddTask(
		"置顶焦点 Hero 任务",
		"- [ ] 完成视觉层级调优\n- [x] 建立基准测试",
		boardDoing,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	pin := true
	if _, _, err := boardUpdateTask(heroTask.ID, boardTaskPatch{Pinned: &pin}); err != nil {
		t.Fatal(err)
	}
	_ = regularTask

	a := &app{
		showBoard: true,
		dispatch:  func(fn func()) { fn() },
	}
	a.ensureBoardLoaded()

	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()

	heroBounds, ok1 := tt.Find("置顶焦点 Hero 任务")
	regBounds, ok2 := tt.Find("普通瀑布流任务")
	if !ok1 || !ok2 {
		t.Fatalf("missing hero or regular card: ok1=%v ok2=%v texts=%q", ok1, ok2, tt.Texts())
	}
	if heroBounds.Y >= regBounds.Y {
		t.Fatalf("expected pinned Hero card above regular masonry cards: hero=%+v regular=%+v", heroBounds, regBounds)
	}
	if !tt.HasText("1/2") {
		t.Fatalf("expected checklist progress 1/2: %q", tt.Texts())
	}

	if err := tt.Click("完成视觉层级调优"); err != nil {
		t.Fatalf("click checklist item: %v", err)
	}
	tt.Frame()
	if !tt.HasText("2/2") {
		t.Fatalf("expected checklist progress updated to 2/2: %q", tt.Texts())
	}
}

func TestBoardExternalUpdateMarksRecentPulse(t *testing.T) {
	now := time.Now().UTC()
	prev := []boardTask{
		{ID: "T-1", Title: "Unchanged", Status: boardTodo, UpdatedAt: now.Add(-time.Minute)},
		{ID: "T-2", Title: "Updated", Status: boardDoing, UpdatedAt: now.Add(-time.Minute)},
	}
	next := []boardTask{
		{ID: "T-1", Title: "Unchanged", Status: boardTodo, UpdatedAt: now.Add(-time.Minute)},
		{ID: "T-2", Title: "Updated", Status: boardReview, UpdatedAt: now},
		{ID: "T-3", Title: "Newly Added", Status: boardTodo, UpdatedAt: now},
	}

	a := &app{
		board: boardState{
			recentUpdated: map[string]time.Time{
				"T-OLD": time.Now().Add(-30 * time.Second),
			},
		},
	}
	a.markExternalBoardUpdates(prev, next)

	if _, ok := a.board.recentUpdated["T-OLD"]; ok {
		t.Fatal("stale recentUpdated entry should be pruned")
	}
	if _, ok := a.board.recentUpdated["T-1"]; ok {
		t.Fatal("unchanged task should not be marked recently updated")
	}
	if _, ok := a.board.recentUpdated["T-2"]; !ok {
		t.Fatal("modified task T-2 should be marked recently updated")
	}
	if _, ok := a.board.recentUpdated["T-3"]; !ok {
		t.Fatal("newly added task T-3 should be marked recently updated")
	}
}

func TestBoardCraftWallShowcasePreviews(t *testing.T) {
	withTempBoardConfig(t)
	updates := make(chan func(), 64)
	tab1 := newTab(1, "/tmp/xylring")
	tab1.loaded, tab1.branch = true, "feat/context-wall-board"
	tab1.commits = []commit{{hash: hashN(1), author: "Ada", subject: "feat(board): context wall", when: 1700000000}}
	tab1.graph.add(&tab1.commits[0])
	defer tab1.cancel()

	tab2 := newTab(2, "/tmp/mygo-engine")
	tab2.loaded, tab2.branch = true, "main"
	tab2.commits = []commit{{hash: hashN(2), author: "Bob", subject: "perf(gpu): batch shadows", when: 1700000100}}
	tab2.graph.add(&tab2.commits[0])
	defer tab2.cancel()

	_, tHero, err := boardAddTaskWithOptions(boardAddTaskOptions{
		Title:       "升级并行 Agent 上下文墙视觉质感",
		Description: "- [x] 顶部滑动聚焦胶囊与速记框\n- [x] Hero 焦点区与双层光影卡片\n- [ ] 拖拽改状态与呼吸感微交互",
		Status:      boardDoing,
		Priority:    boardPriorityUrgentImportant,
		DueDate:     time.Now().Format("2006-01-02"),
		Repos:       []string{"/tmp/xylring"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pin := true
	if _, _, err := boardUpdateTask(tHero.ID, boardTaskPatch{Pinned: &pin}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardLinkCommit(tHero.ID, boardCommitLink{
		Repo:    "/tmp/xylring",
		Hash:    "762ae643754c80756f7de6346b93e4523d70dd64",
		Subject: "feat(board): 重构看板为并行 Agent 上下文墙",
		Agent:   "Antigravity",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddNote(tHero.ID, "Hero 焦点卡与渐变光晕已就绪，正在验证亮暗色对比度。", "Antigravity", "Gemini 4"); err != nil {
		t.Fatal(err)
	}

	_, tReview, err := boardAddTaskWithOptions(boardAddTaskOptions{
		Title:       "审阅阴影批处理与圆角裁剪性能",
		Description: "- [x] 验证 Metal 阴影合批\n- [ ] 核对回归测试通过率",
		Status:      boardReview,
		Priority:    boardPriorityImportant,
		Repos:       []string{"/tmp/mygo-engine"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardLinkCommit(tReview.ID, boardCommitLink{
		Repo:    "/tmp/mygo-engine",
		Hash:    "2222222222222222222222222222222222222222",
		Subject: "perf(gpu): batch rounded rect shadows",
		Agent:   "Codex",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddNote(tReview.ID, "已补充基准测试，等待审阅合并。", "Codex", "GPT-5"); err != nil {
		t.Fatal(err)
	}

	_, tDoing, err := boardAddTask("接入桌面端自动更新签名校验", "补充 macOS 公证后回执校验与失败降级提示。", boardDoing, []string{"/tmp/xylring"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddNote(tDoing.ID, "已完成证书链解析。", "Claude", "Sonnet"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("整理设置页与快捷键映射文档", "", boardTodo, []string{"/tmp/xylring"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTask("修复 Windows 锁文件竞争问题", "", boardDone, []string{"/tmp/xylring"}); err != nil {
		t.Fatal(err)
	}

	for _, dark := range []bool{false, true} {
		a := &app{
			tabs:            []*repoTab{tab1, tab2},
			showBoard:       true,
			bottomFullWidth: true,
			dispatch:        func(fn func()) { updates <- fn },
			board: boardState{
				recentUpdated: map[string]time.Time{
					tReview.ID: time.Now(),
				},
				commitValid: map[string]bool{
					boardCommitCacheKey("/tmp/xylring", "762ae643754c80756f7de6346b93e4523d70dd64"):     true,
					boardCommitCacheKey("/tmp/mygo-engine", "2222222222222222222222222222222222222222"): true,
				},
			},
		}
		a.ensureBoardLoaded()

		tt := ui.NewTester(a.view, 1280, 820)
		tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 72, Right: 10})
		tt.SetDark(dark)
		tt.Frame()
		drainUpdates(updates)
		tt.Frame()

		if !tt.HasText(tHero.Title) || !tt.HasText(tReview.Title) || !tt.HasText(tDoing.Title) {
			retireTester(tt)
			t.Fatalf("showcase missing expected tasks: %q", tt.Texts())
		}

		mode := "light"
		if dark {
			mode = "dark"
		}
		saveLobePreview(t, tt, "board-wall-"+mode)

		a.selectBoardTask(tHero.ID)
		tt.Frame()
		drainUpdates(updates)
		tt.Frame()
		saveLobePreview(t, tt, "board-drawer-"+mode)

		retireTester(tt)
	}
}

func TestBoardWallSortModesAndSessionPersistence(t *testing.T) {
	now := time.Now()
	today := now.Format("2006-01-02")
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")
	base := now.UTC()

	pinned := boardTask{
		ID: "T-1", Title: "置顶任务", Status: boardTodo, Pinned: true,
		Priority: boardPriorityLow, UpdatedAt: base.Add(-3 * time.Hour),
	}
	reviewOld := boardTask{
		ID: "T-2", Title: "旧审阅低优", Status: boardReview,
		Priority: boardPriorityLow, DueDate: tomorrow, UpdatedAt: base.Add(-2 * time.Hour),
	}
	doingUrgent := boardTask{
		ID: "T-3", Title: "进行中高优今日截止", Status: boardDoing,
		Priority: boardPriorityUrgentImportant, DueDate: today, UpdatedAt: base.Add(-time.Hour),
	}
	todoNewest := boardTask{
		ID: "T-4", Title: "刚更新待办", Status: boardTodo,
		Priority: boardPriorityImportant, UpdatedAt: base,
	}

	taskIDs := func(mode boardSortMode) []string {
		list := []boardTask{todoNewest, doingUrgent, reviewOld, pinned}
		sortBoardWallTasksByMode(list, mode)
		ids := make([]string, len(list))
		for i, task := range list {
			ids[i] = task.ID
		}
		return ids
	}

	if got, want := taskIDs(boardSortDefault), []string{"T-1", "T-2", "T-3", "T-4"}; !slices.Equal(got, want) {
		t.Fatalf("default sort mismatch: got %v want %v", got, want)
	}
	if got, want := taskIDs(boardSortRecent), []string{"T-1", "T-4", "T-3", "T-2"}; !slices.Equal(got, want) {
		t.Fatalf("recent sort mismatch: got %v want %v", got, want)
	}
	if got, want := taskIDs(boardSortPriority), []string{"T-1", "T-3", "T-4", "T-2"}; !slices.Equal(got, want) {
		t.Fatalf("priority sort mismatch: got %v want %v", got, want)
	}
	if got, want := taskIDs(boardSortDue), []string{"T-1", "T-3", "T-2", "T-4"}; !slices.Equal(got, want) {
		t.Fatalf("due sort mismatch: got %v want %v", got, want)
	}

	// UI sort menu switches mode and reorders cards
	withTempBoardConfig(t)
	if _, _, err := boardAddTaskWithOptions(boardAddTaskOptions{
		Title: "普通审阅任务", Status: boardReview, Priority: boardPriorityLow,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := boardAddTaskWithOptions(boardAddTaskOptions{
		Title: "紧急待办任务", Status: boardTodo, Priority: boardPriorityUrgentImportant,
	}); err != nil {
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

	// Default sort places review before todo
	revBefore, ok1 := tt.Find("普通审阅任务")
	todoBefore, ok2 := tt.Find("紧急待办任务")
	if !ok1 || !ok2 || revBefore.X >= todoBefore.X {
		t.Fatalf("expected review card left of todo in default sort: rev=%+v todo=%+v", revBefore, todoBefore)
	}

	if err := tt.Click("默认排序"); err != nil {
		t.Fatalf("open sort menu: %v", err)
	}
	if err := tt.ChooseMenuItem("重要紧急"); err != nil {
		t.Fatalf("choose priority sort: %v", err)
	}
	tt.Frame()
	tt.Advance(350 * time.Millisecond)
	tt.Frame()
	if a.board.sortMode != boardSortPriority {
		t.Fatalf("expected sortMode=priority, got %q", a.board.sortMode)
	}
	if !tt.HasText("重要紧急") {
		t.Fatalf("expected active sort label in button: %q", tt.Texts())
	}

	revAfter, ok1 := tt.Find("普通审阅任务")
	todoAfter, ok2 := tt.Find("紧急待办任务")
	if !ok1 || !ok2 || todoAfter.X >= revAfter.X {
		t.Fatalf("expected urgent_important card left of low-priority review card: todo=%+v rev=%+v", todoAfter, revAfter)
	}

	// Session restore & fallback
	var sApp app
	sApp.applySharedSessionState(savedSession{BoardSort: "recent"})
	if sApp.board.sortMode != boardSortRecent {
		t.Fatalf("expected restored recent sort, got %q", sApp.board.sortMode)
	}
	var fallbackApp app
	fallbackApp.applySharedSessionState(savedSession{BoardSort: "unknown-mode"})
	if fallbackApp.board.sortMode != boardSortDefault {
		t.Fatalf("expected unknown sort to fall back to default, got %q", fallbackApp.board.sortMode)
	}

	// The mode also survives the file: write the session the app saves, read it
	// back the way a restart does, and restore the mode from what came back.
	sessionPath, err := configFile("session.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionPath, []byte(`{"board_sort":"due","repos":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if loaded := loadSession(); loaded.BoardSort != string(boardSortDue) {
		t.Fatalf("session file kept board_sort=%q, want %q", loaded.BoardSort, boardSortDue)
	}
	var restarted app
	restarted.applySharedSessionState(loadSession())
	if restarted.board.sortMode != boardSortDue {
		t.Fatalf("restored sort after reopening the session = %q, want %q", restarted.board.sortMode, boardSortDue)
	}
}
