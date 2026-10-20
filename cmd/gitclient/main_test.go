package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEmptyView(t *testing.T) {
	a := &app{split: 64, filePane: 300}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("打开仓库") || !tt.HasText("Xylring") {
		t.Fatalf("空窗口文本：%q", tt.Texts())
	}
}

func TestCommitListView(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2)}, author: "Ada", subject: "支持标签页", when: 1700000000},
		{hash: hashN(2), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.head = tab.commits[0].hash
	tab.refs = map[[20]byte][]refInfo{tab.head: {{name: "main", kind: refBranch}}}
	tab.selected = 0
	tab.commitApplied = 0
	tab.files = []fileChange{{status: "M", path: "说明.txt"}}
	tab.fileSel = 0
	tab.fileApplied = 0
	a := &app{split: 64, filePane: 280, tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	if !tt.HasText("支持标签页") || !tt.HasText("分支") || !tt.HasText("main") {
		t.Fatalf("提交视图文本：%q", tt.Texts())
	}
	if tt.HasText("说明.txt") {
		t.Fatal("未打开改动时不应显示文件")
	}
	click(t, tt, "改动")
	if !tt.HasText("说明.txt") {
		t.Fatalf("改动列表：%q", tt.Texts())
	}
	tab.diffOpen = true
	tt.Frame()
	if !tt.HasText("并排") {
		t.Fatal("点开文件后右侧应显示 diff")
	}
	click(t, tt, "分支")
	if tt.HasText("并排") {
		t.Fatal("回到分支后右侧应恢复图谱")
	}

	other := newTab(2, "/tmp/other")
	other.loaded = true
	other.branch = "dev"
	other.selected = -1
	other.commitApplied = -1
	a.tabs = append(a.tabs, other)
	tt.Frame()
	if err := tt.RightClick("ot"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("关闭"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 1 {
		t.Fatalf("关闭后还剩 %d 个标签", len(a.tabs))
	}
}
