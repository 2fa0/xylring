package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSplitShortstat(t *testing.T) {
	record := strings.Repeat("a", 40) + "\x1f" + strings.Repeat("b", 40) + "\x1fauthor\x1f1700000000\x1fsubject\x1fAI-Agent: Cindy"
	cases := []struct {
		name  string
		rec   string
		lines int32
	}{
		{name: "both counts", rec: record + "\n\n 9 files changed, 1106 insertions(+), 14 deletions(-)", lines: 1120},
		{name: "singular insertions only", rec: record + "\n\n 1 file changed, 1 insertion(+)", lines: 1},
		{name: "deletions only", rec: record + "\n\n 2 files changed, 5 deletions(-)", lines: 5},
		{name: "binary file has no line counts", rec: record + "\n\n 1 file changed", lines: 0},
		{name: "merge commit has no stats", rec: record, lines: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, lines := splitShortstat(tc.rec)
			if body != record || lines != tc.lines {
				t.Fatalf("got body=%q lines=%d", body, lines)
			}
		})
	}
}

func TestSplitShortstatKeepsProvenance(t *testing.T) {
	pool := &provenanceTable{}
	record := strings.Repeat("a", 40) + "\x1f\x1fauthor\x1f1700000000\x1fsubject\x1fAI-Agent: Cindy\x1dAI-Model: exact-model"
	body, lines := splitShortstat(record + "\n\n 3 files changed, 7 insertions(+)")
	if lines != 7 {
		t.Fatalf("lines %d", lines)
	}
	c, err := parseCommitRecord(body, pool)
	if err != nil {
		t.Fatal(err)
	}
	if p := pool.get(c.prov); p.agent != "Cindy" || p.model != "exact-model" {
		t.Fatalf("provenance lost: %+v", p)
	}
	if c.subject != "subject" {
		t.Fatalf("subject polluted: %q", c.subject)
	}
}

func TestCommitRadiusFollowsChangeSize(t *testing.T) {
	cases := []struct {
		lines int32
		want  float32
	}{
		{0, 3}, {1, 3.5}, {10, 3.5}, {11, 4}, {50, 4},
		{51, 4.5}, {200, 4.5}, {201, 5}, {1000, 5}, {1001, 6}, {1 << 20, 6},
	}
	for _, tc := range cases {
		c := commit{lines: tc.lines}
		if got := commitRadius(&c); got != tc.want {
			t.Fatalf("lines %d: radius %v, want %v", tc.lines, got, tc.want)
		}
	}
	if got := commitRadius(&commit{lines: -1}); got != 4 {
		t.Fatalf("missing stats: radius %v, want base 4", got)
	}
}

func TestHistoryReadsShortstat(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "small.txt"), []byte(strings.Repeat("a\n", 3)), 0o600)
	git("add", "small.txt")
	git("commit", "-m", "small")
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("b\n", 400)), 0o600)
	git("add", "big.txt")
	git("commit", "-m", "big")

	bySubject := map[string]commit{}
	err := streamCommits(context.Background(), dir, 0, pageFirst, func(c commit) error {
		bySubject[c.subject] = c
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	small, big := bySubject["small"], bySubject["big"]
	if small.lines != 3 {
		t.Fatalf("small commit lines: %+v", small)
	}
	if big.lines != 400 {
		t.Fatalf("big commit lines: %+v", big)
	}
	if commitRadius(&small) >= commitRadius(&big) {
		t.Fatalf("bigger change must draw a bigger node: %v vs %v", commitRadius(&small), commitRadius(&big))
	}
}

func TestPartialCloneHistoryDoesNotFetchBlobs(t *testing.T) {
	isolateSettingsGit(t)
	source, git := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "first")
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-am", "second")
	git("config", "uploadpack.allowFilter", "true")
	clone := filepath.Join(t.TempDir(), "partial")
	git("clone", "--no-local", "--filter=blob:none", "--no-checkout", source, clone)
	ctx := context.Background()
	missing, err := runGit(ctx, clone, "rev-list", "--objects", "--all", "--missing=print")
	if err != nil || !strings.Contains(string(missing), "\n?") {
		t.Fatalf("fixture is not missing blobs: %q %v", missing, err)
	}
	if err := os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	pager := newHistoryPager(ctx, clone, historyQuery{}, []string{"HEAD"}, 0)
	defer pager.close()
	for i, subject := range []string{"second", "first"} {
		rows, more, err := pager.next(ctx, 1, historyMemoryStep)
		if err != nil || len(rows) != 1 {
			t.Fatalf("offline history page %d: %v rows, %v", i, len(rows), err)
		}
		if rows[0].subject != subject || rows[0].lines != -1 || more != (i == 0) {
			t.Fatalf("unexpected history page %d: %+v, more=%v", i, rows[0], more)
		}
	}
}

func TestGraphNodeSizeFollowsChangeVolume(t *testing.T) {
	tab := newTab(1, "/tmp/graph-size")
	tab.loaded, tab.branch = true, "main"
	tab.commits = []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2), hashN(3)}, subject: "merge-row", author: "A", when: 1700000000, lines: -1},
		{hash: hashN(2), parents: [][20]byte{hashN(4)}, subject: "tiny-change", author: "A", when: 1700000000, lines: 4},
		{hash: hashN(3), parents: [][20]byte{hashN(4)}, subject: "huge-change", author: "A", when: 1700000000, lines: 3300},
		{hash: hashN(4), subject: "base", author: "A", when: 1700000000, lines: 60},
	}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.head = tab.commits[0].hash
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 900, 500)
	tt.Frame()
	defer retireTester(tt)
	img := tt.Image()
	if path := os.Getenv("GRAPH_PNG"); path != "" {
		f, err := os.Create(path + ".sizes.png")
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
	// 没有年轮记录的实心节点，行中心的墨水宽度就是直径。
	width := func(subject string) int {
		t.Helper()
		row, ok := tt.Find(subject)
		if !ok {
			t.Fatalf("未找到行 %q", subject)
		}
		left, right := -1, -1
		for x := int(header.X); x < int(header.X+header.W); x++ {
			r, g, b, _ := img.At(x, int(row.Y+row.H/2)).RGBA()
			if max(r, g, b)-min(r, g, b) > 0x1800 {
				if left < 0 {
					left = x
				}
				right = x
			}
		}
		return right - left + 1
	}
	tiny, huge, base := width("tiny-change"), width("huge-change"), width("base")
	if !(tiny < base && base < huge) {
		t.Fatalf("节点没有按改动量缩放: tiny=%d base=%d huge=%d", tiny, base, huge)
	}
}

func TestHistoryMergeCommitHasNoStats(t *testing.T) {
	dir, git := initTestRepo(t)
	write := func(name, body string) {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
		git("add", name)
	}
	write("base.txt", "base\n")
	git("commit", "-m", "base")
	git("checkout", "-b", "side")
	write("side.txt", "side\n")
	git("commit", "-m", "side")
	git("checkout", "main")
	write("main.txt", "main\n")
	git("commit", "-m", "main")
	git("merge", "--no-ff", "-m", "merge side", "side")

	var merge commit
	err := streamCommits(context.Background(), dir, 0, pageFirst, func(c commit) error {
		if c.subject == "merge side" {
			merge = c
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if merge.hash == zeroHash {
		t.Fatal("merge commit missing from history")
	}
	if merge.lines != -1 {
		t.Fatalf("merge commit should not report stats: %+v", merge)
	}
	if commitRadius(&merge) != 4 {
		t.Fatalf("merge commit radius %v, want base 4", commitRadius(&merge))
	}
}
