package main

import "slices"

// seg 是提交行里的一段图谱线。top/bot 表示线从行顶还是行中开始，到行中还是行底结束。
type seg struct {
	a, b  int
	top   bool
	bot   bool
	color int
	bend  uint8 // 1：S 曲线的下半行，2：下一行的上半行。
}

type graphBend struct {
	a, b, color int
	joins       bool
}

// graph 按 git log 从新到旧的顺序增量排泳道。
// 已排过的提交不会因为后面追加新页而改列，这样分页加载时已经画出的线不跳动。
type graph struct {
	cols           [][20]byte
	colors         []int
	bends          []graphBend
	maxLanes       int
	incoming       [][20]byte
	incomingColors []int
	scratch        []seg
	previous       []seg
	usedColors     []bool
}

func (g *graph) add(c *commit) {
	g.incoming = append(g.incoming[:0], g.cols...)
	g.incomingColors = append(g.incomingColors[:0], g.colors...)
	incoming, incomingColors := g.incoming, g.incomingColors
	node := -1
	for i, h := range incoming {
		if h == c.hash {
			node = i
			break
		}
	}
	if node < 0 {
		node = g.alloc()
		g.colors[node] = g.freeColor(-1)
	}
	nodeColor := g.colors[node]
	for i, h := range g.cols {
		if h == c.hash {
			g.cols[i] = zeroHash
		}
	}

	parentCols := make([]int, len(c.parents))
	parentJoins := make([]bool, len(c.parents))
	for i, p := range c.parents {
		parentCols[i] = -1
		if p == zeroHash {
			continue
		}
		if j := indexHash(g.cols, p); j >= 0 {
			parentCols[i] = j
			parentJoins[i] = true
			continue
		}
		dest := node
		if i != 0 || g.cols[node] != zeroHash {
			dest = g.alloc()
		}
		if i == 0 {
			g.colors[dest] = nodeColor
		} else {
			g.colors[dest] = g.freeColor(nodeColor)
		}
		g.cols[dest] = p
		parentCols[i] = dest
	}
	if len(g.cols) > g.maxLanes {
		g.maxLanes = len(g.cols)
	}
	if node+1 > g.maxLanes {
		g.maxLanes = node + 1
	}

	segs := g.scratch[:0]
	for _, bend := range g.bends {
		segs = append(segs, seg{a: bend.a, b: bend.b, top: true, color: bend.color, bend: 2})
	}
	for i, h := range incoming {
		if h == zeroHash {
			continue
		}
		top := true
		for _, bend := range g.bends {
			if bend.b == i && !bend.joins {
				top = false
				break
			}
		}
		if i != node || top {
			segs = append(segs, seg{a: i, b: i, top: top, bot: i != node, color: incomingColors[i]})
		}
	}
	g.bends = nil
	for i, pc := range parentCols {
		if pc < 0 {
			continue
		}
		color := g.colors[pc]
		if i == 0 {
			// 第一父链汇回已有链前，连线仍属于当前链。
			color = nodeColor
		}
		s := seg{a: node, b: pc, bot: true, color: color}
		if node != pc {
			s.bend = 1
			g.bends = append(g.bends, graphBend{a: node, b: pc, color: color, joins: parentJoins[i]})
		}
		segs = append(segs, s)
	}
	c.nodeCol = node
	c.nodeColor = nodeColor
	// Consecutive rows often have identical through lanes. Keep their geometry once.
	if !slices.Equal(segs, g.previous) {
		g.previous = slices.Clone(segs)
	}
	c.segs = g.previous
	g.scratch = segs
	for len(g.cols) > 0 && g.cols[len(g.cols)-1] == zeroHash {
		g.cols = g.cols[:len(g.cols)-1]
		g.colors = g.colors[:len(g.colors)-1]
	}
}

func (g graph) clone() graph {
	g.cols = slices.Clone(g.cols)
	g.colors = slices.Clone(g.colors)
	g.bends = slices.Clone(g.bends)
	g.incoming, g.incomingColors, g.scratch, g.usedColors = nil, nil, nil, nil
	return g
}

func (g *graph) alloc() int {
	for i, h := range g.cols {
		if h == zeroHash {
			return i
		}
	}
	g.cols = append(g.cols, zeroHash)
	g.colors = append(g.colors, 0)
	return len(g.cols) - 1
}

// 颜色跟随第一父链；新链从未被在途链占用的颜色中分配，与物理列无关。
func (g *graph) freeColor(nodeColor int) int {
	n := len(g.cols) + 2
	g.usedColors = slices.Grow(g.usedColors, max(0, n-len(g.usedColors)))[:n]
	clear(g.usedColors)
	for i, h := range g.cols {
		if h != zeroHash && g.colors[i] < len(g.usedColors) {
			g.usedColors[g.colors[i]] = true
		}
	}
	for color := 0; ; color++ {
		if color == nodeColor {
			continue
		}
		if color >= len(g.usedColors) || !g.usedColors[color] {
			return color
		}
	}
}

func (g *graph) lanes() int {
	if g.maxLanes < 1 {
		return 1
	}
	return g.maxLanes
}

func indexHash(cols [][20]byte, h [20]byte) int {
	for i, c := range cols {
		if c == h {
			return i
		}
	}
	return -1
}
