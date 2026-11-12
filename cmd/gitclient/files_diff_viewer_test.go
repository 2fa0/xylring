package main

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	saveLobePreview(t, tt, "commit-details")

	// Click 收起信息 to collapse: the card shrinks away instead of vanishing.
	if err := tt.Click("收起信息"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.detailsOpen {
		t.Fatal("收起后状态未更新")
	}
	if !tab.detailsShown || !tt.HasText("完整提交信息") {
		t.Fatal("收起动画应保留内容直到缩回")
	}
	tt.Advance(2 * time.Second)
	tt.Frame()
	if tab.detailsShown || tt.HasText("完整提交信息") {
		t.Fatalf("收起动画结束后不应显示完整提交信息")
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
	// so the changed files pane is on the left edge, left of the sidebar boundary.
	sidebarEdge := a.repoSplit
	if fileRect.X >= sidebarEdge {
		t.Fatalf("in full-width mode, file list should be at the left edge below sidebar, got X=%v edge=%v", fileRect.X, sidebarEdge)
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
	// In docked mode, inspect-bottom is beside the sidebar.
	if fileRectDocked.X < a.repoSplit {
		t.Fatalf("in docked mode, file list should be beside sidebar (x >= %v), got X=%v", a.repoSplit, fileRectDocked.X)
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
	if fileRectRestored.X >= a.repoSplit {
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

	// Verify tab.fileSplit starts at the sidebar boundary.
	if tab.fileSplit != a.defaultFileSplit() {
		t.Fatalf("expected tab.fileSplit to default to the sidebar width %v, got %v", a.defaultFileSplit(), tab.fileSplit)
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

	if tab2.fileSplit != a.repoSplit {
		t.Fatalf("expected tab2.fileSplit to follow customized repoSplit %v, got %v", a.repoSplit, tab2.fileSplit)
	}
}

func TestDiffFindInView(t *testing.T) {
	tab := newTab(1, "/tmp/test-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "cmd/main.go"},
	}
	tab.fileSel = 0
	tab.diff = &editor.DiffState{
		Old: editor.NewBuffer("hello world\nline two\n"),
		New: editor.NewBuffer("hello xylring\nline two modified\n"),
	}
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// 1. Verify "在 Diff 中查找" button is visible and click it
	if !tt.HasText("在 Diff 中查找") {
		t.Fatalf("缺少在 Diff 中查找按钮: %v", tt.Texts())
	}
	if err := tt.Click("在 Diff 中查找"); err != nil {
		t.Fatalf("点击在 Diff 中查找失败: %v", err)
	}
	tt.Frame()

	// 2. Type query in find bar
	assertFindBarVisible(t, tt)
	tt.Type("xylring")
	tt.Advance(findQueryDelay)
	tt.Frame()
	if tab.diff.Cursor() != len("hello xylring") || !tt.HasText("1 of 1") {
		t.Fatalf("查找未选中匹配: cursor=%d, texts=%v", tab.diff.Cursor(), tt.Texts())
	}
	if path := os.Getenv("XYLRING_FIND_PNG"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.Click("关闭查找"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("查找内容") {
		t.Fatal("关闭后查找框仍然显示")
	}

	// 3. Test Cmd+F shortcut focuses/reopens find when inspecting diff
	tt.Key(commandModifier(), ui.KeyF)
	tt.Frame()
	assertFindBarVisible(t, tt)

	// 4. Test file tab find button
	fileTab := &fileDiffTab{
		id: 1,
		diff: &editor.DiffState{
			Old: editor.NewBuffer("tab old\n"),
			New: editor.NewBuffer("tab new\n"),
		},
	}
	tab.fileTabs = []*fileDiffTab{fileTab}
	tab.contentTab = 1
	tt.Frame()

	if !tt.HasText("在 Diff 中查找") {
		t.Fatalf("文件标签页缺少在 Diff 中查找按钮: %v", tt.Texts())
	}
	if err := tt.Click("在 Diff 中查找"); err != nil {
		t.Fatalf("点击标签页在 Diff 中查找失败: %v", err)
	}
	tt.Frame()
	assertFindBarVisible(t, tt)

	// Large-file fallback uses the same bar in its independent editors.
	fileTab.diff = nil
	fileTab.simple = &simpleDiff{
		old: editor.State{Buffer: editor.NewBuffer("old\n"), ReadOnly: true},
		new: editor.State{Buffer: editor.NewBuffer("new\n"), ReadOnly: true},
	}
	tt.Frame()
	if err := tt.Click("在 Diff 中查找"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	assertFindBarVisible(t, tt)

	// 5. Test full file tab find button
	fullTab := &fileDiffTab{
		id:       2,
		fullFile: true,
		fullContent: editor.State{
			Buffer: editor.NewBuffer("full file content\n"),
		},
	}
	tab.fileTabs = append(tab.fileTabs, fullTab)
	tab.contentTab = 2
	tt.Frame()

	if !tt.HasText("在文件中查找") {
		t.Fatalf("全量文件标签页缺少在文件中查找按钮: %v", tt.Texts())
	}
	if err := tt.Click("在文件中查找"); err != nil {
		t.Fatalf("点击在文件中查找失败: %v", err)
	}
	tt.Frame()
	assertFindBarVisible(t, tt)

	retireTester(tt)
}

func assertFindBarVisible(t *testing.T, tt *ui.Tester) {
	t.Helper()
	field, ok := tt.Find("查找内容")
	if !ok || field.W < 60 || field.H < 20 || !tt.Focused("查找内容") {
		t.Fatalf("查找输入框未完整显示或未获得焦点: %+v", field)
	}
	for _, name := range []string{"区分大小写", "全字匹配", "正则表达式", "在选区中查找", "上一个匹配", "下一个匹配", "关闭查找"} {
		r, ok := tt.Find(name)
		if !ok || r.W < 12 || r.H < 12 || r.X < field.X-8 || r.X+r.W > field.X+field.W+8 {
			t.Fatalf("查找控件 %s 被裁剪: %+v", name, r)
		}
	}
}

func TestDiffFindAboveCollapsedLines(t *testing.T) {
	for _, layout := range []editor.DiffLayout{editor.DiffSplit, editor.DiffUnified} {
		t.Run(fmt.Sprint(layout), func(t *testing.T) {
			common := strings.Repeat("unchanged line\n", 92)
			diff := &editor.DiffState{
				Old:           editor.NewBuffer(common + "old\n" + common),
				New:           editor.NewBuffer(common + "new\n" + common),
				Layout:        layout,
				RenderFindBar: renderFindBar,
			}
			tt := ui.NewTester(func(c *ui.Context) {
				editor.DiffView(c, diff).Fill()
			}, 1000, 500)
			defer retireTester(tt)
			tt.Frame()
			diff.Run(editor.Find)
			tt.Frame()
			if !tt.HasText("89 unmodified lines") {
				t.Fatal("缺少顶部折叠条")
			}
			field, ok := tt.Find("查找内容")
			if !ok {
				t.Fatal("缺少查找输入框")
			}
			img := tt.Image()
			x := int(field.X + field.W - 40)
			if img.RGBAAt(x, int(field.Y+6)) != img.RGBAAt(x, int(field.Y+field.H-6)) {
				t.Fatal("查找输入框上半部被折叠条绘制覆盖")
			}
			// The first collapsed-line separator crosses the top of the input.
			tt.ClickAt(field.X+20, field.Y+2)
			tt.Frame()
			if !tt.HasText("89 unmodified lines") {
				t.Fatal("点击查找框却展开了顶部折叠条")
			}
			if !tt.Focused("查找内容") {
				t.Fatal("折叠条遮挡查找框，点击被折叠条接收")
			}
			tt.Key(commandModifier(), ui.KeyA)
			tt.Type("new")
			tt.Advance(findQueryDelay)
			tt.Frame()
			if !tt.HasText("1 of 1") {
				t.Fatalf("查找未匹配新内容: %v", tt.Texts())
			}
			if path := os.Getenv("XYLRING_FIND_PNG"); path != "" {
				f, err := os.Create(fmt.Sprintf("%s-%d.png", path, layout))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, tt.Image()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestDiffFindTypingSettles(t *testing.T) {
	for _, layout := range []editor.DiffLayout{editor.DiffSplit, editor.DiffUnified} {
		t.Run(fmt.Sprint(layout), func(t *testing.T) {
			common := strings.Repeat("unchanged line\n", 300)
			diff := &editor.DiffState{
				Old:           editor.NewBuffer(common + "old\n"),
				New:           editor.NewBuffer(common + "if needle\nif next\n"),
				Layout:        layout,
				RenderFindBar: renderFindBar,
			}
			tt := ui.NewTester(func(c *ui.Context) {
				editor.DiffView(c, diff).Fill()
			}, 1100, 500)
			defer retireTester(tt)
			tt.Frame()
			diff.Run(editor.Find)
			tt.Frame()
			panel, _ := tt.Find("文件内查找")
			// The status row keeps its room with an empty query, so the panel
			// does not grow as the first character arrives.
			tt.Key(commandModifier(), ui.KeyA)
			tt.Key(0, ui.KeyBackspace)
			tt.Frame()
			if empty, _ := tt.Find("文件内查找"); empty != panel {
				t.Fatalf("清空后面板伸缩: %+v, 期望 %+v", empty, panel)
			}
			cursor, scroll := diff.Cursor(), diff.ScrollY()
			for _, char := range []string{"i", "f"} {
				tt.Type(char)
				tt.Advance(100 * time.Millisecond)
				if diff.Cursor() != cursor || diff.ScrollY() != scroll {
					t.Fatal("输入未结束时正文发生了定位跳动")
				}
				if typing, _ := tt.Find("文件内查找"); typing != panel {
					t.Fatalf("输入中面板伸缩: %+v, 期望 %+v", typing, panel)
				}
			}
			tt.Advance(findQueryDelay)
			if !tt.HasText("1 of 2") || diff.Cursor() != len(common+"if") {
				t.Fatalf("输入停顿后未定位匹配: cursor=%d texts=%v", diff.Cursor(), tt.Texts())
			}
			if after, _ := tt.Find("文件内查找"); after != panel {
				t.Fatalf("结果更新导致面板伸缩: before=%+v after=%+v", panel, after)
			}
			for range 3 {
				scroll := diff.ScrollY()
				tt.Frame()
				if tt.FramePending() || diff.ScrollY() != scroll || !tt.Focused("查找内容") {
					t.Fatal("匹配后视图没有稳定或输入框丢失焦点")
				}
			}
			// Enter searches the latest input without waiting for the delay.
			tt.Type(" needle")
			tt.Key(0, ui.KeyEnter)
			if !tt.HasText("1 of 1") || diff.Cursor() != len(common+"if needle") {
				t.Fatalf("回车没有立即查询最新输入: %v", tt.Texts())
			}
			// Clearing the query removes matches immediately.
			tt.Key(commandModifier(), ui.KeyA)
			tt.Key(0, ui.KeyBackspace)
			if tt.HasText("1 of 1") || tt.HasText("No results") {
				t.Fatal("清空后仍显示旧结果")
			}
		})
	}
}

func TestDiffFindBothSidesAcceptInput(t *testing.T) {
	var queries []*string
	diff := &editor.DiffState{
		Old: editor.NewBuffer("leftneedle\nold text\n"),
		New: editor.NewBuffer("rightneedle\nnew text\n"),
		RenderFindBar: func(c *ui.Context, anchor ui.Element, f *editor.FindBar) {
			queries = append(queries, f.Query())
			renderFindBar(c, anchor, f)
		},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		queries = nil
		editor.DiffView(c, diff).Fill()
	}, 1100, 500)
	defer retireTester(tt)
	tt.Frame()
	for _, side := range []string{"Old text", "New text"} {
		if err := tt.Click(side); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		diff.Run(editor.Find)
		tt.Frame()
	}
	if len(queries) != 2 {
		t.Fatalf("应同时显示左右查找框，实际 %d", len(queries))
	}
	leftQuery, rightQuery := queries[0], queries[1]
	left, ok := tt.Find("查找内容")
	if !ok {
		t.Fatal("缺少左侧查找框")
	}
	tt.ClickAt(left.X+30, left.Y+left.H/2)
	tt.Key(commandModifier(), ui.KeyA)
	tt.Type("leftneedle")
	tt.Advance(findQueryDelay)
	tt.Frame()
	if *leftQuery != "leftneedle" {
		t.Fatalf("左侧查找框无法独立输入：left=%q, right=%q", *leftQuery, *rightQuery)
	}
	// The right pane occupies the second half of the same split view.
	tt.ClickAt(left.X+550.5+30, left.Y+left.H/2)
	tt.Key(commandModifier(), ui.KeyA)
	tt.Type("rightneedle")
	tt.Advance(findQueryDelay)
	tt.Frame()
	if *rightQuery != "rightneedle" || *leftQuery != "leftneedle" {
		t.Fatalf("左右查找内容串扰：left=%q, right=%q", *leftQuery, *rightQuery)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if len(queries) != 1 || queries[0] != leftQuery {
		t.Fatal("关闭右侧查找框影响了左侧")
	}
	tt.ClickAt(left.X+30, left.Y+left.H/2)
	tt.Key(commandModifier(), ui.KeyA)
	tt.Type("old text")
	tt.Advance(findQueryDelay)
	tt.Frame()
	if *leftQuery != "old text" {
		t.Fatal("关闭右侧后左侧无法继续输入")
	}
}
