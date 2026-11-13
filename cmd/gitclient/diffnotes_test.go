package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// demoDiff 是两份只差一行的文本，笔记落在第 4 行（新侧）。
func demoDiff() *editor.DiffState {
	return &editor.DiffState{
		Old: editor.NewBuffer("alpha\nbravo\ncharlie\ndelta\necho\nfoxtrot\ngolf\nhotel\n"),
		New: editor.NewBuffer("alpha\nbravo\ncharlie\nDELTA\necho\nfoxtrot\ngolf\nhotel\n"),
	}
}

func TestRenderNotesMarkdownSnapshot(t *testing.T) {
	notes := []diffNote{{Path: "app/demo.txt", Side: "new", Line: 4, Text: "这个名字和下面的引用对不上"}}
	got := renderNotesMarkdown(demoDiff(), notes, "app/demo.txt")
	want := "## app/demo.txt\n" +
		"\n" +
		"### L4 (新)\n" +
		"\n" +
		"```diff\n" +
		" alpha\n" +
		" bravo\n" +
		" charlie\n" +
		"+DELTA\n" +
		" echo\n" +
		" foxtrot\n" +
		" golf\n" +
		"```\n" +
		"\n" +
		"> 这个名字和下面的引用对不上\n"
	if got != want {
		t.Fatalf("导出的 Markdown 不对:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderNotesMarkdownWithoutContext 导出别的文件的笔记：手上没有它的
// diff，就只写路径、行号和正文，不编造上下文。
func TestRenderNotesMarkdownWithoutContext(t *testing.T) {
	notes := []diffNote{
		{Path: "b.go", Side: "old", Line: 3, Text: "第一行\n第二行"},
		{Path: "b.go", Side: "new", Line: 9, Text: "新侧"},
	}
	got := renderNotesMarkdown(nil, notes, "b.go")
	want := "## b.go\n" +
		"\n" +
		"### L3 (旧)\n" +
		"\n" +
		"> 第一行\n" +
		"> 第二行\n" +
		"\n" +
		"### L9 (新)\n" +
		"\n" +
		"> 新侧\n"
	if got != want {
		t.Fatalf("没有上下文时导出的 Markdown 不对:\n%s", got)
	}
}

func TestPrepareDiffStateWiresNotes(t *testing.T) {
	tab := reviewTestTab("a.go")
	tab.diff = &editor.DiffState{Old: editor.NewBuffer("a\n"), New: editor.NewBuffer("b\n")}
	tab.diffNotes = []diffNote{{Path: "a.go", Side: "new", Line: 1, Text: "改了这一行"}}
	a := &app{tabs: []*repoTab{tab}}

	a.prepareDiffState(tab.diff)
	if !tab.diff.LineSelection || tab.diff.OnGutterButton == nil || tab.diff.RenderAnnotation == nil {
		t.Fatal("笔记没接到 LineSelection / OnGutterButton / RenderAnnotation 上")
	}
	if len(tab.diff.Annotations) != 1 {
		t.Fatalf("应有一条注释，实为 %d 条", len(tab.diff.Annotations))
	}
	ann := tab.diff.Annotations[0]
	if ann.Side != editor.DiffNew || ann.Line != 0 || ann.Height != noteAnnotationHeight {
		t.Fatalf("注释挂错了位置: %+v", ann)
	}

	tab.diff.OnGutterButton(editor.DiffLineRange{Side: editor.DiffNew, Start: 0, End: 0})
	if tab.noteEdit == nil {
		t.Fatal("点行号加号没有打开输入条")
	}
	if tab.noteEdit.Path != "a.go" || tab.noteEdit.Side != "new" || tab.noteEdit.Line != 1 {
		t.Fatalf("输入条锚错了行: %+v", tab.noteEdit)
	}

	// 文件标签页的 diff 共用这个入口，但笔记不装在它上面。
	other := &editor.DiffState{Old: editor.NewBuffer("a\n"), New: editor.NewBuffer("c\n")}
	a.prepareDiffState(other)
	if other.OnGutterButton != nil || other.LineSelection || len(other.Annotations) != 0 {
		t.Fatal("详情面板之外的 diff 不该挂笔记")
	}
}

func TestNoteInputBarSavesAndCancels(t *testing.T) {
	tab := reviewTestTab("a.go")
	tab.diff = demoDiff()
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	defer tab.cancel()
	tt := ui.NewTester(func(c *ui.Context) { a.fileDiffPane(c, tab) }, 900, 500)
	defer retireTester(tt)
	tt.Frame()

	tab.noteEdit = &diffNote{Path: "a.go", Side: "new", Line: 4}
	tt.Frame()
	tt.Type("改个名字")
	tt.Frame()
	tt.Key(commandModifier(), ui.KeyEnter)
	tt.Frame()
	if len(tab.diffNotes) != 1 || tab.diffNotes[0].Text != "改个名字" {
		t.Fatalf("⌘↩ 没有保存笔记: %+v", tab.diffNotes)
	}
	if tab.noteEdit != nil {
		t.Fatal("保存后输入条该关掉")
	}
	if !tt.HasText("改个名字") {
		t.Fatalf("保存的笔记没有画在行下: %q", tt.Texts())
	}

	// Esc 取消，草稿不落库。
	tab.noteEdit = &diffNote{Path: "a.go", Side: "new", Line: 2}
	tt.Frame()
	tt.Type("不要了")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tab.noteEdit != nil {
		t.Fatal("Esc 没有关掉输入条")
	}
	if len(tab.diffNotes) != 1 {
		t.Fatalf("Esc 不该写入笔记: %+v", tab.diffNotes)
	}
}
