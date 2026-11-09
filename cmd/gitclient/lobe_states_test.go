package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestBusyRowShowsLabelAndHint(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		busyRow(c, "busy", "正在读取改动…", "稍候")
		busyBlock(c, "block", "正在读取提交图谱…", "")
	}, 320, 120)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("正在读取改动…") || !tt.HasText("稍候") || !tt.HasText("正在读取提交图谱…") {
		t.Fatalf("missing busy copy: %q", tt.Texts())
	}
	// The spinner runs a loop, so a still frame must not leave work pending.
	for range 3 {
		tt.Frame()
	}
}

func TestEmptyHintAction(t *testing.T) {
	cleared := 0
	tt := ui.NewTester(func(c *ui.Context) {
		emptyHint(c, "empty", emptyHintProps{
			Icon:        iconSearch,
			Title:       "没有匹配的提交",
			Description: "换个关键词",
			Action: func() {
				if ui.Button(c.Key("clear"), "退出筛选").Clicked() {
					cleared++
				}
			},
		})
	}, 320, 200)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("没有匹配的提交") || !tt.HasText("换个关键词") {
		t.Fatalf("missing empty copy: %q", tt.Texts())
	}
	if err := tt.Click("退出筛选"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if cleared != 1 {
		t.Fatalf("action ran %d times", cleared)
	}
}

func TestEmptyHintWithoutAction(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		emptyHint(c, "empty", emptyHintProps{Title: "无图片内容"})
	}, 240, 160)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("无图片内容") {
		t.Fatalf("missing title: %q", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "无图片内容"); ok {
		t.Fatal("empty hint turned its title into a button")
	}
}

func TestChipRendersLabelAndKeepsMenu(t *testing.T) {
	chose := ""
	tt := ui.NewTester(func(c *ui.Context) {
		t := c.Theme()
		head := chip(c, "head", "HEAD", chipStyle{Text: ui.RGBA(255, 255, 255, 1), Background: t.Accent, Bold: true})
		head.Tooltip("当前检出位置")
		branch := chipSoft(c, "branch", "main", t.Accent, true)
		branch.ContextMenu(func(m *ui.Menu) {
			if m.Item("检出").Chosen() {
				chose = "checkout"
			}
		})
		chip(c, "remote", "origin/main", chipStyle{Text: t.TextMuted, Background: t.Surface, Border: t.Border})
		chip(c, "tag", "标签 v1.0.0", chipStyle{Text: t.Warning, Background: t.Warning.Alpha(0.14), Border: t.Warning.Alpha(0.45)})
	}, 420, 120)
	defer retireTester(tt)
	tt.Frame()
	for _, text := range []string{"HEAD", "main", "origin/main", "标签 v1.0.0"} {
		if !tt.HasText(text) {
			t.Fatalf("missing chip %q: %q", text, tt.Texts())
		}
	}
	if err := tt.RightClick("main"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); menu == nil {
		t.Fatal("chip lost its context menu")
	}
	if err := tt.ChooseMenuItem("检出"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if chose != "checkout" {
		t.Fatalf("chip menu lost: %q", chose)
	}
}

func TestNoMatchingFilesClearsFilter(t *testing.T) {
	filter := "xyz"
	tt := ui.NewTester(func(c *ui.Context) {
		noMatchingFiles(c, &filter)
	}, 360, 220)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("无匹配文件") || !tt.HasText("换个关键词，或清除筛选查看全部改动") {
		t.Fatalf("missing copy: %q", tt.Texts())
	}
	if err := tt.Click("清除筛选"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if filter != "" {
		t.Fatalf("filter kept %q", filter)
	}
}

func TestFilesEmptyHintReasons(t *testing.T) {
	worktree := newTab(1, "/work/example")
	worktree.worktree = true
	if got := filesEmptyHint(worktree); got != "工作区没有未提交的改动" {
		t.Fatalf("worktree: %q", got)
	}
	compared := newTab(2, "/work/example")
	compared.comparison = &commitComparison{}
	if got := filesEmptyHint(compared); got != "这两个版本没有文件差异" {
		t.Fatalf("comparison: %q", got)
	}
	commitTab := newTab(3, "/work/example")
	if got := filesEmptyHint(commitTab); got != "这条提交没有改动任何文件" {
		t.Fatalf("commit: %q", got)
	}
}

func TestCommitListEmptyCopy(t *testing.T) {
	cases := []struct {
		name, text, description string
		prepare                 func(*repoTab)
	}{
		{"fresh", "这个仓库还没有提交", "做出第一次提交后，历史会出现在这里", func(*repoTab) {}},
		{"search", "没有匹配的提交", "换个关键词，或退出筛选查看全部历史", func(tab *repoTab) { tab.query.term = "needle" }},
		{"file", "这个文件在所选版本之前没有历史", "选择更新的版本，或退出文件历史", func(tab *repoTab) { tab.query.filePath = "src/a.go" }},
		{"range", "当前范围没有提交", "扩大分支范围可以看到更多历史", func(tab *repoTab) { tab.query.ref = "main" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := newTab(1, "/work/example")
			tab.loaded = true
			tc.prepare(tab)
			a := &app{tabs: []*repoTab{tab}}
			tt := ui.NewTester(a.view, 1100, 720)
			defer retireTester(tt)
			tt.Frame()
			if !tt.HasText(tc.text) || !tt.HasText(tc.description) {
				t.Fatalf("missing empty copy %q / %q: %q", tc.text, tc.description, tt.Texts())
			}
			if tc.name == "search" && !tt.HasText("退出筛选") {
				t.Fatalf("search empty state offers no way out: %q", tt.Texts())
			}
		})
	}
}

func TestCopyTextButtonConfirmsInPlace(t *testing.T) {
	content := "commit message"
	tt := ui.NewTester(func(c *ui.Context) {
		copyTextButton(c, "copy-info", "复制完整信息", content)
	}, 240, 80)
	defer retireTester(tt)
	tt.Frame()
	if err := tt.Click("复制完整信息"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.Clipboard() != content {
		t.Fatalf("clipboard = %q", tt.Clipboard())
	}
	if !tt.HasText("已复制") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	tt.Advance(2100 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("复制完整信息") {
		t.Fatalf("confirmation did not expire: %q", tt.Texts())
	}
}
