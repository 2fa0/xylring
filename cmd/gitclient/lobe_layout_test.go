package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func saveLobePreview(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("LOBE_UI_PNG_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestLobeModalLayouts(t *testing.T) {
	cases := []struct {
		name, title, action string
		prepare             func(*app)
		view                func(*app, *ui.Context)
	}{
		{"write", "新建分支", "创建", func(a *app) { a.showWrite = true; a.write.kind = writeBranch; a.write.name = "feature/example" }, (*app).commitWriteModal},
		{"fetch", "获取远程", "获取", func(a *app) { a.showFetch = true; a.fetch.remotes = []string{"origin"} }, (*app).fetchModal},
		{"compare", "比较", "开始比较", func(a *app) { a.showCompare = true; a.compareFrom = "main"; a.compareTo = "feature/example" }, (*app).comparisonModal},
		{"operation", "设置身份", "执行", func(a *app) {
			a.showOperation = true
			a.op.action = "设置身份"
			a.op.repo = "/work/example"
			a.op.ready = true
			a.op.output = strings.Repeat("user.name=Example\n", 40)
		}, (*app).operationModal},
		{"ring", "本仓库年轮记录", "关闭", func(a *app) { a.tabs[0].ring.open = true; a.tabs[0].ring.status = "未开启" }, (*app).ringModal},
		{"discard", "确定丢弃文件修改？", "确定丢弃", func(a *app) { a.showDiscard = true; a.discard.file = fileChange{path: "src/example.go"} }, (*app).discardModal},
	}
	for _, tc := range cases {
		for _, size := range [][2]int{{420, 320}, {1100, 720}} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/dark=%t", tc.name, size[0], dark), func(t *testing.T) {
					tab := graphTabForWrite()
					a := &app{tabs: []*repoTab{tab}}
					tc.prepare(a)
					tt := ui.NewTester(func(c *ui.Context) { tc.view(a, c) }, size[0], size[1])
					defer retireTester(tt)
					tt.SetDark(dark)
					tt.Frame()
					tt.Frame()
					dialog, ok := tt.AccessByRole(ui.RoleDialog, tc.title)
					if !ok {
						t.Fatalf("missing dialog %q: %v", tc.title, tt.Texts())
					}
					if dialog.Bounds.X < 0 || dialog.Bounds.Y < 0 || dialog.Bounds.X+dialog.Bounds.W > float32(size[0]) || dialog.Bounds.Y+dialog.Bounds.H > float32(size[1]) {
						t.Fatalf("dialog outside window: %+v", dialog.Bounds)
					}
					for _, name := range []string{"关闭对话框", tc.action} {
						btn, ok := tt.AccessByRole(ui.RoleButton, name)
						if !ok || btn.Bounds.H <= 0 || btn.Bounds.Y < dialog.Bounds.Y || btn.Bounds.Y+btn.Bounds.H > dialog.Bounds.Y+dialog.Bounds.H+1 {
							t.Fatalf("action %q clipped: %+v dialog=%+v", name, btn.Bounds, dialog.Bounds)
						}
					}
					saveLobePreview(t, tt, fmt.Sprintf("%s-%d-dark-%t", tc.name, size[0], dark))
					tt.Key(0, ui.KeyEscape)
					tt.Frame()
					if _, ok := tt.AccessByRole(ui.RoleDialog, tc.title); ok {
						t.Fatal("Escape did not dismiss idle dialog")
					}
				})
			}
		}
	}
}

