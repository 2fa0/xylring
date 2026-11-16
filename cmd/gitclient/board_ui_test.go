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
	if err := tt.Click("+ 新建任务"); err != nil {
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
	if !tt.HasText("任务描述") || !tt.HasText("追加备注") {
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
