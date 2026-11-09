package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParsePorcelainZ(t *testing.T) {
	files := parsePorcelainZ([]byte(" M file.txt\x00?? new.txt\x00R  renamed.txt\x00old.txt\x00"))
	if len(files) != 3 {
		t.Fatalf("files %+v", files)
	}
	if files[0].path != "file.txt" || files[0].extra != "未暂存" || files[0].status != "M" {
		t.Fatalf("modified %+v", files[0])
	}
	if files[1].path != "new.txt" || files[1].extra != "未跟踪" || files[1].status != "A" {
		t.Fatalf("untracked %+v", files[1])
	}
	if files[2].oldPath != "old.txt" || files[2].path != "renamed.txt" || files[2].extra != "重命名" {
		t.Fatalf("rename %+v", files[2])
	}
}

func TestWorktreeBadges(t *testing.T) {
	got := worktreeBadges([]fileChange{
		{extra: "未暂存"},
		{extra: "未跟踪"},
		{extra: "已暂存"},
		{extra: "已暂存并继续改"},
		{extra: "冲突"},
	})
	want := []string{"冲突 1", "已暂存 2", "未暂存 2", "未跟踪 1"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWorktreeRowOffsetsCommitIndex(t *testing.T) {
	tab := newTab(1, "/tmp/worktree-offset")
	tab.commits = []commit{{hash: hashN(1), subject: "head"}}
	tab.head = tab.commits[0].hash
	if tab.rows() != 1 || tab.rowData(0).hash != hashN(1) {
		t.Fatal("干净时不应插入工作区行")
	}
	tab.worktreeCount = 1
	tab.worktreeFiles = []fileChange{{path: "a.txt", extra: "未暂存"}}
	if tab.rows() != 2 || tab.rowData(0).hash != worktreeHash || tab.rowData(1).hash != hashN(1) {
		t.Fatalf("rows=%d first=%x", tab.rows(), tab.rowData(0).hash)
	}
	if tab.rowIndex(tab.head) != 1 || tab.rowIndex(worktreeHash) != 0 {
		t.Fatalf("rowIndex head=%d worktree=%d", tab.rowIndex(tab.head), tab.rowIndex(worktreeHash))
	}
	tab.query.term = "x"
	if tab.hasWorktreeRow() || tab.rows() != 1 {
		t.Fatal("搜索时仍显示工作区行")
	}
}

func TestWorktreeShowsUncommittedAndKeepsGraph(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o600)
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading && tab.worktreeCount > 0 })
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if tt.HasText("工作区 · 2 个文件") {
		t.Fatalf("旧的工作区按钮还在：%q", tt.Texts())
	}
	for _, text := range []string{"未提交改动", "未暂存 1", "未跟踪 1", "图谱", "base"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tab.rows() != 2 || tab.rowData(0).hash != worktreeHash {
		t.Fatalf("工作区不是图谱首行 rows=%d", tab.rows())
	}
	if err := tt.Click("未提交改动"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return tab.worktree && !tab.compareLoading && !tab.diffLoading })
	tt.Frame()
	for _, text := range []string{"未提交改动", "相对 HEAD", "提交改动", "a.txt", "b.txt", "+1", "-1", "-0", "图谱", "base", "并排", "内联"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("工作区代码") {
		t.Fatalf("未提交场景不应再占用侧栏工作区列表：%q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tab.worktree || tt.HasText("并排") {
		t.Fatalf("Esc 没有离开工作区：%q", tt.Texts())
	}
	if !tt.HasText("未提交改动") {
		t.Fatalf("离开后工作区行消失了：%q", tt.Texts())
	}
	if err := tt.Click("base"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.worktree || tab.inspecting == zeroHash {
		t.Fatalf("点提交没有打开详情 worktree=%t inspecting=%x", tab.worktree, tab.inspecting)
	}
}

func TestWorktreeRowConnectsToHead(t *testing.T) {
	tab := newTab(1, "/tmp/worktree-graph")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2)}, subject: "head", author: "A", when: 1700000000},
		{hash: hashN(2), subject: "base", author: "A", when: 1700000000},
	}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.head = tab.commits[0].hash
	tab.worktreeCount = 1
	tab.worktreeFiles = []fileChange{{path: "a.txt", extra: "未暂存", status: "M"}}
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 900, 500)
	tt.Frame()
	header, ok := tt.Find("图谱")
	if !ok {
		t.Fatal("图谱列没有显示")
	}
	row, ok := tt.Find("未提交改动")
	if !ok {
		t.Fatal("未提交改动没有显示")
	}
	next, ok := tt.Find("head")
	if !ok {
		t.Fatal("HEAD 没有显示")
	}
	tab.syncWorktreeCommit()
	img := tt.Image()
	boundary := int((row.Y + row.H/2 + next.Y + next.H/2) / 2)
	x := header.X + laneGap/2 + float32(tab.worktreeRow.nodeCol)*laneGap
	ink := false
	for y := boundary - 1; y <= boundary; y++ {
		for px := int(x) - 2; px <= int(x)+2; px++ {
			r, g, b, _ := img.At(px, y).RGBA()
			if max(r, g, b)-min(r, g, b) > 0x1800 {
				ink = true
			}
		}
	}
	if !ink {
		t.Fatalf("工作区节点没有接到 HEAD，boundary=%d x=%.1f", boundary, x)
	}
}

