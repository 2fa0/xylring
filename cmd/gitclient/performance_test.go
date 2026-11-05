package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func perfHash(n int) [20]byte {
	var h [20]byte
	binary.LittleEndian.PutUint64(h[:], uint64(n+1))
	return h
}

func fastHistoryRepo(t *testing.T, n int) string {
	t.Helper()
	dir, _ := initTestRepo(t)
	var input strings.Builder
	for i := 0; i < n; i++ {
		msg := fmt.Sprintf("commit %d\n", i)
		fmt.Fprintf(&input, "commit refs/heads/main\ncommitter Test <test@example.com> %d +0000\ndata %d\n%s\n", 1700000000+i, len(msg), msg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(input.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fast-import: %s %v", out, err)
	}
	return dir
}

func stopPager(t *testing.T, p *historyPager) {
	t.Helper()
	p.close()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-p.rows:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("history Git process did not exit after cancellation")
		}
	}
}

func TestCloseTabClearsBackingReferences(t *testing.T) {
	a := &app{closed: true, tabs: []*repoTab{newTab(1, "/one"), newTab(2, "/two"), newTab(3, "/three")}}
	backing := a.tabs
	a.closeTab(2)
	if backing[2] != nil {
		t.Fatal("closed tail still retains repository state")
	}
	a.closeTab(0)
	if backing[1] != nil || a.tabs[0].path != "/two" {
		t.Fatal("removed middle slot was not cleared")
	}
	a.closeTab(0)
	if backing[0] != nil {
		t.Fatal("last closed tab remains reachable")
	}
}

func TestAuthorCacheFollowsCurrentHistory(t *testing.T) {
	tab := newTab(1, "/unused")
	rec := strings.Repeat("a", 40) + "\x1f\x1fAda\x1f1700000000\x1f" + strings.Repeat("x", 128<<10)
	c, err := parseCommitRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	author := tab.intern(c.author)
	if unsafe.StringData(author) == unsafe.StringData(c.author) {
		t.Fatal("short author still retains the large record's backing storage")
	}
	a := &app{}
	for i := 0; i < 20; i++ {
		a.applyHistory(tab, repoMeta{}, []commit{{hash: perfHash(i), author: fmt.Sprint(i)}}, 0, -1)
	}
	if len(tab.authors) != 1 || tab.indexOf(perfHash(0)) != -1 || tab.indexOf(perfHash(19)) != 0 {
		t.Fatal("obsolete authors or commit indices survived replacement")
	}
}

