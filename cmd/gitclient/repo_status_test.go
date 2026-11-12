package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func activateWorktreeInspect(tab *repoTab) {
	tab.worktree = true
	tab.inspecting = zeroHash
	tab.comparison = nil
	tab.compareFiles = tab.worktreeFiles
	tab.worktreeCount = len(tab.worktreeFiles)
	tab.selected = 0
	tab.selection.Clear()
	tab.selection.Add(worktreeHash)
	tab.fileSplit = 50
	tab.detailSplit = 45
}

func TestSidebarBranchDisplay(t *testing.T) {
	cases := []struct {
		branch string
		dirty  bool
		want   string
	}{
		{"main", false, "main"},
		{"main", true, "main*"},
		{"feature/login", false, "feature/login"},
		{"feature/login", true, "feature/login*"},
		{"分离头指针 abc1234", false, "(abc1234)"},
		{"分离头指针 abc1234", true, "(abc1234)*"},
		{"尚无提交", false, "(尚无提交)"},
		{"尚无提交", true, "(尚无提交)*"},
		{"", false, ""},
		{"", true, "*"},
	}

	for _, tc := range cases {
		got := sidebarBranchDisplay(tc.branch, tc.dirty)
		if got != tc.want {
			t.Errorf("sidebarBranchDisplay(%q, %v) = %q, want %q", tc.branch, tc.dirty, got, tc.want)
		}
	}
}

