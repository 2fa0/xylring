package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func hashN(n byte) [20]byte {
	var h [20]byte
	h[19] = n
	if n == 0 {
		h[0] = 1
	}
	return h
}

func TestGraphLinear(t *testing.T) {
	var g graph
	cs := []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2)}},
		{hash: hashN(2), parents: [][20]byte{hashN(3)}},
		{hash: hashN(3)},
	}
	for i := range cs {
		g.add(&cs[i])
	}
	for i, c := range cs {
		if c.nodeCol != 0 {
			t.Fatalf("线性历史第 %d 条在列 %d", i, c.nodeCol)
		}
	}
	if g.lanes() != 1 {
		t.Fatalf("泳道 %d", g.lanes())
	}
}

func TestGraphMergeStable(t *testing.T) {
	var g graph
	cs := []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2), hashN(3)}},
		{hash: hashN(2), parents: [][20]byte{hashN(4)}},
		{hash: hashN(3), parents: [][20]byte{hashN(4)}},
		{hash: hashN(4)},
	}
	g.add(&cs[0])
	g.add(&cs[1])
	first := []int{cs[0].nodeCol, cs[1].nodeCol}
	g.add(&cs[2])
	g.add(&cs[3])
	if cs[0].nodeCol != first[0] || cs[1].nodeCol != first[1] {
		t.Fatalf("追加提交改变了已有列：%v -> %d %d", first, cs[0].nodeCol, cs[1].nodeCol)
	}
	want := []int{0, 0, 1, 0}
	for i, c := range cs {
		if c.nodeCol != want[i] {
			t.Fatalf("第 %d 条列 %d，想要 %d", i, c.nodeCol, want[i])
		}
	}
	if g.lanes() < 2 {
		t.Fatalf("合并后泳道 %d", g.lanes())
	}
}

func releaseHistory() []commit {
	return []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(4), hashN(2)}, subject: "Merge branch release-1 into master"},
		{hash: hashN(2), parents: [][20]byte{hashN(4), hashN(3)}, subject: "merge branch dev into release-1"},
		{hash: hashN(3), parents: [][20]byte{hashN(6)}, subject: "开发提交 1"},
		{hash: hashN(4), parents: [][20]byte{hashN(8), hashN(5)}, subject: "Merge branch release-2 into master"},
		{hash: hashN(5), parents: [][20]byte{hashN(8), hashN(6)}, subject: "merge branch dev into release-2"},
		{hash: hashN(6), parents: [][20]byte{hashN(10)}, subject: "开发提交 2"},
		{hash: hashN(8), parents: [][20]byte{hashN(12), hashN(9)}, subject: "Merge branch release-3 into master"},
		{hash: hashN(9), parents: [][20]byte{hashN(12), hashN(10)}, subject: "merge branch dev into release-3"},
		{hash: hashN(10), parents: [][20]byte{hashN(14)}, subject: "开发提交 3"},
		{hash: hashN(12), parents: [][20]byte{hashN(16), hashN(13)}, subject: "Merge branch release-4 into master"},
		{hash: hashN(13), parents: [][20]byte{hashN(16), hashN(14)}, subject: "merge branch dev into release-4"},
		{hash: hashN(14), parents: [][20]byte{hashN(16)}, subject: "开发提交 4"},
		{hash: hashN(16), subject: "初始提交"},
	}
}

