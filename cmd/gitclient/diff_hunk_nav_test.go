package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/egoist/mygo/ui/editor"
)

// preparedDiff returns a diff whose texts change two lines, so it has two
// hunks, as fileDiffContent renders one.
func preparedDiff(a *app) *editor.DiffState {
	var oldLines, newLines []string
	for i := 1; i <= 20; i++ {
		line := "line " + strings.Repeat("x", i)
		oldLines = append(oldLines, line)
		if i == 4 || i == 16 {
			line = "changed " + line
		}
		newLines = append(newLines, line)
	}
	d := &editor.DiffState{
		Old: editor.NewBuffer(strings.Join(oldLines, "\n") + "\n"),
		New: editor.NewBuffer(strings.Join(newLines, "\n") + "\n"),
	}
	a.prepareDiffState(d)
	return d
}

func TestPrepareDiffStateSharesOptionsAndHunkKeys(t *testing.T) {
	a := &app{diffLayout: 1, diffWrap: true}
	d := preparedDiff(a)
	if !d.Wrap {
		t.Fatal("折行没进 DiffState")
	}
	if d.Layout != editor.DiffUnified {
		t.Fatal("排版没进 DiffState")
	}
	plain := func(key ui.Key) editor.Command {
		for _, b := range d.Bindings {
			if b.Mods == 0 && b.ThenKey == 0 && b.Key == key {
				return b.Command
			}
		}
		return editor.NoCommand
	}
	if plain(ui.KeyJ) != editor.NextHunk || plain(ui.KeyK) != editor.PreviousHunk {
		t.Fatalf("J / K 没绑到改动块导航: J=%v K=%v", plain(ui.KeyJ), plain(ui.KeyK))
	}
	if !slices.ContainsFunc(d.Bindings, func(b editor.Binding) bool {
		return b.Key == ui.KeyF8 && b.Command == editor.NextHunk
	}) {
		t.Fatal("默认的 ⌘F8 绑定被顶掉了")
	}
}

// TestDiffHunkNavUsesTheCursorsLine 验证第 2 步的前置问题：mygo 的 gotoHunk
// 按光标所在行找下一个改动块（gitdiff.go），而 diff 在被点过之前光标就落第 0 行，
// 所以 J 从第一个改动块开始，不需要我们自己维护改动块游标。
func TestDiffHunkNavUsesTheCursorsLine(t *testing.T) {
	a := &app{}
	d := preparedDiff(a)
	tt := ui.NewTester(func(c *ui.Context) { editor.DiffView(c.Key("diff"), d) }, 900, 500)
	defer retireTester(tt)
	tt.Frame()
	hunks := d.Hunks()
	if len(hunks) != 2 {
		t.Fatalf("应有 2 个改动块，得到 %d 个", len(hunks))
	}
	if line := d.CursorPos().Line; line != 0 {
		t.Fatalf("没点过的 diff 光标应在第 0 行，实为第 %d 行", line)
	}
	if !d.Run(editor.NextHunk) {
		t.Fatal("J 没有跳转")
	}
	if line := d.CursorPos().Line; line != hunks[0].NewStart {
		t.Fatalf("J 应停在第 %d 行（第一个改动块），停在 %d", hunks[0].NewStart, line)
	}
	d.Run(editor.NextHunk)
	if line := d.CursorPos().Line; line != hunks[1].NewStart {
		t.Fatalf("J 应停在第 %d 行（第二个改动块），停在 %d", hunks[1].NewStart, line)
	}
	d.Run(editor.PreviousHunk)
	if line := d.CursorPos().Line; line != hunks[0].NewStart {
		t.Fatalf("K 应回到第 %d 行，停在 %d", hunks[0].NewStart, line)
	}
}

// TestDiffHunkNavKeyReachesTheDiff 确认 J / K 走的是 DiffState.Bindings：
// 点进 diff 后按 J 跳到下一个改动块，K 回到上一个。
func TestDiffHunkNavKeyReachesTheDiff(t *testing.T) {
	tab := diffWrapTestTab()
	tab.diff = preparedDiff(&app{})
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	defer tab.cancel()
	tt := ui.NewTester(a.view, 1100, 720)
	defer retireTester(tt)
	tt.Frame()
	hunks := tab.diff.Hunks()
	// The new side's text, the last text field of the diff.
	var newSide ui.AccessInfo
	for _, n := range tt.AccessAll() {
		if n.Role == ui.RoleTextField && n.Bounds.W > 100 && n.Bounds.X > newSide.Bounds.X {
			newSide = n
		}
	}
	if newSide.Bounds.W == 0 {
		t.Fatal("找不到 diff 文本")
	}
	tt.ClickAt(newSide.Bounds.X+newSide.Bounds.W/2, newSide.Bounds.Y+8)
	tt.Frame()
	if tab.diff.Cursor() == 0 {
		t.Fatal("点进 diff 没有产生光标")
	}
	before := tab.diff.CursorPos().Line
	want := hunks[0].NewStart
	for _, h := range hunks {
		if h.NewStart > before {
			want = h.NewStart
			break
		}
	}
	tt.Key(0, ui.KeyJ)
	tt.Frame()
	if got := tab.diff.CursorPos().Line; got != want {
		t.Fatalf("J 应从第 %d 行跳到第 %d 行，停在 %d", before, want, got)
	}
	next := hunks[0].NewStart
	for _, h := range hunks {
		if h.NewStart > want {
			next = h.NewStart
			break
		}
	}
	tt.Key(0, ui.KeyJ)
	tt.Frame()
	if got := tab.diff.CursorPos().Line; got != next {
		t.Fatalf("第二个 J 应跳到第 %d 行，停在 %d", next, got)
	}
	tt.Key(0, ui.KeyK)
	tt.Frame()
	if got := tab.diff.CursorPos().Line; got != want {
		t.Fatalf("K 应回到第 %d 行，停在 %d", want, got)
	}
}
