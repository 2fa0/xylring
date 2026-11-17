package main

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func nextDialogUpdate(t *testing.T, updates <-chan func()) func() {
	t.Helper()
	select {
	case fn := <-updates:
		return fn
	case <-time.After(10 * time.Second):
		t.Fatal("等待弹窗读取超时")
		return nil
	}
}

func TestCommitWriteIgnoresPreviousDialogMessage(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "first message")
	first := mustResolve(t, dir, "HEAD")
	git("commit", "--allow-empty", "-m", "second message")
	second := mustResolve(t, dir, "HEAD")
	for _, name := range []string{"old-first", "old-last", "switch-to-tag"} {
		t.Run(name, func(t *testing.T) {
			tab := newTab(1, dir)
			tab.loaded = true
			updates := make(chan func(), 8)
			a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
			tt := ui.NewTester(a.view, 1100, 720)
			a.openCommitWrite(tab, writeMessage, commit{hash: first})
			oldUpdate := nextDialogUpdate(t, updates)
			tt.Frame()
			if err := tt.Click("取消"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			kind := writeMessage
			if name == "switch-to-tag" {
				kind = writeTag
			}
			a.openCommitWrite(tab, kind, commit{hash: second})
			if kind == writeTag {
				oldUpdate()
				if a.write.message != "" || a.write.err != "" {
					t.Fatalf("old message reached tag dialog: %+v", a.write)
				}
			} else {
				newUpdate := nextDialogUpdate(t, updates)
				if name == "old-first" {
					oldUpdate()
					if !a.write.preparing || a.write.message != "" || a.write.err != "" {
						t.Fatalf("old result completed new dialog: %+v", a.write)
					}
					newUpdate()
				} else {
					newUpdate()
					oldUpdate()
				}
				if a.write.preparing || a.write.message != "second message" || a.write.err != "" {
					t.Fatalf("wrong message in current dialog: %+v", a.write)
				}
			}
			tt.Frame()
			tt.Frame()
			if a.write.hash != second || a.write.kind != kind {
				t.Fatalf("dialog target changed: %+v", a.write)
			}
		})
	}
}

func graphTabForWrite() *repoTab {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2)}, author: "Ada", subject: "画出提交图谱", when: 1700000000},
		{hash: hashN(2), author: "Ada", subject: "初始提交", when: 1690000000},
	}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.head = tab.commits[0].hash
	tab.refs = map[[20]byte][]refInfo{tab.head: {{name: "main", kind: refBranch}}}
	return tab
}

func TestCommitRowMenuOpensBranchDialog(t *testing.T) {
	tab := graphTabForWrite()
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.RightClick("画出提交图谱"); err != nil {
		t.Fatal(err)
	}
	got := tt.Menu()
	for _, item := range []string{"添加标签…", "新建分支…", "修改说明…", "复制哈希", "复制提交信息", "比较", "重置当前分支"} {
		found := false
		for _, m := range got {
			if m == item {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("menu %q missing %q", got, item)
		}
	}
	for _, item := range []string{"硬重置…", "合并…", "以此提交进行比较…"} {
		for _, m := range got {
			if m == item {
				t.Fatalf("menu %q still has %q at top level", got, item)
			}
		}
	}
	if err := tt.ChooseMenuItem("新建分支…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, text := range []string{"新建分支", "创建后检出", "覆盖已有同名分支", "创建"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in %q", text, tt.Texts())
		}
	}
	if tt.HasText("跟踪") {
		t.Fatalf("unexpected track in %q", tt.Texts())
	}
	if a.write.kind != writeBranch || a.write.hash != hashN(1) || a.write.track || a.write.name != "" || a.write.checkout {
		t.Fatalf("dialog %+v", a.write)
	}
}

