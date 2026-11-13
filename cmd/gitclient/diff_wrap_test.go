package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/egoist/mygo/ui/editor"
)

// diffWrapTestTab is a loaded compare tab whose diff is one changed line, as
// selectCommit leaves one behind.
func diffWrapTestTab() *repoTab {
	tab := newTab(1, "/tmp/diff-wrap-repo")
	tab.loaded, tab.branch = true, "main"
	tab.commits = []commit{{hash: hashN(1), author: "Ada", subject: "初始提交", when: 1700000000}}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{{status: "M", path: "cmd/main.go"}}
	tab.fileSel = 0
	tab.diff = &editor.DiffState{
		Old: editor.NewBuffer("hello world\n"),
		New: editor.NewBuffer("hello xylring\n"),
	}
	return tab
}

func TestDiffWrapToggleWrapsTheDiff(t *testing.T) {
	tab := diffWrapTestTab()
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	defer tab.cancel()
	tt := ui.NewTester(a.view, 1100, 720)
	defer retireTester(tt)
	tt.Frame()

	if tab.diff.Wrap {
		t.Fatal("折行默认应为关闭")
	}
	if err := tt.Click("折行"); err != nil {
		t.Fatalf("点击折行失败: %v", err)
	}
	tt.Frame()
	if !a.diffWrap || !tab.diff.Wrap {
		t.Fatalf("折行未生效: app=%v diff=%v", a.diffWrap, tab.diff.Wrap)
	}
	if !a.preferences().Wrap {
		t.Fatal("折行未写进偏好，重开应用后会丢失")
	}

	if err := tt.Click("折行"); err != nil {
		t.Fatalf("再次点击折行失败: %v", err)
	}
	tt.Frame()
	if a.diffWrap || tab.diff.Wrap {
		t.Fatalf("折行未关闭: app=%v diff=%v", a.diffWrap, tab.diff.Wrap)
	}
}
