package main

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
)

func parsePalette(s string) ([]ui.Color, error) {
	var colors []ui.Color
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "#")
		b, err := hex.DecodeString(part)
		if err != nil || len(b) != 3 {
			return nil, fmt.Errorf("图谱颜色须为逗号分隔的 #RRGGBB")
		}
		colors = append(colors, ui.RGBA(b[0], b[1], b[2], 1))
	}
	if len(colors) == 0 {
		return nil, fmt.Errorf("没有可用的图谱颜色")
	}
	return colors, nil
}

func (a *app) commitColumns(c *ui.Context, tab *repoTab) []ui.TableColumn {
	winW, _ := c.Size()
	mainW := winW
	if a.showRepoSidebar() {
		split := a.repoSplit
		if split <= 0 {
			split = 320
		}
		mainW -= split
	}
	if mainW > 0 && mainW < 700 {
		if a.refsInline {
			return []ui.TableColumn{
				{ID: "graph", Title: tr("图谱"), Width: graphWidth(tab.graph.lanes()), MinWidth: 56},
				{ID: "subject", Title: tr("描述 / 引用"), MinWidth: 160},
				{ID: "date", Title: tr("日期"), Width: 128, MinWidth: 100},
				{ID: "author", Title: tr("作者"), Width: 96, MinWidth: 60},
				{ID: "hash", Title: tr("哈希"), Width: 84, MinWidth: 70},
			}
		}
		return []ui.TableColumn{
			{ID: "refs", Title: tr("引用"), Width: 132, MinWidth: 128},
			{ID: "graph", Title: tr("图谱"), Width: graphWidth(tab.graph.lanes()), MinWidth: 56},
			{ID: "subject", Title: tr("描述"), MinWidth: 116},
			{ID: "date", Title: tr("日期"), Width: 112, MinWidth: 92},
			{ID: "author", Title: tr("作者"), Width: 72, MinWidth: 56},
			{ID: "hash", Title: tr("哈希"), Width: 72, MinWidth: 64},
		}
	}
	if a.refsInline {
		return []ui.TableColumn{
			{ID: "graph", Title: tr("图谱"), Width: graphWidth(tab.graph.lanes()), MinWidth: 56},
			{ID: "subject", Title: tr("描述 / 引用"), MinWidth: 200},
			{ID: "date", Title: tr("日期"), Width: 172, MinWidth: 128},
			{ID: "author", Title: tr("作者"), Width: 156, MinWidth: 96},
			{ID: "hash", Title: tr("哈希"), Width: 120, MinWidth: 96},
		}
	}
	return []ui.TableColumn{
		{ID: "refs", Title: tr("引用"), Width: 168, MinWidth: 128},
		{ID: "graph", Title: tr("图谱"), Width: graphWidth(tab.graph.lanes()), MinWidth: 56},
		{ID: "subject", Title: tr("描述"), MinWidth: 160},
		{ID: "date", Title: tr("日期"), Width: 144, MinWidth: 116},
		{ID: "author", Title: tr("作者"), Width: 108, MinWidth: 80},
		{ID: "hash", Title: tr("哈希"), Width: 84, MinWidth: 72},
	}
}

func graphWidth(lanes int) float32 {
	if lanes < 1 {
		lanes = 1
	}
	// 至少留够表头「图谱」两个字的宽度，否则单泳道仓库的表头只剩省略号。
	return max(lanePad*2+float32(lanes)*laneGap, 56)
}

func laneCenter(r ui.Rect, col int) float32 {
	return r.X + lanePad + float32(col)*laneGap + laneGap/2
}

func laneColor(col int) ui.Color {
	if col < 0 {
		col = 0
	}
	return laneColors[col%len(laneColors)]
}

func graphColor(chain int, palette []ui.Color) ui.Color {
	if len(palette) > 0 {
		return palette[max(0, chain)%len(palette)]
	}
	return laneColor(chain)
}

func paintCommit(p *ui.Painter, r ui.Rect, c *commit, contFromTop bool, palette []ui.Color, background ui.Color, inferred ...bool) {
	mid := r.Y + r.H/2
	// 引导点线先画，后面的泳道会压在它上面。
	paintGuide(p, r, laneCenter(r, c.nodeCol), commitRadius(c)+3, palette, c.nodeColor)
	// 长泳道在下，短曲线带背景色衬底跨过泳道，最后再画节点。
	for _, bends := range []bool{false, true} {
		for _, s := range c.segs {
			if (s.bend != 0) != bends {
				continue
			}
			path := lanePath(r, s)
			color, width := graphColor(s.color, palette), float32(graphStroke)
			if s.dash {
				color, width = color.Alpha(0.5), 1.6
			}
			if bends {
				p.StrokePath(&path, graphStroke+3, background)
			}
			p.StrokePath(&path, width, color)
		}
	}
	if contFromTop {
		x := laneCenter(r, c.nodeCol)
		p.Line(x, r.Y-1, x, mid, graphStroke, graphColor(c.nodeColor, palette))
	}
	radius := commitRadius(c)
	if c.prov != 0 {
		var ring ui.Path
		x, y, rr := laneCenter(r, c.nodeCol), mid, min(radius+2.5, maxRingRadius)
		if len(inferred) > 0 && inferred[0] {
			for i := 0; i < 12; i++ {
				for j := 0; j <= 4; j++ {
					angle := (float64(i) + float64(j)/8) * math.Pi / 6
					px, py := x+rr*float32(math.Cos(angle)), y+rr*float32(math.Sin(angle))
					if j == 0 {
						ring.MoveTo(px, py)
					} else {
						ring.LineTo(px, py)
					}
				}
			}
		} else {
			ring.Circle(x, y, rr)
		}
		p.StrokePath(&ring, 1.2, graphColor(c.nodeColor, palette).Alpha(0.45))
	}

	paintGraphNode(p, laneCenter(r, c.nodeCol), mid, radius, graphColor(c.nodeColor, palette), background, len(c.parents) > 1)
}

