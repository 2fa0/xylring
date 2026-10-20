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
