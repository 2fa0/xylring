package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDragAndDropOpenRepo(t *testing.T) {
	dir := t.TempDir()
	initBareAndWorktree(t, dir)

	updates := make(chan func(), 64)
	a := &app{
		dispatch: func(fn func()) {
			updates <- fn
		},
		recentWrites: make(chan []string, 4),
	}

	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if !tt.HasText("支持直接拖拽文件夹至窗口") {
		t.Fatal("empty state missing drag & drop hint")
	}

	// Simulate dropping the repository directory into the window
	taken := tt.DropFiles(200, 200, dir)
	if !taken {
		t.Fatal("DropFiles was not accepted by the window")
	}

	// Drain background scan updates
	waitUpdates(t, updates, func() bool {
		return a.activeTab() != nil && a.activeTab().loaded
	})

	tab := a.activeTab()
	if tab == nil || tab.path != dir {
		t.Fatalf("expected active repo %q, got %+v", dir, tab)
	}
	if a.showHome {
		t.Fatal("expected showHome to be false after dropping folder")
	}
	retireTester(tt)
}

func TestSidebarRepoContextMenu(t *testing.T) {
	dir1 := filepath.Join(t.TempDir(), "repo-one")
	dir2 := filepath.Join(t.TempDir(), "repo-two")
	_ = os.MkdirAll(dir1, 0o755)
	_ = os.MkdirAll(dir2, 0o755)

	tab1 := newTab(1, dir1)
	tab1.loaded = true
	tab1.title = "repo-one"
	tab1.branch = "main"

	tab2 := newTab(2, dir2)
	tab2.loaded = true
	tab2.title = "repo-two"
	tab2.branch = "dev"

	a := &app{
		tabs:     []*repoTab{tab1, tab2},
		active:   0,
		dispatch: func(fn func()) {},
	}

	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	// 1. Right click on repo-two in sidebar
	if err := tt.RightClick("repo-two"); err != nil {
		t.Fatalf("RightClick failed: %v", err)
	}

	menu := tt.Menu()
	if len(menu) == 0 {
		t.Fatal("expected context menu for sidebar repo item, got none")
	}

	revealName := "在访达中显示"
	if runtime.GOOS == "windows" {
		revealName = "在资源管理器中显示"
	} else if runtime.GOOS != "darwin" {
		revealName = "在文件管理器中显示"
	}

	hasReveal, hasTerminal, hasCopy, hasClose := false, false, false, false
	for _, it := range menu {
		switch it {
		case revealName:
			hasReveal = true
		case "在终端中打开":
			hasTerminal = true
		case "复制路径":
			hasCopy = true
		case "关闭仓库":
			hasClose = true
		}
	}
	if !hasReveal || !hasTerminal || !hasCopy || !hasClose {
		t.Fatalf("missing menu items in %+v", menu)
	}

	// 2. Test 复制路径
	if err := tt.ChooseMenuItem("复制路径"); err != nil {
		t.Fatalf("ChooseMenuItem(复制路径) failed: %v", err)
	}
	tt.Frame()
	if tt.Clipboard() != dir2 {
		t.Fatalf("expected clipboard %q, got %q", dir2, tt.Clipboard())
	}

	// 3. Right click again and choose 关闭仓库
	if err := tt.RightClick("repo-two"); err != nil {
		t.Fatalf("RightClick failed: %v", err)
	}
	if err := tt.ChooseMenuItem("关闭仓库"); err != nil {
		t.Fatalf("ChooseMenuItem(关闭仓库) failed: %v", err)
	}
	tt.Frame()

	if len(a.tabs) != 1 || a.tabs[0].path != dir1 {
		t.Fatalf("expected only repo-one remaining, got tabs=%+v", a.tabs)
	}
	retireTester(tt)
}

func TestRecentRepoContextMenu(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-recent-project")
	_ = os.MkdirAll(dir, 0o755)

	a := &app{
		recent:   []string{dir},
		showHome: true,
		dispatch: func(fn func()) {},
	}

	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if err := tt.RightClick("my-recent-project"); err != nil {
		t.Fatalf("RightClick on recent repo failed: %v", err)
	}

	menu := tt.Menu()
	if len(menu) == 0 {
		t.Fatal("expected context menu for recent repo item, got none")
	}

	if err := tt.ChooseMenuItem("复制路径"); err != nil {
		t.Fatalf("ChooseMenuItem(复制路径) failed: %v", err)
	}
	tt.Frame()
	if tt.Clipboard() != dir {
		t.Fatalf("expected clipboard %q, got %q", dir, tt.Clipboard())
	}

	// Right click again and choose remove
	if err := tt.RightClick("my-recent-project"); err != nil {
		t.Fatalf("RightClick failed: %v", err)
	}
	if err := tt.ChooseMenuItem("从最近列表中移除"); err != nil {
		t.Fatalf("ChooseMenuItem failed: %v", err)
	}
	tt.Frame()
	if len(a.recent) != 0 {
		t.Fatalf("expected recent list to be empty, got %v", a.recent)
	}
	retireTester(tt)
}

func TestGlobalAlertBannerDismissAndCopy(t *testing.T) {
	tab := newTab(1, "/tmp/sample-repo")
	tab.loaded = true
	tab.branch = "main"

	errMsg := "Fetch 失败：无法解析远程主机 github.com"
	a := &app{
		tabs:       []*repoTab{tab},
		banner:     errMsg,
		bannerPath: tab.path,
		dispatch:   func(fn func()) {},
	}

	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if !tt.HasText(errMsg) {
		t.Fatalf("expected global alert banner with message %q, but texts are %q", errMsg, tt.Texts())
	}

	// Test copy button
	if err := tt.Click("复制错误信息"); err != nil {
		t.Fatalf("clicking banner-copy-err failed: %v", err)
	}
	if tt.Clipboard() != errMsg {
		t.Fatalf("expected clipboard %q, got %q", errMsg, tt.Clipboard())
	}

	// Test close button
	if err := tt.Click("关闭提示"); err != nil {
		t.Fatalf("clicking banner-close failed: %v", err)
	}
	tt.Frame()
	if a.banner != "" {
		t.Fatalf("expected banner cleared, got %q", a.banner)
	}
	if tt.HasText(errMsg) {
		t.Fatal("banner still visible after closing")
	}
	retireTester(tt)
}

func TestNotifySuccessDeliversToastAndNotice(t *testing.T) {
	tab := newTab(1, "/tmp/sample-repo")
	tab.loaded = true
	a := &app{
		tabs:     []*repoTab{tab},
		dispatch: func(fn func()) {},
	}

	a.notifySuccess(tab, "已切换到分支 dev")
	if tab.notice != "已切换到分支 dev" {
		t.Fatalf("expected tab.notice '已切换到分支 dev', got %q", tab.notice)
	}
	if a.pendingToast != "已切换到分支 dev" {
		t.Fatalf("expected pendingToast '已切换到分支 dev', got %q", a.pendingToast)
	}

	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if a.pendingToast != "" {
		t.Fatalf("expected pendingToast consumed, got %q", a.pendingToast)
	}
	retireTester(tt)
}

func initBareAndWorktree(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	_, _ = runGit(ctx, dir, "init", "-b", "main")
	_, _ = runGit(ctx, dir, "config", "user.name", "Tester")
	_, _ = runGit(ctx, dir, "config", "user.email", "tester@example.com")
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello"), 0o644)
	_, _ = runGit(ctx, dir, "add", ".")
	_, _ = runGit(ctx, dir, "commit", "-m", "init commit")
}
