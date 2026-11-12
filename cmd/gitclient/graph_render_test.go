package main

import (
	"fmt"
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGraphLinesMeetAcrossRows(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			testGraphLinesMeetAcrossRows(t, dark)
		})
	}
}

func testGraphLinesMeetAcrossRows(t *testing.T, dark bool) {
	tab := newTab(1, "/tmp/graph-gap")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2), hashN(3)}, subject: "merge", author: "A", when: 1700000000},
		{hash: hashN(2), parents: [][20]byte{hashN(4)}, subject: "left", author: "A", when: 1700000000},
		{hash: hashN(3), parents: [][20]byte{hashN(4)}, subject: "right", author: "A", when: 1700000000},
		{hash: hashN(4), subject: "base", author: "A", when: 1700000000},
	}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.head = tab.commits[0].hash
	tab.selected = 0
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 900, 500)
	tt.SetDark(dark)
	tt.Frame()
	img := tt.Image()
	if path := os.Getenv("GRAPH_PNG"); path != "" {
		if dark {
			path += ".dark.png"
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, img)
		f.Close()
	}
	header, ok := tt.Find("图谱")
	if !ok {
		t.Fatal("图谱列没有显示")
	}
	for i := 0; i < len(tab.commits)-1; i++ {
		row, _ := tt.Find(tab.commits[i].subject)
		next, _ := tt.Find(tab.commits[i+1].subject)
		boundary := int((row.Y + row.H/2 + next.Y + next.H/2) / 2)
		for _, s := range tab.commits[i].segs {
			if !s.bot {
				continue
			}
			x := header.X + laneGap/2 + float32(s.a)*laneGap
			if s.bend == 1 {
				x += float32(s.b-s.a) * laneGap / 2
			}
			for y := boundary - 1; y <= boundary; y++ {
				ink := false
				for px := int(x) - 2; px <= int(x)+2; px++ {
					r, g, b, _ := img.At(px, y).RGBA()
					ink = ink || max(r, g, b)-min(r, g, b) > 0x1800
				}
				if !ink {
					t.Fatalf("第 %d 行泳道 %d→%d 在行边界 y=%d 断开", i, s.a, s.b, y)
				}
			}
		}
	}
}

func TestGraphLaneBendsAreSmooth(t *testing.T) {
	for _, span := range []int{1, 3} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("lanes=%d/reverse=%t", span, reverse), func(t *testing.T) {
				a, b := 0, span
				if reverse {
					a, b = b, a
				}
				tt := ui.NewTester(func(c *ui.Context) {
					ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
						p.Fill(r, ui.RGB(255, 255, 255), 0)
						for half := uint8(1); half <= 2; half++ {
							row := ui.Rect{Y: float32(half-1) * 40, W: r.W, H: 40}
							path := lanePath(row, seg{a: a, b: b, bend: half})
							p.Clip(row, 0, func() { p.StrokePath(&path, graphStroke, ui.RGB(0, 0, 0)) })
						}
					})
				}, 110, 80)
				tt.Frame()
				img := tt.Image()
				ink := func(x, y int) bool {
					r, _, _, _ := img.At(x, y).RGBA()
					return r < 0x8000
				}
				x0, x1 := laneCenter(ui.Rect{}, a), laneCenter(ui.Rect{}, b)
				if ink(int(x0), 40) || ink(int(x1), 40) {
					t.Fatal("转弯仍经过直角顶点")
				}
				if !ink(int(x0), 21) || !ink(int(x1), 58) {
					t.Fatal("曲线没有沿泳道竖直进出")
				}
				for y := 21; y < 60; y++ {
					pixels := 0
					for x := int(min(x0, x1)) - 2; x <= int(max(x0, x1))+2; x++ {
						if ink(x, y) {
							pixels++
						}
					}
					if pixels == 0 || pixels > 10 {
						t.Fatalf("y=%d 有 %d 个线条像素，曲线断开或出现水平段", y, pixels)
					}
				}
			})
		}
	}
}

