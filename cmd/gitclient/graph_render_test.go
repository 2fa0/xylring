package main

import (
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGraphLinesMeetAcrossRows(t *testing.T) {
	tab := newTab(1, "/tmp/graph-gap")
	tab.loaded = true
	tab.branch = "main"
	tab.options = defaultViewSettings()
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
	tab.selected, tab.commitApplied = 0, 0
	a := &app{split: 40, filePane: 200, tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 900, 500)
	tt.Frame()
	img := tt.Image()
	if os.Getenv("GRAPH_PNG") != "" {
		f, err := os.Create(os.Getenv("GRAPH_PNG"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, img)
		f.Close()
	}
	lanes := continuousLanes(img)
	if len(lanes) < 2 {
		t.Fatalf("只看到 %d 条连续泳道，分叉可能被裁掉或没接上：%v", len(lanes), lanes)
	}
	for _, lane := range lanes {
		if lane.gap > 2 {
			t.Fatalf("泳道 x=%d 断开 %d 像素（跨度 %d）", lane.x, lane.gap, lane.span)
		}
	}
}

func TestCanonicalGraphColorsKeepsCustomPalette(t *testing.T) {
	if canonicalGraphColors(graphColorLegacy) != graphColorDefault {
		t.Fatal("旧默认色没有迁移")
	}
	custom := "#112233,#445566"
	if canonicalGraphColors(custom) != custom {
		t.Fatal("自定义颜色被覆盖")
	}
}

type laneRun struct{ x, span, gap int }

func continuousLanes(img image.Image) []laneRun {
	return laneRuns(img, 40)
}

func laneRuns(img image.Image, minSpan int) []laneRun {
	b := img.Bounds()
	ink := func(x, y int) bool {
		r, g, bl, a := img.At(x, y).RGBA()
		if a < 0x8000 {
			return false
		}
		maxc, minc := r, r
		for _, c := range []uint32{g, bl} {
			if c > maxc {
				maxc = c
			}
			if c < minc {
				minc = c
			}
		}
		return maxc-minc > 0x1800 && maxc > 0x2000
	}
	limit := b.Min.X + 360
	if limit > b.Max.X {
		limit = b.Max.X
	}
	var runs []laneRun
	for px := b.Min.X; px < limit; px++ {
		first, last := -1, -1
		for y := b.Min.Y; y < b.Max.Y; y++ {
			if !ink(px, y) {
				continue
			}
			if first < 0 {
				first = y
			}
			last = y
		}
		gap, run, inked := 0, 0, 0
		for y := first; y <= last; y++ {
			if ink(px, y) {
				inked++
				run = 0
				continue
			}
			run++
			if run > gap {
				gap = run
			}
		}
		if last-first < minSpan || gap > 2 || inked*2 < last-first {
			continue
		}
		runs = append(runs, laneRun{px, last - first, gap})
	}
	var lanes []laneRun
	for _, run := range runs {
		if len(lanes) == 0 || run.x > lanes[len(lanes)-1].x+3 {
			lanes = append(lanes, run)
			continue
		}
		if run.span > lanes[len(lanes)-1].span {
			lanes[len(lanes)-1] = run
		}
	}
	return lanes
}
