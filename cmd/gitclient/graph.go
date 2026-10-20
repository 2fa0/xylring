package main

// seg 是提交行里的一段图谱线。top/bot 表示线从行顶还是行中开始，到行中还是行底结束。
type seg struct {
	a, b int
	top  bool
	bot  bool
}

// graph 按 git log 从新到旧的顺序增量排泳道。
// 已排过的提交不会因为后面追加新页而改列，这样分页加载时已经画出的线不跳动。
type graph struct {
	cols     [][20]byte
	maxLanes int
}

func (g *graph) add(c *commit) {
	incoming := append([][20]byte(nil), g.cols...)
	node := -1
	for i, h := range incoming {
		if h == c.hash {
			node = i
			break
		}
	}
	if node < 0 {
		node = g.alloc()
	}
	for i, h := range g.cols {
		if h == c.hash {
			g.cols[i] = zeroHash
		}
	}

	parentCols := make([]int, len(c.parents))
	for i, p := range c.parents {
		parentCols[i] = -1
		if p == zeroHash {
			continue
		}
		if j := indexHash(g.cols, p); j >= 0 {
			parentCols[i] = j
			continue
		}
		dest := node
		if i != 0 || g.cols[node] != zeroHash {
			dest = g.alloc()
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

	var segs []seg
	for i, h := range incoming {
		if h == zeroHash {
			continue
		}
		if i == node {
			segs = append(segs, seg{a: i, b: i, top: true, bot: false})
			continue
		}
		segs = append(segs, seg{a: i, b: i, top: true, bot: true})
	}
	for _, pc := range parentCols {
		if pc < 0 {
			continue
		}
		segs = append(segs, seg{a: node, b: pc, top: false, bot: true})
	}
	c.nodeCol = node
	c.segs = segs
}

func (g *graph) alloc() int {
	for i, h := range g.cols {
		if h == zeroHash {
			return i
		}
	}
	g.cols = append(g.cols, zeroHash)
	return len(g.cols) - 1
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