func TestHistoryPagerKeepsOnePinnedGitStream(t *testing.T) {
	dir := fastHistoryRepo(t, 900)
	ctx := context.Background()
	tips, err := historyTips(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := readQuery(t, dir, historyQuery{}, 0, 900)
	trace := filepath.Join(t.TempDir(), "trace.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	p := newHistoryPager(ctx, dir, historyQuery{}, tips, 0)
	defer stopPager(t, p)
	var got []commit
	for _, n := range []int{300, 200, 400} {
		rows, more, err := p.next(ctx, n, historyMemoryStep)
		if err != nil || more != (len(got)+n < 900) {
			t.Fatalf("page len=%d more=%v err=%v", len(rows), more, err)
		}
		got = append(got, rows...)
	}
	for i := range got {
		if got[i].hash != want[i].hash {
			t.Fatalf("pagination moved at %d", i)
		}
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, line := range strings.Split(string(data), "\n") {
		var e struct {
			Event string
			Argv  []string
		}
		if json.Unmarshal([]byte(line), &e) == nil && e.Event == "start" && slices.Contains(e.Argv, "log") {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("pagination restarted git log %d times", starts)
	}
}

func TestHistoryPagerPreservesRenameAcrossPages(t *testing.T) {
	dir, _ := renameHistoryRepo(t)
	h, err := resolveCommit(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	q := historyQuery{filePath: "新名.txt", fileAt: h}
	p := newHistoryPager(context.Background(), dir, q, []string{hashHex(h)}, 0)
	defer stopPager(t, p)
	for i, want := range []string{"新名.txt", "新名.txt", "新名.txt", "旧名[1].txt"} {
		rows, more, err := p.next(context.Background(), 1, historyMemoryStep)
		if err != nil || len(rows) != 1 || rows[0].file.path != want || more != (i < 3) {
			t.Fatalf("rename page %d: %+v more=%v err=%v", i, rows, more, err)
		}
	}
}

func TestPausedHistoryReleasesGitSlots(t *testing.T) {
	dir := fastHistoryRepo(t, 50)
	tips, err := historyTips(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	p := newHistoryPager(context.Background(), dir, historyQuery{}, tips, 0)
	defer stopPager(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := p.next(ctx, 1, historyMemoryStep); err != nil {
		t.Fatal(err)
	}
	for range cap(gitSlots) {
		if err := acquireGit(ctx); err != nil {
			t.Fatal("idle pager reserved a Git execution slot:", err)
		}
		defer func() { <-gitSlots }()
	}
}

func TestRefreshMissingAnchorStopsAfterBoundedProbe(t *testing.T) {
	dir := fastHistoryRepo(t, 1800)
	tab := newTab(1, dir)
	tab.inspecting = perfHash(999999)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	if tab.err != "" || !tab.hasMore || len(tab.commits) > pageFirst*2 || tab.inspecting != zeroHash {
		t.Fatalf("refresh exhausted history: rows=%d more=%v err=%s", len(tab.commits), tab.hasMore, tab.err)
	}
	a.jumpToHash(tab, tab.commits[0].hash)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
}

func TestHistoryByteBudgetAndContinuation(t *testing.T) {
	rows := make(chan commit, 4)
	for i := range 3 {
		rows <- commit{hash: perfHash(i), subject: strings.Repeat("x", 2048)}
	}
	close(rows)
	p := &historyPager{rows: rows, cancel: func() {}}
	for i := range 3 {
		page, more, err := p.next(context.Background(), 100, 2500)
		if err != nil || len(page) != 1 || page[0].hash != perfHash(i) || more != (i < 2) {
			t.Fatalf("byte budget lost records: %+v %v %v", page, more, err)
		}
	}
	tab := newTab(1, "/unused")
	tab.loaded, tab.hasMore = true, true
	tab.historyBytes = historyMemoryStep
	tab.commits = []commit{{hash: perfHash(1), subject: "loaded"}}
	empty := make(chan commit)
	close(empty)
	tab.pager = &historyPager{rows: empty, cancel: func() {}}
	updates := make(chan func(), 8)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	tt := ui.NewTester(a.view, 1100, 720)
	if tab.loading || !tt.HasText("继续加载") || !tt.HasText("已加载 1 条提交 · 还有更多历史") {
		t.Fatalf("memory budget did not pause automatic loading or missing footer hint: %q", tt.Texts())
	}
	if err := tt.Click("继续加载"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading })
	if tab.historyBudget != 2*historyMemoryStep {
		t.Fatal("explicit continuation did not extend the budget")
	}
}

func TestCommitTableFooterStates(t *testing.T) {
	tab := newTab(1, "/tmp/footer-repo")
	tab.loaded = true
	tab.commits = []commit{{hash: perfHash(1), subject: "commit 1"}}
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)

	// 1. Loading state shows loading text in table footer
	tab.loading = true
	tt.Frame()
	if !tt.HasText("正在加载更多历史提交…") {
		t.Fatalf("加载中状态未显示底部提示，文本：%q", tt.Texts())
	}

	// 2. Error state shows error and retry button
	tab.loading = false
	tab.err = "网络超时"
	tt.Frame()
	if !tt.HasText("网络超时") || !tt.HasText("重试") {
		t.Fatalf("错误状态未显示重试提示，文本：%q", tt.Texts())
	}
}

func TestGraphSharesStableRowsWithoutMutatingOldRows(t *testing.T) {
	var g graph
	a := commit{hash: perfHash(1), parents: [][20]byte{perfHash(2)}}
	g.add(&a)
	b := commit{hash: perfHash(2), parents: [][20]byte{perfHash(3)}}
	g.add(&b)
	before := slices.Clone(b.segs)
	c := commit{hash: perfHash(3), parents: [][20]byte{perfHash(4)}}
	g.add(&c)
	if &b.segs[0] != &c.segs[0] {
		t.Fatal("identical through-lane geometry is duplicated")
	}
	last := commit{hash: perfHash(4)}
	g.add(&last)
	if !slices.Equal(before, b.segs) || len(g.cols) != 0 || g.lanes() != 1 {
		t.Fatal("old geometry changed or empty tail lanes survived")
	}
}

func TestLargeDiffUsesReadablePlainViews(t *testing.T) {
	old, newText := strings.Repeat("a\n", 100001), strings.Repeat("b\n", 100001)
	d, simple := prepareDiff([]byte(old), []byte(newText), "a.txt")
	if simple == nil || d.Old.String() != old || d.New.String() != newText {
		t.Fatal("large diff lost file contents")
	}
	tab := newTab(1, "/unused")
	tab.diff, tab.simpleDiff = d, simple
	a := &app{}
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.fileDiffPane(c, tab) }) }, 1100, 720)
	for _, label := range []string{"原版本", "新版本"} {
		r, ok := tt.Find(label)
		if !ok || r.W < 100 || r.H < 100 {
			t.Fatalf("plain text pane is not visible: %s %+v", label, r)
		}
	}
	medium, _ := prepareDiff([]byte(strings.Repeat("a\n", 3000)), []byte(strings.Repeat("b\n", 3000)), "a.txt")
	if medium.Inline != editor.DiffInlineNone {
		t.Fatal("large hunk still computes inline highlights")
	}
}

func TestReferenceWatchDetectsGitChangesAndIgnoresIndexNoise(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	w := newRefWatch(context.Background(), dir)
	if w.changed() {
		t.Fatal("unchanged refs reported as modified")
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "unrelated"), []byte("noise"), 0600); err != nil {
		t.Fatal(err)
	}
	if w.changed() {
		t.Fatal("unrelated .git write forces a full ref enumeration")
	}
	git("branch", "feature/nested")
	if !w.changed() {
		t.Fatal("new nested branch was missed")
	}
	w = newRefWatch(context.Background(), dir)
	git("commit", "--allow-empty", "-m", "next")
	if !w.changed() {
		t.Fatal("branch update was missed")
	}
	w = newRefWatch(context.Background(), dir)
	git("pack-refs", "--all")
	if !w.changed() {
		t.Fatal("packed refs update was missed")
	}
	worktree := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "-b", "linked", worktree)
	w = newRefWatch(context.Background(), worktree)
	git("branch", "shared")
	if !w.changed() {
		t.Fatal("linked worktree missed a shared ref update")
	}
}

