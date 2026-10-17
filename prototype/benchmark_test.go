package main

import (
	"encoding/binary"
	"fmt"
	"github.com/egoist/mygo/ui"
	"testing"
)

func BenchmarkCommitTable50K(b *testing.B) {
	tab := newTab(1, "/tmp/benchmark")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = make([]commit, 50000)
	for i := range tab.commits {
		var h [20]byte
		binary.BigEndian.PutUint64(h[12:], uint64(i+1))
		tab.commits[i] = commit{hash: h, author: "Benchmark", subject: fmt.Sprintf("commit %d", i), when: 1700000000}
		tab.graph.add(&tab.commits[i])
	}
	a := &app{tabs: []*repoTab{tab}, split: 48, filePane: 260}
	tt := ui.NewTester(a.view, 1200, 800)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tab.list.ScrollTo((i*137)%49000, ui.Start)
		tt.Frame()
	}
}