func TestLobeModalBusyDismissAndCancel(t *testing.T) {
	open, busy, cancelled := true, true, false
	tt := ui.NewTester(func(c *ui.Context) {
		appModal(c, &open, "执行任务", 500, busy, func() { ui.Text(c, "正在执行") }, func() {
			if ui.Button(c, "取消执行").Clicked() {
				cancelled = true
				busy = false
			}
		})
	}, 420, 320)
	defer retireTester(tt)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !open {
		t.Fatal("Escape dismissed running job")
	}
	tt.ClickAt(1, 1)
	tt.Frame()
	if !open {
		t.Fatal("backdrop dismissed running job")
	}
	if err := tt.Click("取消执行"); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("cancel action unavailable")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if open {
		t.Fatal("idle dialog did not close")
	}
	open = true
	tt.Frame()
	tt.Frame()
	if err := tt.Click("关闭对话框"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if open {
		t.Fatal("header close did not work after reopening")
	}
}

func TestLobeSearchClearFocusAndLoading(t *testing.T) {
	tab := newTab(1, "/work/example")
	tab.query = historyQuery{term: "existing", kind: "说明"}
	tab.searchText = "author:Alice"
	tab.locate = tab.searchText
	submissions := 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Width(360).Children(func() {
			_, submit := omniSearchField(c, tab)
			if submit {
				submissions++
			}
		})
	}, 420, 100)
	defer retireTester(tt)
	tt.Frame()
	if err := tt.Click("清空搜索输入"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("path:README")
	tt.Frame()
	if tab.searchText != "path:README" || tab.query.term != "existing" || submissions != 0 {
		t.Fatalf("clear lost focus or changed filter: text=%q query=%+v submits=%d", tab.searchText, tab.query, submissions)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if submissions != 1 {
		t.Fatalf("Enter submissions=%d", submissions)
	}
	tab.loading = true
	tab.query.term = tab.searchText
	tab.query.kind = tab.searchKind
	tt.Frame()
	if _, ok := tt.Find("正在搜索"); !ok {
		t.Fatal("loading indicator missing")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if submissions != 1 {
		t.Fatal("loading search submitted twice")
	}
	tab.loading = false
	tt.Frame()
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "搜索"); !ok {
		t.Fatal("search action did not return")
	}
}

func TestLobeFileTabsIndicatorAndClose(t *testing.T) {
	tab := newTab(1, "/work/example")
	tab.fileTabs = []*fileDiffTab{{id: 1, file: fileChange{path: "src/first.go"}}, {id: 2, file: fileChange{path: "src/second.go"}}}
	tab.contentTab = 1
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(func(c *ui.Context) { a.fileTabBar(c, tab) }, 420, 100)
	defer retireTester(tt)
	tt.Frame()
	if err := tt.Click("second.go · " + shortHash(tab.fileTabs[1].to)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	if tab.contentTab != 2 {
		t.Fatal("tab switch failed")
	}
	saveLobePreview(t, tt, "file-tabs")
	a.closeFileTab(tab, 0)
	tt.Frame()
	tt.Frame()
	if tab.contentTab != 1 || len(tab.fileTabs) != 1 {
		t.Fatal("closing earlier tab lost selection")
	}
	a.closeAllFileTabs(tab)
	tt.Frame()
	tt.Frame()
	tab.fileTabs = []*fileDiffTab{{id: 3, file: fileChange{path: "third.go"}}}
	tab.contentTab = 1
	tt.Frame()
	tt.Frame()
	if !tt.HasText("third.go") {
		t.Fatal("tabs did not return after removal")
	}
}

func TestLobeMainLayout(t *testing.T) {
	for _, size := range [][2]int{{860, 560}, {1200, 760}} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/dark=%t", size[0], dark), func(t *testing.T) {
				tab := graphTabForWrite()
				a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
				tt := ui.NewTester(a.view, size[0], size[1])
				defer retireTester(tt)
				tt.SetDark(dark)
				tt.Frame()
				tt.Advance(200 * time.Millisecond)
				tt.Frame()
				field, ok := tt.AccessByRole(ui.RoleTextField, "搜索提交、代码、分支、作者或路径")
				if !ok || field.Bounds.W < 80 || field.Bounds.X < 0 || field.Bounds.X+field.Bounds.W > float32(size[0]) {
					t.Fatalf("search clipped: %+v", field.Bounds)
				}
				tt.Key(commandModifier(), ui.KeyP)
				tt.Type("author:Alice")
				tt.Frame()
				if tab.searchText != "author:Alice" {
					t.Fatal("search shortcut lost focus")
				}
				if err := tt.Click("清空搜索输入"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if tab.searchText != "" || tab.locate != "" {
					t.Fatal("clear restored old query")
				}
				tt.Key(0, ui.KeyEscape)
				tt.Frame()
				saveLobePreview(t, tt, fmt.Sprintf("main-%d-dark-%t", size[0], dark))
			})
		}
	}
}
