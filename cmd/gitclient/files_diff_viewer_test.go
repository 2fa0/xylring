package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestFileFilterFlatAndTree(t *testing.T) {
	tab := newTab(1, "/tmp/test-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "cmd/gitclient/main.go"},
		{status: "M", path: "cmd/gitclient/compare.go"},
		{status: "A", path: "docs/readme.md"},
		{status: "M", path: "README.md"},
	}
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "测试内容"
	a := &app{tabs: []*repoTab{tab}, fileView: 0, dispatch: func(func()) {}}

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// Initial view has all files and filter input
	if !tt.HasText("main.go") || !tt.HasText("compare.go") || !tt.HasText("README.md") {
		t.Fatalf("初始列表缺少文件: %v", tt.Texts())
	}

	// Filter by "compare" in flat view
	tab.fileFilter = "compare"
	tt.Frame()
	if !tt.HasText("compare.go") || tt.HasText("README.md") {
		t.Fatalf("过滤后应只显示 compare.go 且不显示 README.md: %v", tt.Texts())
	}
	if !tt.HasText("1/4") {
		t.Fatalf("应显示过滤计数 1/4: %v", tt.Texts())
	}

	// Clear filter via clear button
	if err := tt.Click("清除过滤"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.fileFilter != "" || !tt.HasText("main.go") {
		t.Fatalf("清除过滤失败: filter=%q", tab.fileFilter)
	}

	// Test Esc key clears filter
	tab.fileFilter = "readme"
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tab.fileFilter != "" {
		t.Fatalf("Esc 应该清除过滤: filter=%q", tab.fileFilter)
	}

	// Switch to Tree view
	a.fileView = 1
	tab.fileFilter = "docs"
	tt.Frame()
	if !tt.HasText("readme.md") || tt.HasText("compare.go") {
		t.Fatalf("树状视图过滤 docs 应该只显示 readme.md: %v", tt.Texts())
	}

	// Clear filter in tree view
	tab.fileFilter = ""
	tt.Frame()
	if !tt.HasText("compare.go") {
		t.Fatalf("清除过滤后树状视图应恢复: %v", tt.Texts())
	}
}

func TestTreeViewExpandAndCollapseAll(t *testing.T) {
	tab := newTab(1, "/tmp/test-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "a/b/c/deep.go"},
		{status: "M", path: "x/y/z/other.go"},
	}
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "测试内容"
	a := &app{tabs: []*repoTab{tab}, fileView: 1, dispatch: func(func()) {}}

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// Initially all directories are expanded
	if !tt.HasText("全部收起") || !tt.HasText("全部展开") {
		t.Fatalf("树状视图缺少全部收起/展开按钮: %v", tt.Texts())
	}

	// Collapse all
	if err := tt.Click("全部收起"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.fileOutline.Open.Len() != 0 {
		t.Fatalf("全部收起后 Open 应为空，实际为 %d", tab.fileOutline.Open.Len())
	}

	// Expand all
	if err := tt.Click("全部展开"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.fileOutline.Open.Len() == 0 {
		t.Fatalf("全部展开后 Open 不应为空")
	}
}

func TestDiffLayoutAndIgnoreWhitespaceToggle(t *testing.T) {
	dir, git := initTestRepo(t)
	// Create initial file
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "初始提交")

	// Make whitespace-only changes
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("package main\n\nfunc main() {\n        println(\"hello\")   \n}\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "仅改动空白字符")

	ctx := context.Background()
	hash, err := resolveCommit(ctx, dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	parentHash, err := resolveCommit(ctx, dir, "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}

	// Test readDiffWithWhitespaceIgnored
	diffWs, note, err := readDiffWithWhitespaceIgnored(ctx, dir, hashHex(parentHash), hashHex(hash), "code.go", "code.go", false)
	if err != nil {
		t.Fatalf("readDiffWithWhitespaceIgnored error: %v", err)
	}
	if diffWs != nil || !strings.Contains(note, "空白字符改动已忽略") {
		t.Fatalf("仅空白改动时应提示已忽略，得到 diff=%v note=%q", diffWs, note)
	}

	// Now test UI controls for diff layout and whitespace
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 0)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// Check layout toggle buttons
	if !tt.HasText("并排") || !tt.HasText("内联") || !tt.HasText("忽略空白") {
		t.Fatalf("缺少排版或忽略空白按钮: %v", tt.Texts())
	}
	rLayout, okLayout := tt.Find("差异排版")
	rIgnore, okIgnore := tt.Find("忽略空白")
	if !okLayout || !okIgnore {
		t.Fatalf("未能找到差异排版或忽略空白控件")
	}
	if rLayout.H != rIgnore.H {
		t.Fatalf("差异排版高度(%v)与忽略空白高度(%v)不一致", rLayout.H, rIgnore.H)
	}
	if rLayout.Y != rIgnore.Y {
		t.Fatalf("差异排版Y坐标(%v)与忽略空白Y坐标(%v)不一致", rLayout.Y, rIgnore.Y)
	}

	// Toggle layout to Unified (内联)
	if err := tt.Click("内联"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.currentDiffLayout() != editor.DiffUnified {
		t.Fatalf("应切换为 DiffUnified")
	}

	// Toggle layout to Split (并排)
	if err := tt.Click("并排"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.currentDiffLayout() != editor.DiffSplit {
		t.Fatalf("应切换为 DiffSplit")
	}

	// Toggle ignore whitespace
	if err := tt.Click("忽略空白"); err != nil {
		t.Fatal(err)
	}
	if !a.diffIgnoreWhitespace {
		t.Fatal("diffIgnoreWhitespace 应为 true")
	}
	waitUpdates(t, updates, func() bool { return !tab.diffLoading })
	tt.Frame()
	if !tt.HasText("空白字符改动已忽略") {
		t.Fatalf("忽略空白后应提示无改动: %v", tt.Texts())
	}
}

func TestCommitDetailsInlineCard(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o600)
	git("add", ".")
	git("commit", "--author=TestAuthor <author@example.com>", "-m", "第一行说明", "-m", "详细描述第二行\n详细描述第三行")

	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 0)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	if !tt.HasText("完整信息") {
		t.Fatalf("未找到完整信息按钮: %v", tt.Texts())
	}

	// Click 完整信息 to open inline
	if err := tt.Click("完整信息"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.detailsLoading })
	tt.Frame()

	if !tt.HasText("完整提交信息") || !tt.HasText("复制完整信息") || !tt.HasText("收起信息") {
		t.Fatalf("缺少内联完整信息内容: %v", tt.Texts())
	}

	// Click 收起信息 to collapse
	if err := tt.Click("收起信息"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.detailsOpen || tt.HasText("完整提交信息") {
		t.Fatalf("收起信息后不应显示完整提交信息")
	}
}

func TestWhitespaceDiffDistinguishesBinaryHeadersFromText(t *testing.T) {
	for _, body := range []string{"Binary files are handled here\n", "GIT binary patch\n", "binary\x00content"} {
		t.Run(body, func(t *testing.T) {
			dir, git := initTestRepo(t)
			path := filepath.Join(dir, "a.txt")
			os.WriteFile(path, []byte("one\n"), 0o600)
			git("add", ".")
			git("commit", "-m", "base")
			before := mustResolve(t, dir, "HEAD")
			os.WriteFile(path, []byte(body), 0o600)
			git("commit", "-am", "change")
			after := mustResolve(t, dir, "HEAD")
			diff, note, err := readDiffWithWhitespaceIgnored(context.Background(), dir, hashHex(before), hashHex(after), "a.txt", "a.txt", false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.ContainsRune(body, 0) {
				if diff != nil || !strings.Contains(note, "二进制") {
					t.Fatalf("binary not recognized: diff=%v note=%q", diff != nil, note)
				}
			} else if diff == nil || note != "" || !strings.Contains(diff.New.String(), strings.TrimSpace(body)) {
				t.Fatalf("text diff suppressed: diff=%v note=%q", diff != nil, note)
			}
		})
	}
}

func readDiffWithWhitespaceIgnored(ctx context.Context, dir, fromRev, toRev, oldPath, newPath string, worktree bool) (*editor.DiffState, string, error) {
	f := fileChange{path: newPath, oldPath: oldPath}
	if oldPath == "" {
		f.status = "A"
	}
	if newPath == "" {
		f.path, f.status = oldPath, "D"
	}
	s, err := readFileDiffSource(ctx, dir, fromRev, toRev, f, worktree)
	if err != nil {
		return nil, "", err
	}
	d, _, _, note, err := s.prepare(ctx, f.path, true)
	return d, note, err
}

func TestSidebarInspectAndFullWidthDiff(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "画出提交图谱", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "main.go"},
		{status: "A", path: "compare.go"},
	}
	tab.fileSel = 0
	tab.diffNote = "测试差异内容"
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}

	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// 1. Sidebar contains the files and commit subject
	if !tt.HasText("main.go") || !tt.HasText("compare.go") || !tt.HasText("画出提交图谱") {
		t.Fatalf("侧边栏应展示改动文件与提交信息: %v", tt.Texts())
	}

	// 2. Diff viewer displays diff note and header controls
	if !tt.HasText("测试差异内容") || !tt.HasText("放大阅读") || !tt.HasText("关闭详情") {
		t.Fatalf("底栏应为全宽 Diff 并带有控制按钮: %v", tt.Texts())
	}

	// 3. Click second file in sidebar
	if err := tt.Click("compare.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.fileSel != 1 {
		t.Fatalf("点击侧栏文件应更新 fileSel，实际为: %d", tab.fileSel)
	}
}

func TestBottomFullWidthToggle(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "画出提交图谱", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "main.go"},
	}
	tab.fileSel = 0
	tab.diffNote = "测试差异内容"
	a := newApp()
	a.tabs = []*repoTab{tab}
	a.dispatch = func(func()) {}

	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// 1. By default, bottomFullWidth is true
	if !a.bottomFullWidth {
		t.Fatalf("newApp should have bottomFullWidth enabled by default")
	}
	fileRect, ok := tt.Find("main.go")
	if !ok {
		t.Fatalf("main.go not found: %v", tt.Texts())
	}
	// In full-width mode, inspect-bottom spans the whole window (starts at x=0),
	// so the changed files pane is on the left edge (x < 320).
	if fileRect.X >= 320 {
		t.Fatalf("in full-width mode, file list should be at the left edge below sidebar, got X=%v", fileRect.X)
	}

	// 2. Click toggle button to switch to beside-sidebar mode
	if err := tt.Click("切换底栏全宽"); err != nil {
		t.Fatalf("failed to click toggle-bottom-width: %v", err)
	}
	tt.Frame()
	if a.bottomFullWidth {
		t.Fatalf("bottomFullWidth should be false after toggle")
	}

	fileRectDocked, ok := tt.Find("main.go")
	if !ok {
		t.Fatalf("main.go not found after docking: %v", tt.Texts())
	}
	// In docked mode, inspect-bottom is beside the sidebar (x >= 320).
	if fileRectDocked.X < 320 {
		t.Fatalf("in docked mode, file list should be beside sidebar (x >= 320), got X=%v", fileRectDocked.X)
	}

	// 3. Click toggle button again to restore full-width
	if err := tt.Click("切换底栏全宽"); err != nil {
		t.Fatalf("failed to click toggle-bottom-width again: %v", err)
	}
	tt.Frame()
	if !a.bottomFullWidth {
		t.Fatalf("bottomFullWidth should be restored to true")
	}
	fileRectRestored, ok := tt.Find("main.go")
	if !ok {
		t.Fatalf("main.go not found after restoring full width: %v", tt.Texts())
	}
	if fileRectRestored.X >= 320 {
		t.Fatalf("after restoring, file list should be at the left edge, got X=%v", fileRectRestored.X)
	}
}

func TestInspectFilesSplitDefaultMatchesSidebar(t *testing.T) {
	tab := newTab(1, "/tmp/test-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.worktree = true
	tab.compareFiles = []fileChange{
		{status: "M", path: "main.go"},
	}
	tab.fileSel = 0
	tab.diffNote = "测试差异内容"
	a := newApp()
	a.tabs = []*repoTab{tab}
	a.dispatch = func(func()) {}

	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// Verify tab.fileSplit is initialized to sidebar width (320)
	if tab.fileSplit != 320 {
		t.Fatalf("expected tab.fileSplit to default to sidebar width 320, got %v", tab.fileSplit)
	}

	// Verify that if a customized repoSplit is provided on a new tab, it follows repoSplit
	a.repoSplit = 280
	tab2 := newTab(2, "/tmp/test-repo-2")
	tab2.loaded = true
	tab2.inspecting = hashN(1)
	tab2.worktree = true
	tab2.compareFiles = []fileChange{{status: "M", path: "other.go"}}
	a.tabs = append(a.tabs, tab2)
	a.active = 1
	tt.Frame()

	if tab2.fileSplit != 280 {
		t.Fatalf("expected tab2.fileSplit to follow customized repoSplit 280, got %v", tab2.fileSplit)
	}
}