func TestCachedRepoScanDiscoversAdditionsAndRemovals(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	nested := filepath.Join(alpha, "lib", "nested")
	initGitDir(t, alpha)
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	roots, cache, err := scanGitRepos(context.Background(), root, nil)
	if err != nil || len(roots) != 1 {
		t.Fatal(roots, err)
	}
	initGitDir(t, nested)
	roots, cache, err = scanGitRepos(context.Background(), root, cache)
	if err != nil || len(roots) != 2 {
		t.Fatal("cache missed nested repository", roots, err)
	}
	if err := os.RemoveAll(filepath.Join(nested, ".git")); err != nil {
		t.Fatal(err)
	}
	roots, _, err = scanGitRepos(context.Background(), root, cache)
	if err != nil || len(roots) != 1 {
		t.Fatal("removed repository survived cache", roots, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := scanGitRepos(ctx, root, cache); err == nil {
		t.Fatal("cancelled scan kept running")
	}
}

func TestGitOutputBudgetCoversIOCopy(t *testing.T) {
	b := &limitedBuffer{limit: 32}
	if _, err := io.Copy(b, io.LimitReader(strings.NewReader(strings.Repeat("x", 1024)), 1024)); err != nil || b.Len() != 32 || !b.overflow {
		t.Fatal("output copy bypassed budget", b.Len(), err)
	}
	dir, _ := initTestRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := runGitLimit(ctx, dir, 16, "config", "--local", "--list")
	if err == nil || out != nil || !strings.Contains(err.Error(), "超过") || ctx.Err() != nil {
		t.Fatalf("output limit: out=%q err=%v ctx=%v", out, err, ctx.Err())
	}
}

func BenchmarkInstallHistoryWithManySelections(b *testing.B) {
	cs := make([]commit, 30000)
	tab := newTab(1, "/unused")
	for i := range cs {
		cs[i].hash = perfHash(i)
		tab.selection.Add(cs[i].hash)
	}
	p, _ := prepareHistory(context.Background(), cs, graph{}, nil, false)
	a := &app{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.installHistory(tab, repoMeta{}, p)
	}
}

func TestInactiveHistoryEvictionRestoresReadingPosition(t *testing.T) {
	dir := fastHistoryRepo(t, 900)
	cs := readQuery(t, dir, historyQuery{}, 0, 900)
	tab, active := newTab(1, dir), newTab(2, "/unused")
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab, active}, active: 0, dispatch: func(fn func()) { updates <- fn }}
	a.applyHistory(tab, repoMeta{}, cs, 0, -1)
	tab.loaded, active.loaded = true, true
	want := cs[650].hash
	tab.inspecting, tab.selected = want, 650
	tab.selection.Add(want)
	tab.historyBytes = historyMemoryStep + 1
	a.active = 1
	a.trimInactiveHistory()
	if tab.loaded || tab.commits != nil || tab.diff != nil || tab.restore == nil || tab.restore.hash != want {
		t.Fatal("eviction lost the reading position or kept heavy data")
	}
	defer tab.cancel()
	defer active.cancel()
	a.activate(0)
	waitUpdates(t, updates, func() bool { return !tab.loading && !tab.compareLoading && !tab.diffLoading })
	if tab.inspecting != want || tab.indexOf(want) != tab.selected {
		t.Fatal("reactivating an evicted repository did not restore selection")
	}
}
