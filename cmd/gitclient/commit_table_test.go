package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func commitTableFixture() (*app, *repoTab) {
	tab := newTab(1, "/tmp/commit-table")
	tab.loaded = true
	tab.options = defaultViewSettings()
	tab.commits = []commit{
		{hash: hashN(1), subject: "Merge branch feature into main", author: "Ada", when: 1700000000},
		{hash: hashN(2), subject: "merge branch dev into release", author: "Bob", when: 1700000001},
	}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.head = tab.commits[0].hash
	tab.refs = map[[20]byte][]refInfo{tab.head: {{name: "release", kind: refBranch}}}
	return &app{tabs: []*repoTab{tab}}, tab
}

func TestCommitTableSelectionText(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, search := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%t/search=%t", dark, search), func(t *testing.T) {
				a, tab := commitTableFixture()
				var theme ui.Theme
				tt := ui.NewTester(func(c *ui.Context) {
					theme = *c.Theme()
					ui.Column(c).Fill().Children(func() { a.commitPane(c) })
				}, 1100, 300)
				tt.SetDark(dark)
				if search {
					tab.searchText = "branch"
				}
				tab.selection.Add(tab.commits[0].hash)
				tab.selection.Add(tab.commits[1].hash)
				tt.Frame()
				if out := os.Getenv("P0_ARTIFACT_DIR"); out != "" {
					if err := os.MkdirAll(out, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(out, fmt.Sprintf("selection-dark-%t-search-%t.png", dark, search)))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
				}
				checkInk := func(label string, color ui.Color) {
					t.Helper()
					r, ok := tt.Find(label)
					if !ok {
						t.Fatalf("missing text %q", label)
					}
					img := tt.Image()
					pixels := 0
					for y := int(r.Y); y < int(r.Y+r.H); y++ {
						for x := int(r.X); x < int(r.X+r.W); x++ {
							p := img.RGBAAt(x, y)
							if absInt(int(p.R)-int(color.R)) < 20 && absInt(int(p.G)-int(color.G)) < 20 && absInt(int(p.B)-int(color.B)) < 20 {
								pixels++
							}
						}
					}
					if pixels < 5 {
						t.Errorf("text %q does not use readable color %v", label, color)
					}
				}
				for _, label := range []string{"HEAD", "release", tab.commits[0].subject, tab.commits[1].subject, "Ada", "Bob", tab.commits[0].whenText(), shortHash(tab.commits[0].hash)} {
					checkInk(label, theme.AccentText)
				}
				if search {
					r, _ := tt.Find(tab.commits[0].subject)
					p := tt.Image().RGBAAt(int(r.X+r.W-2), int(r.Y+1))
					if p.R != theme.Accent.R || p.G != theme.Accent.G || p.B != theme.Accent.B {
						t.Error("search highlighting covers the selected row background")
					}
				}
				tab.selection.Clear()
				tt.Frame()
				checkInk("Ada", theme.TextMuted)
			})
		}
	}
}

func absInt(v int) int {
	return max(v, -v)
}

func TestCommitTableColumnResize(t *testing.T) {
	a, tab := commitTableFixture()
	var pad float32
	tt := ui.NewTester(func(c *ui.Context) {
		pad = c.Theme().Space(2.5)
		ui.Column(c).Fill().Children(func() { a.commitPane(c) })
	}, 1100, 300)
	author, _ := tt.Find("作者")
	x, y := author.X-pad-2, author.Y+author.H/2
	tt.Move(x, y)
	if tt.Cursor() != ui.CursorResizeEW {
		t.Fatal("column edge does not show the resize cursor")
	}
	tt.Press(x, y)
	tt.Move(x+40, y)
	tt.Release(x+40, y)
	tt.Frame()
	if got := tab.list.Columns.Widths["date"]; got != 190 {
		t.Fatalf("dragging date column: got %v, want 190", got)
	}
	author, _ = tt.Find("作者")
	if author.X-pad-2 != x+40 {
		t.Fatal("header did not follow the resized column")
	}
	x += 40
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	tt.Frame()
	width := tab.list.Columns.Widths["date"]
	if width == 190 || width <= 0 {
		t.Fatalf("double-click did not fit the date column: %v", width)
	}
	before, _ := tt.Find("作者")
	tt.Frame()
	after, _ := tt.Find("作者")
	if before != after {
		t.Fatal("fitted column width was lost on the next frame")
	}
	tab.releaseHeavy()
	if tab.list.Columns.Widths["date"] != width {
		t.Fatal("column width was lost when releasing an inactive tab")
	}
}
