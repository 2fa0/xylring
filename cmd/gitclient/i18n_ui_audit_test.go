package main

import (
	"testing"
	"unicode"

	"github.com/egoist/mygo/ui"
)

func assertEnglishUITexts(t *testing.T, tt *ui.Tester) {
	t.Helper()
	for _, text := range tt.Texts() {
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("Chinese application text in English UI: %q", text)
				break
			}
		}
	}
}

func TestEnglishGraphAndDiffHaveNoChineseApplicationText(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	updates := make(chan func(), 64)
	tab := newTab(1, "/tmp/example")
	tab.loaded, tab.branch = true, "main"
	tab.commits = []commit{{hash: hashN(1), author: "Ada", subject: "First commit", when: 1700000000}}
	tab.graph.add(&tab.commits[0])
	a := &app{tabs: []*repoTab{tab}, bottomFullWidth: true, dispatch: func(fn func()) { updates <- fn }}
	tt := ui.NewTester(a.view, 1200, 800)
	defer retireTester(tt)
	defer tab.cancel()
	tt.Frame()
	drainUpdates(updates)
	assertEnglishUITexts(t, tt)
	if !tt.HasText("1 commits") {
		t.Fatalf("missing translated commit count: %q", tt.Texts())
	}
	// Worker callbacks must run on the test goroutine, exactly as the window loop
	// serializes them in production, or they race with rendering over repoTab.
	tab.hasMore = true
	tt.Frame()
	drainUpdates(updates)
	tt.Frame()
	drainUpdates(updates)
	assertEnglishUITexts(t, tt)

	tab.compareFiles = []fileChange{{path: "main.go", status: "M"}}
	tab.fileSel = 0
	tab.diffNote = "二进制文件，已忽略差异"
	diff := ui.NewTester(func(c *ui.Context) { a.fileDiffPane(c, tab) }, 900, 500)
	defer retireTester(diff)
	diff.Frame()
	assertEnglishUITexts(t, diff)
	if !diff.HasText("Binary file; diff omitted") {
		t.Fatalf("untranslated cached diff note: %q", diff.Texts())
	}
}

func TestEnglishFileTabsAndImageBadgesHaveNoChineseApplicationText(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	tab := newTab(1, "/tmp/example")
	tab.fileTabs = []*fileDiffTab{{id: 1, file: fileChange{path: "example.go"}, worktree: true}}
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(func(c *ui.Context) {
		a.fileTabBar(c, tab)
		a.renderImageCard(c, "image", tr("新增图片"), "新增", c.Theme().Success, nil, false, nil)
	}, 700, 500)
	defer retireTester(tt)
	tt.Frame()
	assertEnglishUITexts(t, tt)
	if !tt.HasText("Added") {
		t.Fatalf("untranslated image badge: %q", tt.Texts())
	}
}

func TestEnglishGraphPreservesChineseUserContent(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	tab := newTab(1, "/tmp/中文仓库")
	tab.loaded, tab.branch = true, "中文分支"
	tab.commits = []commit{{hash: hashN(1), author: "中文作者", subject: "修复中文数据", when: 1700000000}}
	tab.graph.add(&tab.commits[0])
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1200, 800)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("修复中文数据") || !tt.HasText("中文作者") {
		t.Fatalf("user content was changed: %q", tt.Texts())
	}
}