func TestCountTextLines(t *testing.T) {
	if n := countTextLines(nil); n != 0 {
		t.Fatalf("nil %d", n)
	}
	if n := countTextLines([]byte("a\n")); n != 1 {
		t.Fatalf("one line %d", n)
	}
	if n := countTextLines([]byte("a\nb")); n != 2 {
		t.Fatalf("no trailing newline %d", n)
	}
}

func TestWorktreeStatusLineStats(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2}, 0o600)
	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]fileChange{}
	for _, f := range files {
		got[f.path] = f
	}
	if !got["a.txt"].hasStat || got["a.txt"].added != 1 || got["a.txt"].deleted != 1 {
		t.Fatalf("modified %+v", got["a.txt"])
	}
	if !got["b.txt"].hasStat || got["b.txt"].binary || got["b.txt"].added != 1 || got["b.txt"].deleted != 0 {
		t.Fatalf("untracked %+v", got["b.txt"])
	}
	if !got["bin.dat"].hasStat || !got["bin.dat"].binary {
		t.Fatalf("binary %+v", got["bin.dat"])
	}
}

func TestWorktreeStatusCachedWithoutHead(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o600)
	git("add", "a.txt")
	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].path != "a.txt" || !files[0].hasStat || files[0].added != 1 || files[0].deleted != 0 {
		t.Fatalf("staged without HEAD %+v", files)
	}
}

func TestWorktreeRenameUsesGitPathOrder(t *testing.T) {
	dir, git := initTestRepo(t)
	oldPath, newPath := "旧名 file.txt", "新名 file.txt"
	os.WriteFile(filepath.Join(dir, oldPath), []byte("one\ntwo\nthree\nfour\nfive\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")
	head := mustResolve(t, dir, "HEAD")
	git("mv", oldPath, newPath)
	os.WriteFile(filepath.Join(dir, newPath), []byte("one\ntwo\nthree\nfour\nchanged\n"), 0o600)
	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].path != newPath || files[0].oldPath != oldPath || files[0].status != "R" {
		t.Fatalf("rename paths: %+v", files)
	}
	if !files[0].hasStat || files[0].added != 1 || files[0].deleted != 1 {
		t.Fatalf("rename stats: %+v", files[0])
	}
	source, err := readFileDiffSource(context.Background(), dir, hashHex(head), "", files[0], true)
	if err != nil || source == nil || string(source.old) != "one\ntwo\nthree\nfour\nfive\n" || string(source.new) != "one\ntwo\nthree\nfour\nchanged\n" {
		t.Fatalf("rename diff: source=%+v err=%v", source, err)
	}
}