func TestCommitRowMenuTracksUniqueRemote(t *testing.T) {
	tab := graphTabForWrite()
	tab.refs[hashN(2)] = []refInfo{
		{name: "origin/HEAD", kind: refRemote},
		{name: "origin/feature", kind: refRemote},
	}
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.RightClick("初始提交"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("新建分支…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("跟踪 origin/feature") {
		t.Fatalf("missing track in %q", tt.Texts())
	}
	if a.write.name != "feature" || !a.write.track || a.write.trackRemote != "origin/feature" || !a.write.checkout {
		t.Fatalf("dialog %+v", a.write)
	}
}

func TestCommitRowMenuDoesNotPrefillExistingLocal(t *testing.T) {
	tab := graphTabForWrite()
	tab.refs[tab.head] = []refInfo{
		{name: "main", kind: refBranch},
		{name: "origin/main", kind: refRemote},
	}
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.RightClick("画出提交图谱"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("新建分支…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("跟踪 origin/main") {
		t.Fatalf("missing track in %q", tt.Texts())
	}
	if a.write.name != "" || a.write.track || a.write.checkout || a.write.trackRemote != "origin/main" {
		t.Fatalf("dialog %+v", a.write)
	}
}

func TestCommitRowMenuOpensTagAndMessageDialogs(t *testing.T) {
	tab := graphTabForWrite()
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.RightClick("初始提交"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("添加标签…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("添加标签") || !tt.HasText("附注标签") || a.write.hash != hashN(2) {
		t.Fatalf("tag dialog texts=%q write=%+v", tt.Texts(), a.write)
	}
	a.showWrite = false
	tt.Frame()
	if err := tt.RightClick("画出提交图谱"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("修改说明…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("修改说明") || a.write.kind != writeMessage {
		t.Fatalf("message dialog texts=%q write=%+v", tt.Texts(), a.write)
	}
}

func TestWorktreeRowHasNoCommitWriteMenu(t *testing.T) {
	tab := graphTabForWrite()
	tab.worktree = true
	tab.worktreeCount = 1
	tab.worktreeFiles = []fileChange{{status: "M", path: "a.txt"}}
	tab.syncWorktreeCommit()
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("未提交改动") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.RightClick("未提交改动"); err != nil {
		t.Fatal(err)
	}
	if m := tt.Menu(); len(m) != 0 {
		t.Fatalf("worktree menu %q", m)
	}
}

func TestCommitRowMenuCopiesHashAndInfo(t *testing.T) {
	tab := graphTabForWrite()
	tab.inspecting = hashN(1)
	tab.details = &commitDetails{formatted: "完整提交信息\n第二行"}
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.RightClick("画出提交图谱"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("复制哈希"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != hashHex(hashN(1)) {
		t.Fatalf("hash clipboard %q", tt.Clipboard())
	}
	if err := tt.RightClick("画出提交图谱"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("复制提交信息"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "完整提交信息\n第二行" {
		t.Fatalf("info clipboard %q", tt.Clipboard())
	}
}

func TestBranchBadgeDoesNotCheckoutOnDoubleClick(t *testing.T) {
	tab := graphTabForWrite()
	tab.branch = "feature"
	tab.refs = map[[20]byte][]refInfo{tab.head: {{name: "main", kind: refBranch}}}
	a := &app{
		tabs:     []*repoTab{tab},
		dispatch: func(fn func()) {},
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	rect, found := tt.Find("main")
	if !found {
		t.Fatal("expected to find branch badge 'main'")
	}
	oldGen := a.write.gen
	// Double click the branch badge
	tt.ClickAt(rect.X+2, rect.Y+2)
	tt.ClickAt(rect.X+2, rect.Y+2)
	tt.Frame()

	if a.write.busy || a.write.gen != oldGen {
		t.Fatal("expected branch badge double click NOT to trigger checkout / write.busy")
	}
}

func TestBranchBadgeContextMenuCheckout(t *testing.T) {
	tab := graphTabForWrite()
	tab.branch = "feature"
	tab.refs = map[[20]byte][]refInfo{tab.head: {{name: "main", kind: refBranch}}}
	a := &app{
		tabs:     []*repoTab{tab},
		dispatch: func(fn func()) {},
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	if err := tt.RightClick("main"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	if !slices.Contains(menu, "检出分支 main") {
		t.Fatalf("expected menu to contain '检出分支 main', got: %v", menu)
	}
	if err := tt.ChooseMenuItem("检出分支 main"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.write.busy {
		t.Fatal("expected write.busy to be true after choosing 检出分支 main")
	}
}

func TestBranchBadgeContextMenuCurrentBranchDisabled(t *testing.T) {
	tab := graphTabForWrite()
	tab.branch = "main"
	tab.refs = map[[20]byte][]refInfo{tab.head: {{name: "main", kind: refBranch}}}
	a := &app{
		tabs:     []*repoTab{tab},
		dispatch: func(fn func()) {},
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	if err := tt.RightClick("main"); err != nil {
		t.Fatal(err)
	}
	oldGen := a.write.gen
	_ = tt.ChooseMenuItem("检出分支 main")
	tt.Frame()
	if a.write.busy || a.write.gen != oldGen {
		t.Fatal("expected choosing disabled current branch not to trigger checkout")
	}
}

func TestRunCheckoutBranchCurrentBranch(t *testing.T) {
	tab := graphTabForWrite()
	tab.branch = "main"
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) {}}
	a.runCheckoutBranch(tab, "main")
	if a.write.busy {
		t.Fatal("expected write.busy to be false when checking out current branch")
	}
	if a.banner != "当前已处于分支 main" {
		t.Fatalf("expected banner '当前已处于分支 main', got %q", a.banner)
	}
}
