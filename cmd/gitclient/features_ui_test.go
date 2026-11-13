package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestHistoryControlsStayVisible(t *testing.T) {
	dir, git := renameHistoryRepo(t)
	longBranch := "feature/a-long-branch-name-that-must-fit-in-the-toolbar"
	git("branch", longBranch)
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, found: []string{dir, dir + "/other"}, foundFrom: dir, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 1)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 1200, 800}, {"narrow", 860, 560}} {
		t.Run(size.name, func(t *testing.T) {
			tt := ui.NewTester(a.view, size.w, size.h)
			tt.SetTitleBar(ui.TitleBar{Height: 40, Left: 72, Right: 12})
			for _, dark := range []bool{false, true} {
				tt.SetDark(dark)
				tt.Frame()
				hoverSelectedFile(t, tt, tab)
				for _, text := range []string{"搜索", "本地分支", "全部分支", "筛选分支", "图谱", "复制哈希", "完整信息", "复制路径", "文件历史", "比较"} {
					r, ok := tt.Find(text)
					if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > float32(size.w)+1 || r.Y+r.H > float32(size.h)+1 {
						t.Fatalf("%q is outside the window: %+v", text, r)
					}
				}
				preview := os.Getenv("XYLRING_PREVIEW_DIR")
				if preview != "" {
					if err := os.MkdirAll(preview, 0o700); err != nil {
						t.Fatal(err)
					}
					mode := "light"
					if dark {
						mode = "dark"
					}
					f, err := os.Create(filepath.Join(preview, size.name+"-"+mode+".png"))
					if err != nil {
						t.Fatal(err)
					}
					if err := png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			tab.searchText = "modify"
			a.searchHistory(tab)
			waitUpdates(t, updates, func() bool { return !tab.loading })
			q := tab.query
			q.ref = "refs/heads/" + longBranch
			a.changeScope(tab, q)
			waitUpdates(t, updates, func() bool { return !tab.loading })
			tt.Frame()
			search, _ := tt.Find("搜索")
			back, ok := tt.Find("返回")
			if !ok || back.W <= 0 || back.X+back.W > float32(size.w) || search.X+search.W > back.X {
				t.Fatalf("search controls overlap: search=%+v back=%+v", search, back)
			}
			a.selectCommit(tab, 0)
			waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
			a.openFileHistory(tab)
			waitUpdates(t, updates, func() bool { return !tab.loading })
			tt.Frame()
			for _, text := range []string{"在图谱中定位", "返回"} {
				r, ok := tt.Find(text)
				if !ok || r.W <= 0 || r.H <= 0 || r.X+r.W > float32(size.w) || r.Y < 40 {
					t.Fatalf("file history control is hidden: %q %+v", text, r)
				}
			}
			a.leaveFileHistory(tab)
			waitUpdates(t, updates, func() bool { return !tab.loading && !tab.compareLoading && !tab.diffLoading })
			a.leaveSearch(tab)
			waitUpdates(t, updates, func() bool { return !tab.loading && !tab.compareLoading && !tab.diffLoading })
			if tab.query.ref != "refs/heads/"+longBranch || tab.query.term != "" {
				t.Fatal("return from search did not preserve the chosen branch")
			}
			a.changeScope(tab, historyQuery{})
			waitUpdates(t, updates, func() bool { return !tab.loading })
			a.selectCommit(tab, 1)
			waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
			retireTester(tt)
		})
	}
}

func TestWorkflowOptimizations(t *testing.T) {
	badges := groupCommitRefs([]refInfo{
		{name: "main", kind: refBranch},
		{name: "origin/main", kind: refRemote},
		{name: "origin/HEAD", kind: refRemote},
		{name: "origin/dev", kind: refRemote},
		{name: "v1.0.0", kind: refTag},
	}, false)
	if len(badges) != 4 {
		t.Fatalf("expected 4 grouped badges, got %+v", badges)
	}
	if badges[0].kind != refBranch || badges[0].name != "main" || len(badges[0].remotes) != 1 || badges[0].remotes[0] != "origin" {
		t.Fatalf("expected main merged with origin, got %+v", badges[0])
	}

	dir, _ := renameHistoryRepo(t)
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 1)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading && tab.details != nil })

	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// 1. 放大阅读 / 还原布局 & 关闭详情
	if err := tt.Click("放大阅读"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tab.diffMaximized || !tt.HasText("还原布局") {
		t.Fatal("diffMaximized not enabled")
	}
	if err := tt.Click("还原布局"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.diffMaximized {
		t.Fatal("diffMaximized not restored")
	}
	if err := tt.Click("关闭详情"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.inspecting != zeroHash {
		t.Fatal("closeBottomPane did not clear inspecting")
	}

	// 2. 搜索结果中直接定位到此提交
	tab.searchText = "modify"
	a.searchHistory(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 0)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	targetHash := tab.inspecting
	tt.Frame()
	if err := tt.Click("定位到此提交"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading && !tab.compareLoading && !tab.diffLoading })
	tt.Frame()
	if tab.query.term != "" || tab.inspecting != targetHash {
		t.Fatalf("locateCommitInGraph failed: query=%+v inspecting=%x want=%x", tab.query, tab.inspecting, targetHash)
	}
	retireTester(tt)
}