func TestWorktreeDiffTakesFullBottomWidthWithoutInspectFilesPane(t *testing.T) {
	tab := newTab(1, "/tmp/test-wt-width")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "feat: something", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.worktreeCount = 2
	tab.worktreeFiles = []fileChange{
		{status: "M", path: "main.go", extra: "未暂存", hasStat: true, added: 10, deleted: 2},
		{status: "A", path: "new.go", extra: "未跟踪", hasStat: true, added: 20, deleted: 0},
	}
	tab.compareFiles = tab.worktreeFiles
	tab.fileSel = 0
	tab.worktree = true
	tab.selected = 0
	tab.selection.Add(worktreeHash)
	tab.inspecting = zeroHash
	tab.diffNote = "测试差异"

	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	if !tt.HasText("相对 HEAD") || !tt.HasText("提交改动") {
		t.Fatalf("未提交场景应展示文件列表和提交框：%q", tt.Texts())
	}
	if !tt.HasText("并排") || !tt.HasText("内联") || !tt.HasText("测试差异") {
		t.Fatalf("底栏应展示 diff 视图：%q", tt.Texts())
	}
	if tt.HasText("工作区代码") {
		t.Fatalf("侧栏不应再钉着工作区文件列表：%q", tt.Texts())
	}

	// Now switch to normal commit inspection: Mark 1 MUST be present
	tab.worktree = false
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "committed.go", hasStat: true, added: 5, deleted: 1},
	}
	tab.fileSel = 0
	tt.Frame()

	if !tt.HasText("committed.go") {
		t.Fatalf("常规提交详情应正常显示中间文件列表：%q", tt.Texts())
	}
	if !tt.HasText("feat: something") {
		t.Fatalf("常规提交详情应正常显示提交信息面板：%q", tt.Texts())
	}
}

func TestWorktreeCommitCardExpandOnHoverAndPin(t *testing.T) {
	tab := newTab(1, "/tmp/test-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	tab.head = hashN(1)
	tab.graph.add(&tab.commits[0])
	tab.selected = 0
	tab.selection.Add(worktreeHash)
	tab.inspecting = zeroHash
	tab.worktree = true
	tab.worktreeFiles = []fileChange{
		{status: "M", path: "main.go"},
	}
	tab.compareFiles = []fileChange{
		{status: "M", path: "main.go"},
	}
	tab.fileSel = 0
	tab.diffNote = "测试差异"

	a := newApp()
	a.tabs = []*repoTab{tab}
	a.dispatch = func(func()) {}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// 1. Inactive state by default: only single line (header) is shown
	if !tt.HasText("提交改动") || !tt.HasText("未暂存任何文件") {
		t.Fatalf("单行状态应展示标题和状态：%q", tt.Texts())
	}
	if tt.HasText("修改上次提交") {
		t.Fatalf("非活动状态不应展示扩展内容：%q", tt.Texts())
	}

	// 2. Hover over the card -> expands
	cardRect, ok := tt.Find("提交改动")
	if !ok {
		t.Fatal("未找到提交改动卡片")
	}
	tt.Move(cardRect.X+2, cardRect.Y+2)
	tt.Frame()
	if !tt.HasText("修改上次提交") {
		t.Fatalf("鼠标移动到卡片上后应展开面板：%q", tt.Texts())
	}

	// 3. Move mouse away while no text -> collapses
	tt.Move(10, 10)
	tt.Frame()
	if tt.HasText("修改上次提交") {
		t.Fatalf("鼠标移开且无输入时应自动折叠：%q", tt.Texts())
	}

	// 4. If commitMsg is drafted, it stays expanded even with mouse away
	tab.commitMsg = "feat: some changes"
	tt.Frame()
	if !tt.HasText("修改上次提交") {
		t.Fatalf("有输入说明时即使鼠标移开也应保持展开：%q", tt.Texts())
	}

	// 5. Clear commitMsg -> collapses again
	tab.commitMsg = ""
	tt.Frame()
	if tt.HasText("修改上次提交") {
		t.Fatalf("清空提交说明后应自动折叠：%q", tt.Texts())
	}

	// 6. Pin via clicking header -> stays expanded
	if err := tt.Click("提交改动"); err != nil {
		t.Fatalf("点击标题栏失败: %v", err)
	}
	tt.Move(10, 10)
	tt.Frame()
	if !tab.commitPinned || !tt.HasText("修改上次提交") {
		t.Fatalf("点击固定后即使鼠标移开也应保持展开：%q", tt.Texts())
	}
}
