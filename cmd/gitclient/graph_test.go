package main

import "testing"

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