func TestQueryRepoSummary(t *testing.T) {
	root := t.TempDir()
	initGitDir(t, root)
	git := func(args ...string) {
		t.Helper()
		if _, err := runGit(context.Background(), root, args...); err != nil {
			t.Fatal(err)
		}
	}
	git("config", "user.name", "Tester")
	git("config", "user.email", "tester@example.com")
	git("config", "commit.gpgsign", "false")
	git("commit", "--allow-empty", "-m", "initial")

	// Initially clean
	s := queryRepoSummary(context.Background(), root)
	if s.branch != "main" {
		t.Fatalf("expected branch main, got %q", s.branch)
	}
	if s.dirty {
		t.Fatalf("expected clean repo, got dirty")
	}

	// Add untracked file
	untracked := filepath.Join(root, "untracked.txt")
	if err := os.WriteFile(untracked, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	s = queryRepoSummary(context.Background(), root)
	if !s.dirty {
		t.Fatalf("expected dirty repo with untracked file, got clean")
	}

	// Stage the file
	git("add", "untracked.txt")
	s = queryRepoSummary(context.Background(), root)
	if !s.dirty {
		t.Fatalf("expected dirty repo with staged file, got clean")
	}

	// Commit the file -> clean again
	git("commit", "-m", "add untracked.txt")
	s = queryRepoSummary(context.Background(), root)
	if s.dirty {
		t.Fatalf("expected clean repo after commit, got dirty")
	}

	// Modify tracked file
	if err := os.WriteFile(untracked, []byte("world"), 0o600); err != nil {
		t.Fatal(err)
	}
	s = queryRepoSummary(context.Background(), root)
	if !s.dirty {
		t.Fatalf("expected dirty repo with modified file, got clean")
	}
}

func TestRepoSidebarShowsBranchAndDirty(t *testing.T) {
	tab1 := newTab(1, "/tmp/work/alpha")
	tab1.loaded = true
	tab1.branch = "main"
	tab1.worktreeCount = 0

	tab2 := newTab(2, "/tmp/work/beta")
	tab2.loaded = true
	tab2.branch = "feature-ui"
	tab2.worktreeCount = 2

	a := &app{
		tabs:      []*repoTab{tab1, tab2},
		found:     []string{"/tmp/work/alpha", "/tmp/work/beta"},
		foundFrom: "/tmp/work",
		repoSummaries: map[string]repoSummary{
			"/tmp/work/alpha": {branch: "main", dirty: false},
			"/tmp/work/beta":  {branch: "feature-ui", dirty: true},
		},
	}

	tt := ui.NewTester(a.view, 1100, 900)
	tt.Frame()

	// Both repo labels should be present
	if !tt.HasText("alpha") || !tt.HasText("beta") {
		t.Fatalf("缺少仓库名称：%q", tt.Texts())
	}

	// alpha is clean -> shows "main"
	if !tt.HasText("main") {
		t.Fatalf("缺少分支 main：%q", tt.Texts())
	}
	// beta is dirty -> shows "feature-ui*"
	if !tt.HasText("feature-ui*") {
		t.Fatalf("缺少未提交标记 feature-ui*：%q", tt.Texts())
	}

	// Now beta commits changes -> clean
	tab2.worktreeCount = 0
	tt.Frame()
	if !tt.HasText("feature-ui") {
		t.Fatalf("提交后应展示 feature-ui：%q", tt.Texts())
	}

	// alpha gets dirty
	tab1.worktreeCount = 1
	tt.Frame()
	if !tt.HasText("main*") {
		t.Fatalf("alpha 变脏后应展示 main*：%q", tt.Texts())
	}
}

func TestRepoSidebarFetchesFoundRepoSummary(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	initGitDir(t, alpha)
	initGitDir(t, beta)

	git := func(dir string, args ...string) {
		t.Helper()
		if _, err := runGit(context.Background(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	git(alpha, "config", "user.name", "Tester")
	git(alpha, "config", "user.email", "tester@example.com")
	git(alpha, "config", "commit.gpgsign", "false")
	git(alpha, "commit", "--allow-empty", "-m", "init alpha")

	git(beta, "config", "user.name", "Tester")
	git(beta, "config", "user.email", "tester@example.com")
	git(beta, "config", "commit.gpgsign", "false")
	git(beta, "checkout", "-b", "dev")
	git(beta, "commit", "--allow-empty", "-m", "init beta")
	// Make beta dirty with untracked file
	if err := os.WriteFile(filepath.Join(beta, "new.txt"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}

	tab1 := newTab(1, alpha)
	tab1.loaded = true
	tab1.branch = "main"

	updates := make(chan func(), 16)
	a := &app{
		tabs:      []*repoTab{tab1},
		found:     []string{alpha, beta},
		foundFrom: root,
		dispatch:  func(fn func()) { updates <- fn },
	}

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// Drain any update posted by background summary fetch
	waitUpdates(t, updates, func() bool {
		s, ok := a.repoSummaries[beta]
		return ok && s.branch == "dev" && s.dirty
	})

	tt.Frame()
	if !tt.HasText("dev*") {
		t.Fatalf("未打开的子仓库 beta 应异步展示 dev*：%q", tt.Texts())
	}
}

func TestSidebarWorktreeShowsActiveRepoFiles(t *testing.T) {
	tab1 := newTab(1, "/tmp/work/alpha")
	tab1.loaded = true
	tab1.branch = "main"
	tab1.worktreeFiles = []fileChange{
		{path: ".DS_Store", status: "M"},
		{path: "src/main.go", status: "A", added: 10, deleted: 0, hasStat: true},
	}
	tab1.worktreeCount = 2

	tab2 := newTab(2, "/tmp/work/beta")
	tab2.loaded = true
	tab2.branch = "dev"
	tab2.worktreeFiles = nil
	tab2.worktreeCount = 0

	a := &app{
		tabs:      []*repoTab{tab1, tab2},
		active:    0,
		found:     []string{"/tmp/work/alpha", "/tmp/work/beta"},
		foundFrom: "/tmp/work",
	}

	activateWorktreeInspect(tab1)
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	if !tt.HasText("全部暂存") {
		t.Fatalf("未提交场景应显示文件列表：%q", tt.Texts())
	}
	if !tt.HasText(".DS_Store") || !tt.HasText("main.go") {
		t.Fatalf("侧栏应展示改动的文件：%q", tt.Texts())
	}
	if !tt.HasText("src") {
		t.Fatalf("侧栏应展示子目录路径：%q", tt.Texts())
	}

	a.active = 1
	tt.Frame()
	if tt.HasText(".DS_Store") || tt.HasText("main.go") {
		t.Fatalf("切换到干净仓库后不应再显示上一仓库的改动文件：%q", tt.Texts())
	}
}

func TestSidebarWorktreeClickOpensDiffTab(t *testing.T) {
	tab := newTab(1, "/tmp/work/alpha")
	tab.loaded = true
	tab.branch = "main"
	tab.worktreeFiles = []fileChange{
		{path: ".DS_Store", status: "M"},
		{path: "README.md", status: "A"},
	}
	tab.worktreeCount = 2
	tab.commits = []commit{{hash: hashN(1), subject: "init"}}

	updates := make(chan func(), 16)
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/alpha", "/tmp/work/beta"},
		foundFrom: "/tmp/work",
		dispatch:  func(fn func()) { updates <- fn },
	}

	activateWorktreeInspect(tab)
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	if err := tt.Click("README.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if !tab.worktree || tab.fileSel != 1 {
		t.Fatalf("单击未提交文件后应选中该文件: worktree=%v sel=%d", tab.worktree, tab.fileSel)
	}

	a.openWorktreeFileTab(tab, tab.worktreeFiles[1])
	waitUpdates(t, updates, func() bool {
		return len(tab.fileTabs) == 1 && tab.contentTab == 1
	})
	tt.Frame()

	if len(tab.fileTabs) != 1 || tab.contentTab != 1 {
		t.Fatalf("点击侧边栏工作区文件后未打开文件对比标签页: tabs=%d contentTab=%d", len(tab.fileTabs), tab.contentTab)
	}
	if tab.fileTabs[0].file.path != "README.md" {
		t.Fatalf("标签页文件路径应为 README.md，实际为 %s", tab.fileTabs[0].file.path)
	}
	if !tt.HasText("README.md") {
		t.Fatalf("主界面应展示打开的文件标签：%q", tt.Texts())
	}
}

func TestSidebarWorktreeTreeToggle(t *testing.T) {
	tab := newTab(1, "/tmp/work/alpha")
	tab.loaded = true
	tab.branch = "main"
	tab.worktreeFiles = []fileChange{
		{path: "zaec-image/Dockerfile.jdk8-2", status: "A", added: 4, hasStat: true},
		{path: "zaec-image/build_jdk8-2.sh", status: "A", added: 9, hasStat: true},
		{path: "zaec-python-sdk", status: "M"},
		{path: "zaec-springboot-demo/pom.xml", status: "M", added: 1, deleted: 1, hasStat: true},
	}
	tab.worktreeCount = 4
	tab.commits = []commit{{hash: hashN(1), subject: "init"}}

	updates := make(chan func(), 16)
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/alpha"},
		foundFrom: "/tmp/work",
		dispatch:  func(fn func()) { updates <- fn },
	}

	activateWorktreeInspect(tab)
	tt := ui.NewTester(a.view, 1100, 900)
	tt.Frame()

	if !tt.HasText("全部暂存") || !tt.HasText("列表") || !tt.HasText("树") {
		t.Fatalf("未提交场景缺少文件视图切换：%q", tt.Texts())
	}
	// Initially in list mode, dir is shown under file
	if !tt.HasText("zaec-image") || !tt.HasText("Dockerfile.jdk8-2") {
		t.Fatalf("列表模式缺少文件信息：%q", tt.Texts())
	}

	// Switch to tree mode
	if err := tt.Click("树"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.fileView != 1 {
		t.Fatalf("点击树后 fileView 应为 1，实际为 %d", a.fileView)
	}

	// In tree mode, directory names and file names should be visible
	for _, text := range []string{"zaec-image", "Dockerfile.jdk8-2", "build_jdk8-2.sh", "zaec-python-sdk", "zaec-springboot-demo", "pom.xml", "+4", "+9"} {
		if !tt.HasText(text) {
			t.Fatalf("树状模式缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}

	// Verify directory node is open by default
	if !tab.fileOutline.Open.Has("zaec-image/") {
		t.Fatalf("zaec-image/ 应该默认展开")
	}
	initialRows := tab.fileOutline.Rows()
	// Collapse directory
	tab.fileOutline.Open.Remove("zaec-image/")
	tt.Frame()
	if tt.HasText("Dockerfile.jdk8-2") {
		t.Fatalf("折叠 zaec-image 后不应显示内部文件")
	}
	if tab.fileOutline.Rows() >= initialRows {
		t.Fatalf("折叠后行数应减少: was %d, now %d", initialRows, tab.fileOutline.Rows())
	}
	// Re-expand directory
	tab.fileOutline.Open.Add("zaec-image/")
	tt.Frame()
	if !tt.HasText("Dockerfile.jdk8-2") {
		t.Fatalf("重新展开后应显示内部文件")
	}

	// Click a file in tree mode
	if err := tt.Click("Dockerfile.jdk8-2"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if tab.fileSel != 0 {
		t.Fatalf("选中文件索引应为 0，实际为 %d", tab.fileSel)
	}
	if !tab.worktree {
		t.Fatalf("点击后应激活工作区视图")
	}

	a.openWorktreeFileTab(tab, tab.worktreeFiles[0])
	waitUpdates(t, updates, func() bool {
		return len(tab.fileTabs) == 1 && tab.contentTab == 1
	})
	tt.Frame()

	if len(tab.fileTabs) != 1 || tab.fileTabs[0].file.path != "zaec-image/Dockerfile.jdk8-2" {
		t.Fatalf("打开标签页异常：%+v", tab.fileTabs)
	}

	tab.contentTab = 0
	tt.Frame()
	// Switch back to list mode
	if err := tt.Click("列表"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.fileView != 0 {
		t.Fatalf("点击列表后 fileView 应为 0，实际为 %d", a.fileView)
	}
	// Selection should be preserved
	if tab.fileSel != 0 {
		t.Fatalf("切回列表后工作区选中丢失: %d", tab.fileSel)
	}
}

func TestSidebarWorktreeFeatures(t *testing.T) {
	tab := newTab(1, "/tmp/work/alpha")
	tab.loaded = true
	tab.branch = "main"
	tab.worktreeFiles = []fileChange{
		{path: "README.md", status: "M", added: 8, deleted: 5, hasStat: true},
		{path: "pkg/core.go", status: "M", added: 20, deleted: 2, hasStat: true},
		{path: "pkg/core_test.go", status: "A", added: 30, hasStat: true},
	}
	tab.worktreeCount = 3
	tab.commits = []commit{{hash: hashN(1), subject: "init"}}
	tab.head = hashN(1)

	updates := make(chan func(), 16)
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/alpha"},
		foundFrom: "/tmp/work",
		dispatch:  func(fn func()) { updates <- fn },
	}

	activateWorktreeInspect(tab)
	tt := ui.NewTester(a.view, 1100, 900)
	tt.Frame()

	// 1. Root directory placeholder
	if !tt.HasText("仓库根目录") {
		t.Fatalf("根目录文件应展示'仓库根目录'，当前文本：%q", tt.Texts())
	}

	// 2. Filter input is present
	if !tt.HasText("过滤文件") {
		t.Fatalf("缺少文件过滤输入框：%q", tt.Texts())
	}

	// 3. Filter test
	tab.fileFilter = "core"
	tt.Frame()
	if !tt.HasText("2/3") || !tt.HasText("清除过滤") {
		t.Fatalf("过滤后未展示匹配统计或清除按钮：%q", tt.Texts())
	}
	if tt.HasText("README.md") {
		t.Fatalf("过滤 core 后不应再展示 README.md：%q", tt.Texts())
	}
	if !tt.HasText("core.go") {
		t.Fatalf("过滤 core 后应展示 core.go：%q", tt.Texts())
	}

	// 4. Clear filter
	if err := tt.Click("清除过滤"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.fileFilter != "" {
		t.Fatalf("点击清除过滤后 fileFilter 应为空，实际为 %q", tab.fileFilter)
	}
	if !tt.HasText("README.md") {
		t.Fatalf("清除过滤后应恢复 README.md：%q", tt.Texts())
	}

	// 5. Tree expand/collapse all buttons
	if err := tt.Click("树"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("全部收起") || !tt.HasText("全部展开") {
		t.Fatalf("树状模式应有全部收起和全部展开按钮：%q", tt.Texts())
	}
	if err := tt.Click("全部收起"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.fileOutline.Open.Has("pkg/") {
		t.Fatalf("全部收起后 pkg/ 目录不应处于展开状态")
	}
	if err := tt.Click("全部展开"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tab.fileOutline.Open.Has("pkg/") {
		t.Fatalf("全部展开后 pkg/ 目录应处于展开状态")
	}
}
