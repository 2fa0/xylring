package main

import (
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestBuildFileTreeCompact(t *testing.T) {
	files := []fileChange{
		{status: "M", path: "LICENSE"},
		{status: "M", path: "cmd/gitclient/main.go"},
		{status: "A", path: "cmd/gitclient/compare.go"},
		{status: "D", path: "docs/readme.md"},
		{status: "R", path: "scripts/new.py", oldPath: "scripts/old.py"},
	}
	tree := buildFileTree(files)
	wantRoots := []string{"LICENSE", "cmd/gitclient/", "docs/", "scripts/"}
	if !reflect.DeepEqual(tree.roots, wantRoots) {
		t.Fatalf("roots %q", tree.roots)
	}
	if n := tree.nodes["cmd/gitclient/"]; !n.dir || n.name != "cmd/gitclient" {
		t.Fatalf("compact dir %+v", n)
	}
	wantKids := []string{"cmd/gitclient/main.go", "cmd/gitclient/compare.go"}
	if !reflect.DeepEqual(tree.kids["cmd/gitclient/"], wantKids) {
		t.Fatalf("cmd kids %q", tree.kids["cmd/gitclient/"])
	}
	if n := tree.nodes["cmd/gitclient/compare.go"]; n.dir || n.file != 2 || n.name != "compare.go" {
		t.Fatalf("file node %+v", n)
	}
	if tree.children("LICENSE") != nil {
		t.Fatal("file should be a leaf")
	}
	var open ui.Selection[string]
	for k, n := range tree.nodes {
		if n.dir {
			open.Add(k)
		}
	}
	if row := tree.rowOf("cmd/gitclient/compare.go", &open); row != 3 {
		t.Fatalf("compare.go row %d", row)
	}
}

func TestFileTreeKeepsFileDirectoryReplacements(t *testing.T) {
	for _, files := range [][]fileChange{
		{{status: "D", path: "config"}, {status: "A", path: "config/app.json"}},
		{{status: "D", path: "config/app.json"}, {status: "A", path: "config"}},
		{{status: "D", path: "config/app"}, {status: "A", path: "config/app/settings.json"}},
	} {
		t.Run(files[0].path, func(t *testing.T) {
			tab := graphTabForWrite()
			tab.inspecting = tab.head
			tab.compareFiles = files
			tab.fileSel = 0
			tab.fileSplit = 40
			tab.diffNote = "没有载入内容"
			a := &app{tabs: []*repoTab{tab}, fileView: 1, dispatch: func(func()) {}}
			tree := tab.prepareFileTree()
			for i, f := range files {
				n, ok := tree.nodes[f.path]
				if !ok || n.dir || n.file != i || tree.rowOf(f.path, &tab.fileOutline.Open) < 0 {
					t.Fatalf("file missing from tree: %q nodes=%+v", f.path, tree.nodes)
				}
			}
			tt := ui.NewTester(a.view, 1100, 720)
			tt.Frame()
			for _, i := range []int{1, 0} {
				if err := tt.Click(files[i].label()); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				tt.Frame()
				if tab.fileSel != i {
					t.Fatalf("file selection: got %d want %d", tab.fileSel, i)
				}
			}
		})
	}
}

func TestFileTreeToggle(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "画出提交图谱", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "LICENSE"},
		{status: "M", path: "cmd/gitclient/main.go"},
		{status: "A", path: "cmd/gitclient/compare.go"},
	}
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "没有载入内容"
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("仓库根目录") || !tt.HasText("列表") || !tt.HasText("树") {
		t.Fatalf("列表视图缺少控件：%q", tt.Texts())
	}
	if err := tt.Click("树"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("仓库根目录") {
		t.Fatalf("树状仍显示列表副标题：%q", tt.Texts())
	}
	for _, text := range []string{"cmd/gitclient", "LICENSE", "main.go", "compare.go", "M  LICENSE", "A  cmd/gitclient/compare.go"} {
		if !tt.HasText(text) {
			t.Fatalf("树状缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if err := tt.Click("compare.go"); err != nil {
		t.Fatal(err)
	}
	if tab.fileSel != 2 {
		t.Fatalf("树状点选文件后 fileSel=%d", tab.fileSel)
	}
	if err := tt.Click("列表"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("仓库根目录") {
		t.Fatalf("切回列表后丢失副标题：%q", tt.Texts())
	}
	if tab.fileSel != 2 {
		t.Fatalf("切回列表后选中丢失 fileSel=%d", tab.fileSel)
	}
}

func TestFileHoverShowsActionsOnThatRow(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "画出提交图谱", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "LICENSE", added: 2, deleted: 1, hasStat: true},
		{status: "M", path: "cmd/gitclient/main.go"},
		{status: "A", path: "cmd/gitclient/compare.go", added: 381, deleted: 545, hasStat: true},
	}
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "没有载入内容"
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if tt.HasText("在新标签页打开") || tt.HasText("文件历史") || tt.HasText("复制路径") {
		t.Fatalf("未悬停就显示了文件操作: %q", tt.Texts())
	}
	r, ok := tt.Find("compare.go")
	if !ok {
		t.Fatal("missing compare.go")
	}
	if !tt.HasText("+381") || !tt.HasText("-545") || !tt.HasText("+2") {
		t.Fatalf("悬停前缺少行数: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	if !tt.HasText("在新标签页打开") || !tt.HasText("文件历史") || !tt.HasText("复制路径") {
		t.Fatalf("悬停文件后没有操作按钮: %q", tt.Texts())
	}
	if tt.HasText("+381") || tt.HasText("-545") {
		t.Fatalf("悬停后行数还露在菜单外: %q", tt.Texts())
	}
	if !tt.HasText("+2") {
		t.Fatalf("未悬停的文件行数被收掉了: %q", tt.Texts())
	}
	open, _ := tt.Find("在新标签页打开")
	hist, _ := tt.Find("文件历史")
	if open.X+open.W < r.X || hist.X+hist.W < r.X {
		t.Fatalf("操作按钮不在文件右侧: file=%+v open=%+v hist=%+v", r, open, hist)
	}
	bar, ok := tt.Find("文件操作")
	if !ok {
		t.Fatal("missing hover bar")
	}
	if bar.Y > r.Y+1 || bar.Y+bar.H < r.Y+r.H+12 {
		t.Fatalf("浮动操作没有挡住目录行: file=%+v bar=%+v", r, bar)
	}
	lic, ok := tt.Find("LICENSE")
	if !ok {
		t.Fatal("missing LICENSE")
	}
	tt.Move(lic.X+lic.W/2, lic.Y+lic.H/2)
	if err := tt.Click("在新标签页打开"); err != nil {
		t.Fatal(err)
	}
	if len(tab.fileTabs) != 1 || tab.contentTab != 1 || tab.fileTabs[0].file.path != "LICENSE" {
		t.Fatalf("悬停打开了错误的文件: tabs=%d sel=%d", len(tab.fileTabs), tab.contentTab)
	}
	tab.contentTab = 0
	tt.Frame()
	tt.Move(8, 8)
	if tt.HasText("在新标签页打开") || tt.HasText("文件历史") || tt.HasText("复制路径") {
		t.Fatalf("离开后仍显示文件操作: %q", tt.Texts())
	}
}

func TestFileTreeSpacingAndIndentation(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "LICENSE"},
		{status: "M", path: "cmd/gitclient/main.go"},
		{status: "A", path: "cmd/gitclient/compare.go"},
	}
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "没有载入内容"
	a := &app{tabs: []*repoTab{tab}, fileView: 1, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	rCmd, okCmd := tt.Find("cmd/gitclient")
	rMain, okMain := tt.Find("main.go")
	rComp, okComp := tt.Find("compare.go")
	if !okCmd || !okMain || !okComp {
		t.Fatalf("树节点未找到: cmd=%v main=%v comp=%v", okCmd, okMain, okComp)
	}

	// Verify compact row height: delta between consecutive sibling rows should be <= 32px (previously 44px)
	rowDelta := rComp.Y - rMain.Y
	if rowDelta > 32 {
		t.Fatalf("树行间距过大: delta=%.1f (期望 <= 32)", rowDelta)
	}

	// Verify compact directory row height: should be <= 30px (previously 40px)
	if rCmd.H > 30 {
		t.Fatalf("目录行高度过大: H=%.1f (期望 <= 30)", rCmd.H)
	}

	// Verify compact indentation: delta between child file and root file should be <= 10px (previously 24px)
	rLic, okLic := tt.Find("LICENSE")
	if !okLic {
		t.Fatal("树节点未找到: LICENSE")
	}
	indentDelta := rMain.X - rLic.X
	if indentDelta > 10 {
		t.Fatalf("层级缩进过大: delta=%.1f (期望 <= 10)", indentDelta)
	}
}
