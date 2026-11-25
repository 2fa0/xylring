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

	// Copy now lives in the drag overlay, which shows only while dragging;
	// the empty state relies on the import button alone.
	if !tt.HasText("导入仓库") {
		t.Fatal("empty state missing import button")
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

// 看板这类没有仓库上下文的页面，报错必须说清是哪个仓库。
func TestGlobalAlertBannerNamesRepoWithoutTab(t *testing.T) {
	withTempBoardConfig(t)
	missing := "/tmp/gone/legacy-project"
	errMsg := "找不到这个仓库：" + missing
	a := &app{
		showBoard:  true,
		banner:     errMsg,
		bannerPath: missing,
		dispatch:   func(fn func()) {},
	}

	tt := ui.NewTester(a.view, 1200, 760)
	defer retireTester(tt)
	tt.Frame()

	if !tt.HasText(missing) {
		t.Fatalf("expected the failure to name its repository, texts are %q", tt.Texts())
	}
	if err := tt.Click("复制错误信息"); err != nil {
		t.Fatalf("clicking banner-copy-err failed: %v", err)
	}
	if tt.Clipboard() != errMsg {
		t.Fatalf("expected clipboard %q, got %q", errMsg, tt.Clipboard())
	}
}

// Git 自己报的错（如 fetch 失败）不带路径，横幅要补上位置。
func TestGlobalAlertBannerAddsMissingRepoLocation(t *testing.T) {
	withTempBoardConfig(t)
	dir := "/tmp/work/alpha"
	errMsg := "无法解析远程主机 github.com"
	a := &app{
		showBoard:  true,
		banner:     errMsg,
		bannerPath: dir,
		dispatch:   func(fn func()) {},
	}

	tt := ui.NewTester(a.view, 1200, 760)
	defer retireTester(tt)
	tt.Frame()

	if !tt.HasText(errMsg) || !tt.HasText(dir) {
		t.Fatalf("expected error and its repository, texts are %q", tt.Texts())
	}
	if err := tt.Click("复制错误信息"); err != nil {
		t.Fatalf("clicking banner-copy-err failed: %v", err)
	}
	if want := errMsg + "\n" + dir; tt.Clipboard() != want {
		t.Fatalf("expected clipboard %q, got %q", want, tt.Clipboard())
	}
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

func TestFolderImportModalInteraction(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	_ = os.MkdirAll(alpha, 0o755)
	_ = os.MkdirAll(beta, 0o755)
	initBareAndWorktree(t, alpha)
	initBareAndWorktree(t, beta)

	updates := make(chan func(), 64)
	a := &app{
		persist:      false,
		showHome:     true,
		dispatch:     func(fn func()) { updates <- fn },
		recentWrites: make(chan []string, 4),
	}

	tt := ui.NewTester(a.view, 1000, 700)
	defer retireTester(tt)

	a.openFolderImportRepos(root, []string{alpha, beta})
	tt.Frame()

	// Cancel keeps showHome intact and imports nothing.
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("missing cancel button: %v", err)
	}
	tt.Frame()
	if a.folderImport.open || !a.showHome || len(a.tabs) != 0 || len(a.found) != 0 {
		t.Fatalf("cancel modified state: open=%v showHome=%v tabs=%d found=%v", a.folderImport.open, a.showHome, len(a.tabs), a.found)
	}

	// Re-open, toggle Select All off, pick only beta, and import.
	a.openFolderImportRepos(root, []string{alpha, beta})
	tt.Frame()
	if err := tt.Click("全选"); err != nil {
		t.Fatalf("missing select-all checkbox: %v", err)
	}
	tt.Frame()
	if !tt.HasText("导入 (0)") {
		t.Fatalf("expected 导入 (0) after unchecking all, texts=%v", tt.Texts())
	}
	if err := tt.Click("beta"); err != nil {
		t.Fatalf("missing beta checkbox: %v", err)
	}
	tt.Frame()
	if !tt.HasText("导入 (1)") {
		t.Fatalf("expected 导入 (1) after selecting beta, texts=%v", tt.Texts())
	}
	if err := tt.Click("导入 (1)"); err != nil {
		t.Fatalf("failed to click import button: %v", err)
	}
	tt.Frame()

	if a.folderImport.open {
		t.Fatal("expected dialog closed after import")
	}
	if a.showHome {
		t.Fatal("expected showHome false after import")
	}
	if len(a.tabs) != 1 || a.tabs[0].path != beta {
		t.Fatalf("expected only beta opened, got tabs=%+v", a.tabs)
	}
	if len(a.found) != 0 {
		t.Fatalf("expected no sidebar multi-repo list when importing single selected repo, got found=%v", a.found)
	}
	for _, tab := range a.tabs {
		tab.cancel()
	}
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

func TestSidebarRepoColorTagsAndFilter(t *testing.T) {
	dir1 := filepath.Join(t.TempDir(), "repo-one")
	dir2 := filepath.Join(t.TempDir(), "repo-two")
	dir3 := filepath.Join(t.TempDir(), "repo-three")
	wtDir := filepath.Join(t.TempDir(), "repo-wt")
	for _, d := range []string{dir1, dir2, dir3, wtDir} {
		_ = os.MkdirAll(d, 0o755)
	}

	tab1 := newTab(1, dir1)
	tab1.loaded = true
	tab1.title = "repo-one"
	tab1.branch = "main"
	tab1.worktrees = []repoWorktree{{path: wtDir, branch: "feat-wt"}}

	tab2 := newTab(2, dir2)
	tab2.loaded = true
	tab2.title = "repo-two"
	tab2.branch = "dev"

	tab3 := newTab(3, dir3)
	tab3.loaded = true
	tab3.title = "repo-three"
	tab3.branch = "topic"

	a := &app{
		tabs:     []*repoTab{tab1, tab2, tab3},
		active:   0,
		dispatch: func(fn func()) {},
	}
	for _, tag := range repoColorTags {
		if tag.menuIcon == nil {
			t.Fatalf("expected non-nil menuIcon for color tag %q", tag.id)
		}
	}

	tt := ui.NewTester(a.view, 1200, 760)
	defer retireTester(tt)
	tt.Frame()

	if err := tt.RightClick("repo-two"); err != nil {
		t.Fatalf("RightClick failed: %v", err)
	}
	if err := tt.ChooseMenuItem("标签颜色", "蓝色"); err != nil {
		t.Fatal(err)
	}
	tt.CloseMenu()
	tt.Frame()
	if got := a.repoColors[dir2]; got != "blue" {
		t.Fatalf("expected repo-two color blue, got %q", got)
	}

	if err := tt.RightClick("repo-two"); err != nil {
		t.Fatalf("RightClick failed: %v", err)
	}
	if err := tt.ChooseMenuItem("标签颜色", "清除标签"); err != nil {
		t.Fatal(err)
	}
	tt.CloseMenu()
	tt.Frame()
	if _, ok := a.repoColors[dir2]; ok {
		t.Fatalf("expected repo-two color cleared, got %+v", a.repoColors)
	}

	// Worktree sub-row should not show 标签颜色
	if err := tt.RightClick("feat-wt"); err != nil {
		t.Fatalf("RightClick on worktree failed: %v", err)
	}
	for _, it := range tt.Menu() {
		if it == "标签颜色" {
			t.Fatalf("worktree row should not have 标签颜色 menu item, got %+v", tt.Menu())
		}
	}
	tt.CloseMenu()
	tt.Frame()

	// 2. Color filter bar & sort by color
	a.setRepoColor(dir1, "blue")
	a.setRepoColor(dir2, "red")
	tt.Frame()

	if !tt.HasText("全部") {
		t.Fatalf("expected color filter bar with 全部, texts=%v", tt.Texts())
	}
	if err := tt.Click("红色"); err != nil {
		t.Fatalf("Click(红色) filter failed: %v", err)
	}
	tt.Frame()
	if a.repoColorFilter != "red" {
		t.Fatalf("expected repoColorFilter=red, got %q", a.repoColorFilter)
	}
	if tt.HasText("repo-one") || !tt.HasText("repo-two") || tt.HasText("repo-three") {
		t.Fatalf("expected only red repo-two visible when filtered, texts=%v", tt.Texts())
	}

	if err := tt.Click("全部"); err != nil {
		t.Fatalf("Click(全部) filter failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("repo-one") || !tt.HasText("repo-two") || !tt.HasText("repo-three") {
		t.Fatalf("expected all repos visible after clearing filter, texts=%v", tt.Texts())
	}

	if err := tt.Click("按标签颜色排序"); err != nil {
		t.Fatalf("Click(按标签颜色排序) failed: %v", err)
	}
	tt.Frame()
	wantOrder := []string{dir2, dir1, dir3}
	if len(a.repoOrder) != len(wantOrder) || a.repoOrder[0] != wantOrder[0] || a.repoOrder[1] != wantOrder[1] || a.repoOrder[2] != wantOrder[2] {
		t.Fatalf("expected repoOrder %v, got %v", wantOrder, a.repoOrder)
	}
}

func TestRecentRepoColorTag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-recent-project")
	_ = os.MkdirAll(dir, 0o755)

	a := &app{
		showHome: true,
		recent:   []string{dir},
		dispatch: func(fn func()) {},
	}
	tt := ui.NewTester(a.view, 1000, 700)
	defer retireTester(tt)
	tt.Frame()

	if err := tt.RightClick("my-recent-project"); err != nil {
		t.Fatalf("RightClick on recent repo failed: %v", err)
	}
	if err := tt.ChooseMenuItem("标签颜色", "绿色"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.repoColors[dir]; got != "green" {
		t.Fatalf("expected recent repo color green, got %q", got)
	}
}

func TestRepoColorMenuKeyboard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo-keyboard")
	a := &app{showHome: true, recent: []string{path}, dispatch: func(fn func()) {}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetMenuPresentation(ui.PopoverMenus)
		a.view(c)
	}, 1000, 700)
	defer retireTester(tt)
	tt.Frame()

	if err := tt.RightClick("repo-keyboard"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyRight)
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if got := a.repoColors[path]; got != "orange" {
		t.Fatalf("expected keyboard to select orange, got %q", got)
	}
}
