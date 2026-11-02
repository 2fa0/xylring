package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEmptyView(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("导入仓库") || !tt.HasText("Xylring") {
		t.Fatalf("空窗口文本：%q", tt.Texts())
	}
}

func TestCommitGraphView(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2)}, author: "Ada", subject: "画出提交图谱", when: 1700000000},
		{hash: hashN(2), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.head = tab.commits[0].hash
	tab.refs = map[[20]byte][]refInfo{tab.head: {{name: "main", kind: refBranch}}}
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"画出提交图谱", "图谱", "main", "HEAD", "导入仓库"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("拉取") || tt.HasText("Stash") {
		t.Fatal("图谱视图还留着已删除的操作")
	}
	if err := tt.Click("关闭仓库"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 0 || !tt.HasText("导入仓库") {
		t.Fatalf("关闭后没有回到导入页：tabs=%d texts=%q", len(a.tabs), tt.Texts())
	}
}

func TestRepoListShows(t *testing.T) {
	tab := newTab(1, "/tmp/work/alpha")
	tab.loaded = true
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/alpha", "/tmp/work/alpha/lib/nested", "/tmp/work/beta"},
		foundFrom: "/tmp/work",
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"alpha", "alpha/lib/nested", "beta"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("切换仓库") || tt.HasText("选择要打开的仓库") {
		t.Fatalf("还在用独立选择页：%q", tt.Texts())
	}
}

func TestRepoListSwitches(t *testing.T) {
	root := t.TempDir()
	alpha := root + "/alpha"
	beta := root + "/beta"
	initGitDir(t, alpha)
	initGitDir(t, beta)
	tab := newTab(1, alpha)
	tab.loaded = true
	updates := make(chan func(), 16)
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{alpha, beta},
		foundFrom: root,
		persist:   false,
		dispatch:  func(fn func()) { updates <- fn },
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("beta"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, item := range a.tabs {
			item.cancel()
		}
	}()
	if len(a.found) != 2 || len(a.tabs) != 2 || a.tabs[a.active].path != beta {
		t.Fatalf("found=%v tabs=%d active=%q", a.found, len(a.tabs), a.tabs[a.active].path)
	}
	if !tt.HasText("beta") || !tt.HasText("alpha") {
		t.Fatalf("切换后列表不见了：%q", tt.Texts())
	}
}

func TestChromeClearsTitleBarControls(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 72, Right: 10})
	tt.Frame()
	r, ok := tt.Find("导入仓库")
	if !ok {
		t.Fatalf("没有导入仓库：%q", tt.Texts())
	}
	if r.X < 72 {
		t.Fatalf("导入仓库还在红绿灯下面：x=%.0f", r.X)
	}
}

func TestOpenReposShareSidebar(t *testing.T) {
	one := newTab(1, "/tmp/work/alpha")
	two := newTab(2, "/tmp/other/beta")
	one.loaded, two.loaded = true, true
	a := &app{tabs: []*repoTab{one, two}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"alpha", "beta"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("● ") {
		t.Fatalf("还在用顶部标签：%q", tt.Texts())
	}
}