func TestGraphChainColorsSurviveLaneReuse(t *testing.T) {
	var g graph
	cs := releaseHistory()
	for i := range cs {
		g.add(&cs[i])
	}
	if cs[1].nodeCol == cs[4].nodeCol {
		t.Fatal("测试没有覆盖 release 使用不同泳道的情况")
	}
	for i := 1; i < 12; i += 3 {
		if cs[i].nodeColor != cs[1].nodeColor {
			t.Fatalf("release 第 %d 次变色：%d → %d", i/3+1, cs[1].nodeColor, cs[i].nodeColor)
		}
		if cs[i+1].nodeColor != cs[2].nodeColor || cs[i].nodeColor == cs[i+1].nodeColor {
			t.Fatal("dev 没有保持独立的第一父链颜色")
		}
		if cs[i-1].nodeColor != cs[0].nodeColor {
			t.Fatal("master 颜色改变")
		}
	}
	if cs[1].nodeCol != cs[2].nodeCol || cs[1].nodeColor == cs[2].nodeColor {
		t.Fatal("dev 复用 release 泳道后继承了原来的颜色")
	}
}

func TestGraphBendsContinueAcrossPages(t *testing.T) {
	var g graph
	cs := releaseHistory()
	g.add(&cs[0])
	first := cs[0].segs[len(cs[0].segs)-1]
	if first.bend != 1 || len(g.bends) != 1 {
		t.Fatal("分页末尾没有保留下一行的半条曲线")
	}
	g.add(&cs[1])
	second := cs[1].segs[0]
	if second.bend != 2 || second.a != first.a || second.b != first.b || second.color != first.color {
		t.Fatal("新页没有继续上一页的曲线和颜色")
	}
}

// 左边的列空出来也不回收：新分支的泳道只落在自己的列或更右边，
// 这样不会为了省一列画出横跨半个图谱的长弯。
func TestGraphParentLanesNeverOpenLeftOfNode(t *testing.T) {
	var g graph
	cs := []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2), hashN(3)}},
		{hash: hashN(2)},
		{hash: hashN(3), parents: [][20]byte{hashN(4), hashN(5)}},
		{hash: hashN(4)},
		{hash: hashN(5)},
	}
	for i := range cs {
		g.add(&cs[i])
	}
	if cs[2].nodeCol != 1 {
		t.Fatalf("第三条提交在列 %d，想在 1", cs[2].nodeCol)
	}
	opened := false
	for _, s := range cs[2].segs {
		if s.bend != 1 {
			continue
		}
		opened = true
		if s.a != cs[2].nodeCol || s.b < s.a {
			t.Fatalf("新泳道从列 %d 开到了左边的列 %d", s.a, s.b)
		}
	}
	if !opened {
		t.Fatal("第二条父链没有开新泳道")
	}
}

// 过滤历史（搜索、单文件历史）里看不到父提交：只给虚线短桩，不占泳道，
// 否则每一行都会留下一条漏到列表底部的实线。
func TestGraphFilteredHistoryUsesBoundaryStubs(t *testing.T) {
	rows := []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2), hashN(3)}},
		{hash: hashN(2), parents: [][20]byte{hashN(4)}},
		{hash: hashN(3), parents: [][20]byte{hashN(4), hashN(9)}},
	}
	g := graph{}
	g.visible = visibleSet(rows)
	for i := range rows {
		g.add(&rows[i])
	}
	if rows[0].nodeCol != 0 || rows[1].nodeCol != 0 || rows[2].nodeCol != 1 {
		t.Fatalf("结果集里的父链没连上：%d %d %d", rows[0].nodeCol, rows[1].nodeCol, rows[2].nodeCol)
	}
	dashes := func(c commit) int {
		n := 0
		for _, s := range c.segs {
			if s.dash {
				n++
			}
		}
		return n
	}
	if dashes(rows[0]) != 0 {
		t.Fatal("父提交都在结果集里时不该画短桩")
	}
	// 父提交被过滤掉，而且节点下方没有接上实线：画一段短桩。
	if dashes(rows[1]) != 1 {
		t.Fatalf("缺失的父提交没有画虚线短桩：%d", dashes(rows[1]))
	}
	// 这一行的父链汇回左边的已有泳道，自己列下方空着，也补一段短桩。
	if dashes(rows[2]) != 1 {
		t.Fatalf("节点列下方空着时没有画短桩：%d", dashes(rows[2]))
	}
	for _, c := range rows {
		for _, s := range c.segs {
			if s.dash && (s.bot || s.bend != 0) {
				t.Fatal("虚线短桩不能延续到下一行")
			}
		}
	}
	// 短桩不占泳道：两条泳道全来自结果集内部的两条父链。
	if g.lanes() != 2 {
		t.Fatalf("过滤历史占了 %d 条泳道", g.lanes())
	}
}