func TestGraphReleaseRendering(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			tab := newTab(1, "/tmp/release-graph")
			tab.loaded, tab.branch = true, "master"
			tab.commits = releaseHistory()
			tab.refs = map[[20]byte][]refInfo{}
			for i := range tab.commits {
				tab.commits[i].author = "Ada"
				tab.commits[i].when = 1700000000
				tab.graph.add(&tab.commits[i])
				if i%3 == 0 && i < 12 {
					tab.refs[tab.commits[i].hash] = []refInfo{{name: fmt.Sprintf("v1-%d", i/3+1), kind: refTag}}
				}
			}
			tab.head = tab.commits[0].hash
			a := &app{tabs: []*repoTab{tab}}
			tt := ui.NewTester(a.view, 1200, 760)
			tt.SetDark(dark)
			tt.Frame()
			if !tt.HasText("标签 v1-1") || tt.HasText("🏷 v1-1") {
				t.Fatal("标签徽章应使用文字而不是图标")
			}
			if path := os.Getenv("GRAPH_PNG"); path != "" {
				f, err := os.Create(fmt.Sprintf("%s.releases-dark-%t.png", path, dark))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, tt.Image()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestGraphCrossingsAndNodeHalos(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			var background ui.Color
			tt := ui.NewTester(func(c *ui.Context) {
				background = c.Theme().Background
				ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
					p.Fill(r, background, 0)
					for half := uint8(1); half <= 2; half++ {
						row := ui.Rect{Y: float32(half-1) * 40, W: r.W, H: 40}
						item := commit{nodeCol: 0, segs: []seg{
							{a: 1, b: 1, color: 1, top: true, bot: true},
							{a: 0, b: 2, color: 2, bend: half},
						}}
						p.Clip(row, 0, func() { paintCommit(p, row, &item, false, nil, background) })
					}
					for i, radius := range []float32{4.5, 5.5, 4.5} {
						x := float32(20 + i*30)
						p.Line(x, 85, x, 115, graphStroke, laneColor(0))
						paintGraphNode(p, x, 100, radius, laneColor(0), background, i == 2)
					}
				})
			}, 110, 120)
			tt.SetDark(dark)
			tt.Frame()
			img := tt.Image()
			near := func(x, y int, color ui.Color) bool {
				r, g, b, _ := img.At(x, y).RGBA()
				delta := func(a, b int) int { return max(a-b, b-a) }
				return delta(int(r>>8), int(color.R)) < 25 && delta(int(g>>8), int(color.G)) < 25 && delta(int(b>>8), int(color.B)) < 25
			}
			x := int(laneCenter(ui.Rect{}, 1))
			if !near(x, 40, laneColor(2)) || !near(x, 25, laneColor(1)) {
				t.Fatal("短曲线没有从长泳道上方跨过")
			}
			if !near(x, 42, background) {
				t.Fatal("交叉处没有背景色衬底")
			}
			if !near(23, 100, laneColor(0)) || !near(54, 100, laneColor(0)) {
				t.Fatal("普通节点或引用节点的半径不足")
			}
			if !near(20, 105, background) || !near(50, 106, background) {
				t.Fatal("节点没有背景色外圈")
			}
			if !near(80, 100, background) || !near(83, 100, laneColor(0)) {
				t.Fatal("合并节点没有空心描边")
			}
		})
	}
}

func TestGraphSidebarClosedSpacing(t *testing.T) {
	tab := newTab(1, "/tmp/graph-closed-gap")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), subject: "commit-spacing-check", author: "A", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.head = tab.commits[0].hash

	a := &app{
		tabs:          []*repoTab{tab},
		sidebarHidden: true,
	}

	tt := ui.NewTester(a.view, 900, 500)
	tt.Frame()
	defer retireTester(tt)

	header, ok := tt.Find("图谱")
	if !ok {
		t.Fatal("未找到图谱列头")
	}
	if header.X < 12 {
		t.Fatalf("关闭侧栏后图谱列过于靠近边框: X = %v, want >= 12", header.X)
	}

	row, ok := tt.Find("commit-spacing-check")
	if !ok {
		t.Fatal("未找到提交行")
	}
	if row.X < 12 {
		t.Fatalf("关闭侧栏后提交行过于靠近边框: X = %v, want >= 12", row.X)
	}
}