// commitRadius 用新增+删除的行数表示改动量。
// 合并提交和文件历史拿不到 shortstat，用中间档的基准大小。
func commitRadius(c *commit) float32 {
	switch {
	case c.lines < 0:
		return 4
	case c.lines == 0:
		return 3
	case c.lines <= 10:
		return 3.5
	case c.lines <= 50:
		return 4
	case c.lines <= 200:
		return 4.5
	case c.lines <= 1000:
		return 5
	default:
		return 6
	}
}

// 每次换道用一个行高的三次贝塞尔。两行分别画下半段和上半段，
// 行边界处位置和切线相同，节点处则沿泳道竖直进出。
func lanePath(r ui.Rect, s seg) ui.Path {
	x0, x1 := laneCenter(r, s.a), laneCenter(r, s.b)
	mid, bottom := r.Y+r.H/2, r.Y+r.H
	var path ui.Path
	if s.dash {
		path.MoveTo(x0, mid)
		path.LineTo(x0, bottom-5)
		path.Dash(0, 2, 3)
		return path
	}
	switch s.bend {
	case 1:
		path.MoveTo(x0, mid)
		path.CubeTo(x0, mid+r.H/4, x0+(x1-x0)/4, bottom-r.H/8, (x0+x1)/2, bottom)
	case 2:
		path.MoveTo((x0+x1)/2, r.Y)
		path.CubeTo(x1-(x1-x0)/4, r.Y+r.H/8, x1, r.Y+r.H/4, x1, mid)
	case 3:
		y0 := mid
		if s.top {
			y0 = r.Y
		}
		path.MoveTo(x0, y0)
		path.CubeTo(x0, mid, (x0+x1)/2, mid, x1, mid)
	default:
		y0, y1 := mid, mid
		if s.top {
			y0 = r.Y - 1
		}
		if s.bot {
			y1 = bottom + 1
		}
		path.MoveTo(x0, y0).LineTo(x1, y1)
	}
	return path
}

func paintWorktreeCommit(p *ui.Painter, r ui.Rect, c *commit, background ui.Color) {
	mid := r.Y + r.H/2
	x := laneCenter(r, c.nodeCol)
	color := graphColor(c.nodeColor, nil)
	paintGuide(p, r, x, 6, nil, c.nodeColor)
	if len(c.segs) > 0 && c.segs[0].bot {
		p.Line(x, mid, x, r.Y+r.H+1, graphStroke, color)
	}
	paintWorktreeNode(p, x, mid, 5, color, background)
}

// paintGuide 从节点往右拉一条淡点线一直到图谱列末尾。
// 泳道多的时候，一眼就能把这条提交和它的标题连起来。
func paintGuide(p *ui.Painter, r ui.Rect, nodeX, gap float32, palette []ui.Color, color int) {
	paintGuideSpan(p, nodeX+gap, r.X+r.W-1, r.Y+r.H/2, palette, color)
}

func paintGuideLeft(p *ui.Painter, r ui.Rect, nodeX, gap float32, palette []ui.Color, color int) {
	paintGuideSpan(p, r.X+1, nodeX-gap, r.Y+r.H/2, palette, color)
}

func paintGuideSpan(p *ui.Painter, x0, x1, y float32, palette []ui.Color, color int) {
	if x1-x0 < 6 {
		return
	}
	var path ui.Path
	path.MoveTo(x0, y)
	path.LineTo(x1, y)
	path.Dash(0, 1.5, 3)
	p.StrokePath(&path, 1, graphColor(color, palette).Alpha(0.35))
}

func commitHasRefs(tab *repoTab, item *commit) bool {
	if item.hash == tab.head && tab.head != zeroHash {
		return true
	}
	return len(groupCommitRefs(tab.refs[item.hash], tab.query.hideRemote)) > 0
}

func paintWorktreeNode(p *ui.Painter, x, y, radius float32, color, background ui.Color) {
	var halo ui.Path
	halo.Circle(x, y, radius+2)
	p.FillPath(&halo, background)
	size := radius * 1.65
	p.Fill(ui.Rect{X: x - size/2, Y: y - size/2, W: size, H: size}, color, 2)
}

func paintGraphNode(p *ui.Painter, x, y, radius float32, color, background ui.Color, hollow bool) {
	var halo ui.Path
	halo.Circle(x, y, radius+2)
	p.FillPath(&halo, background)
	var node ui.Path
	if hollow {
		node.Circle(x, y, radius-1)
		p.StrokePath(&node, 2, color)
	} else {
		node.Circle(x, y, radius)
		p.FillPath(&node, color)
	}
}