// 搜索结果的一页里几乎全是孤立的提交：每行只有节点和一段短桩，列数不增长。
func TestGraphFilteredPageStaysFlat(t *testing.T) {
	var rows []commit
	for i := byte(1); i <= 6; i++ {
		rows = append(rows, commit{hash: hashN(i), parents: [][20]byte{hashN(100 + i)}})
	}
	g := graph{}
	g.visible = visibleSet(rows)
	for i := range rows {
		g.add(&rows[i])
	}
	for i, c := range rows {
		if c.nodeCol != 0 {
			t.Fatalf("第 %d 条提交在列 %d", i, c.nodeCol)
		}
		if len(c.segs) != 1 || !c.segs[0].dash {
			t.Fatalf("第 %d 条提交的线不是单段短桩：%+v", i, c.segs)
		}
	}
	if g.lanes() != 1 {
		t.Fatalf("过滤历史占了 %d 条泳道", g.lanes())
	}
}

func filteredFixRepo(t *testing.T) string {
	t.Helper()
	dir, git := initTestRepo(t)
	write := func(name, body string) {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
		git("add", name)
	}
	for _, step := range []struct{ file, msg string }{
		{"base.txt", "base"},
		{"a.txt", "fix one"},
		{"b.txt", "fix two"},
		{"c.txt", "fix three"},
		{"d.txt", "unrelated"},
		{"e.txt", "fix four"},
	} {
		write(step.file, step.msg+"\n")
		git("commit", "-m", step.msg)
	}
	return dir
}

func TestFilteredQueryKeepsGraphFlat(t *testing.T) {
	dir := filteredFixRepo(t)
	ctx := context.Background()
	tips, err := historyTips(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	q := historyQuery{term: "fix"}
	p := newHistoryPager(ctx, dir, q, tips, 0)
	defer stopPager(t, p)
	rows, _, err := p.next(ctx, 50, historyMemoryStep)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("匹配到 %d 条，想 4", len(rows))
	}
	prep, err := prepareHistory(ctx, rows, graph{}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	cs := prep.commits
	for i, c := range cs {
		if c.nodeCol != 0 {
			t.Fatalf("第 %d 条提交在列 %d", i, c.nodeCol)
		}
	}
	if prep.graph.lanes() != 1 {
		t.Fatalf("过滤历史占了 %d 条泳道", prep.graph.lanes())
	}
	solid := func(c commit) bool {
		for _, s := range c.segs {
			if s.bot && !s.dash {
				return true
			}
		}
		return false
	}
	stub := func(c commit) bool {
		for _, s := range c.segs {
			if s.dash {
				return true
			}
		}
		return false
	}
	// 从新到旧：fix four（父提交未匹配）、fix three、fix two、fix one（父提交未匹配）。
	if solid(cs[0]) || !stub(cs[0]) {
		t.Fatalf("父提交未匹配时应该只有虚线短桩：%+v", cs[0].segs)
	}
	if !solid(cs[1]) || !solid(cs[2]) {
		t.Fatalf("相邻的两条匹配提交没有连成实线：%+v %+v", cs[1].segs, cs[2].segs)
	}
	if stub(cs[1]) || stub(cs[2]) {
		t.Fatalf("父提交在结果集里时不应画短桩：%+v %+v", cs[1].segs, cs[2].segs)
	}
	if !stub(cs[3]) {
		t.Fatalf("最后一条提交缺失父提交时没有画短桩：%+v", cs[3].segs)
	}

}
