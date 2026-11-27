package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// worktreeDump 是解析结果的快照形式：字段按固定顺序排，一行为一个工作树。
func worktreeDump(list []repoWorktree) string {
	var b strings.Builder
	for _, wt := range list {
		fmt.Fprintf(&b, "path=%s branch=%q head=%s detached=%v locked=%q prunable=%q bare=%v\n",
			wt.path, wt.branch, shortHash(wt.head), wt.detached, wt.locked, wt.prunable, wt.bare)
	}
	return b.String()
}

func TestParseWorktreeList(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	// 路径先过 canonicalRepoDir：Windows 会补盘符，分隔符也换成 \。
	path := func(p string) string { return "path=" + canonicalRepoDir(p) }
	line := func(p, branch, head, detached, locked, prunable, bare string) string {
		return path(p) + " branch=" + branch + " head=" + head + " detached=" + detached +
			" locked=" + locked + " prunable=" + prunable + " bare=" + bare + "\n"
	}
	plain := func(p, branch string) string {
		return line(p, strconv.Quote(branch), "0123456", "false", `""`, `""`, "false")
	}
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "普通",
			raw:  "worktree /repo/main\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00",
			want: plain("/repo/main", "main"),
		},
		{
			name: "分离头指针",
			raw:  "worktree /repo/main\x00HEAD " + sha + "\x00detached\x00\x00",
			want: line("/repo/main", `""`, "0123456", "true", `""`, `""`, "false"),
		},
		{
			name: "锁定（带与不带原因）",
			raw: "worktree /repo/main\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00" +
				"worktree /repo/locked\x00HEAD " + sha + "\x00branch refs/heads/side\x00locked\x00\x00" +
				"worktree /repo/reasoned\x00HEAD " + sha + "\x00branch refs/heads/other\x00locked user asked\x00\x00",
			want: plain("/repo/main", "main") +
				line("/repo/locked", `"side"`, "0123456", "false", `"locked"`, `""`, "false") +
				line("/repo/reasoned", `"other"`, "0123456", "false", `"user asked"`, `""`, "false"),
		},
		{
			name: "可清理",
			raw:  "worktree /repo/main\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00worktree /repo/gone\x00HEAD " + sha + "\x00branch refs/heads/side\x00prunable gitdir file points to non-existent location\x00\x00",
			want: plain("/repo/main", "main") +
				line("/repo/gone", `"side"`, "0123456", "false", `""`, `"gitdir file points to non-existent location"`, "false"),
		},
		{name: "空输出", raw: "", want: ""},
		{
			name: "含空格路径",
			raw:  "worktree /repo/my project\x00HEAD " + sha + "\x00branch refs/heads/feature branch\x00\x00worktree /repo/my project-wt\x00HEAD " + sha + "\x00detached\x00locked\x00\x00",
			want: plain("/repo/my project", "feature branch") +
				line("/repo/my project-wt", `""`, "0123456", "true", `"locked"`, `""`, "false"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := worktreeDump(parseWorktreeList([]byte(tc.raw))); got != tc.want {
				t.Fatalf("parseWorktreeList:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestWorktreeListExcludesMainWorktree(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "first")
	side := filepath.Join(t.TempDir(), "side checkout")
	git("worktree", "add", side, "-b", "side")

	ctx := context.Background()
	list, err := listWorktrees(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].branch != "side" {
		t.Fatalf("linked worktrees = %+v", list)
	}
	if list[0].path != canonicalRepoDir(side) {
		t.Fatalf("path=%q want %q", list[0].path, canonicalRepoDir(side))
	}

	// 从工作树里读也一样：主工作树排第一条，不进子行。
	fromSide, err := listWorktrees(ctx, side)
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range fromSide {
		if wt.path == canonicalRepoDir(dir) || wt.path == canonicalRepoDir(side) {
			t.Fatalf("main or own worktree left in %+v", fromSide)
		}
	}

	// 侧栏里该仓库下正好多一行，主工作树不再单独出现。
	a := &app{}
	tab := newTab(1, dir)
	tab.loaded, tab.worktrees = true, list
	a.tabs = []*repoTab{tab}
	rows := a.sidebarRows(a.repoItems())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].path != dir || rows[0].worktree != nil {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1].worktree == nil || rows[1].path != canonicalRepoDir(side) {
		t.Fatalf("child row = %+v", rows[1])
	}
	if label := worktreeRowLabel(*rows[1].worktree); label != "side" {
		t.Fatalf("child label = %q", label)
	}
}

func TestPruneAndRemoveWorktree(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "first")
	ctx := context.Background()

	// 1. 删除目录后变为 prunable，pruneWorktreeRecord 只清理指定记录，不影响同仓库其他失效记录。
	gone := filepath.Join(t.TempDir(), "gone-wt")
	goneOther := filepath.Join(t.TempDir(), "gone-wt-other")
	git("worktree", "add", gone, "-b", "gone-branch")
	git("worktree", "add", goneOther, "-b", "gone-other")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(goneOther); err != nil {
		t.Fatal(err)
	}
	list, err := listWorktrees(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].prunable == "" || list[1].prunable == "" {
		t.Fatalf("expected 2 prunable worktrees, got %+v", list)
	}
	if err := pruneWorktreeRecord(ctx, dir, gone, false); err != nil {
		t.Fatalf("pruneWorktreeRecord: %v", err)
	}
	list, err = listWorktrees(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !sameWorktreePath(list[0].path, goneOther) {
		t.Fatalf("expected only goneOther left after single-record prune, got %+v", list)
	}
	if err := pruneWorktreeRecord(ctx, dir, goneOther, false); err != nil {
		t.Fatalf("pruneWorktreeRecord second: %v", err)
	}
	list, err = listWorktrees(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 worktrees after pruning second, got %+v", list)
	}

	// 2. 活跃工作树预检：干净且已合并 -> 脏 -> 强制移除。
	active := filepath.Join(t.TempDir(), "active-wt")
	git("worktree", "add", active, "-b", "active-branch")
	list, err = listWorktrees(ctx, dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("list active: %v %+v", err, list)
	}
	_, check := inspectWorktreeRemoval(ctx, dir, list[0])
	if check.dirty || check.ignored || check.unmerged || check.submodule || check.locked != "" || check.err != "" {
		t.Fatalf("expected clean check, got %+v", check)
	}
	if err := os.WriteFile(filepath.Join(active, "untracked.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, check = inspectWorktreeRemoval(ctx, dir, list[0])
	if !check.dirty {
		t.Fatalf("expected dirty check, got %+v", check)
	}
	if err := removeWorktree(ctx, dir, active, true); err != nil {
		t.Fatalf("removeWorktree force: %v", err)
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Fatalf("expected removed directory, stat err=%v", err)
	}
}

func TestPruneRestoredWhileWaitingForLane(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "first")
	temp := canonicalRepoDir(t.TempDir())
	wt := filepath.Join(temp, "worktree")
	away := filepath.Join(temp, "away")
	git("worktree", "add", wt, "-b", "side")
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte("keep\n"), 0644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(wt, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(wt, away); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release := acquireGitLane(ctx, wt, true)
	defer release()
	done := make(chan error, 1)
	go func() { done <- pruneWorktreeRecord(ctx, dir, wt, false) }()
	for {
		gitLanes.Lock()
		waiting := len(gitLanes.ByKey[wt].Queue) > 0
		gitLanes.Unlock()
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("prune returned before waiting for the target lane: %v", err)
		case <-ctx.Done():
			t.Fatal("prune did not reach the target lane")
		case <-time.After(time.Millisecond):
		}
	}
	if err := os.Rename(away, wt); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; err == nil {
		t.Fatal("expected restored directory to block cleanup")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("restored file was removed: %v", err)
	}
	list, err := listWorktrees(context.Background(), dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("worktree record lost: %v %+v", err, list)
	}
}

func TestWorktreeSummaryAndModalUI(t *testing.T) {
	if !isTempWorktreePath("/tmp/agent-wt") || !isTempWorktreePath("/private/tmp/agent-wt") {
		t.Fatal("expected /tmp paths to be detected as temp worktree paths")
	}
	if isTempWorktreePath("/Users/dev/project-wt") {
		t.Fatal("unexpected temp path match for normal directory")
	}

	updates := make(chan func(), 16)
	a := &app{
		dispatch: func(fn func()) { updates <- fn },
		repoSummaries: map[string]repoSummary{
			"/repo/main": {
				branch: "main",
				worktrees: []repoWorktree{
					{path: "/private/tmp/gone", branch: "fix/gone", prunable: "gitdir missing"},
				},
			},
		},
	}
	if wts := a.repoWorktrees("/repo/main"); len(wts) != 1 || wts[0].branch != "fix/gone" {
		t.Fatalf("expected fallback to repoSummaries worktrees, got %+v", wts)
	}

	a.openWorktreeRemoveModal("/repo/main", repoWorktree{
		path:     "/private/tmp/gone",
		branch:   "fix/gone",
		prunable: "gitdir missing",
	})
	a.wtRemove.check = worktreeRemoveCheck{}
	tt := ui.NewTester(a.worktreeRemoveModal, 700, 500)
	if !tt.HasText("清理失效工作树") || !tt.HasText("清理记录") {
		t.Fatal("expected prunable modal title and confirm button")
	}

	a.openWorktreeRemoveModal("/repo/main", repoWorktree{
		path:   "/repo/wt",
		branch: "feat/side",
	})
	a.wtRemove.check = worktreeRemoveCheck{dirty: true, ignored: true, unmerged: true}
	tt = ui.NewTester(a.worktreeRemoveModal, 700, 500)
	if !tt.HasText("移除工作树") || !tt.HasText("存在未提交或未跟踪的改动，强制移除将丢失这些改动。") {
		t.Fatal("expected dirty warning in worktree remove modal")
	}
}

func TestSidebarFilterAndSiblingWorktrees(t *testing.T) {
	repoA := t.TempDir()
	repoB := t.TempDir()
	wtB := filepath.Join(t.TempDir(), "wt-b-prunable")
	a := &app{
		tabs: []*repoTab{
			{path: repoA, loaded: true, branch: "main"},
			{
				path:      repoB,
				loaded:    true,
				branch:    "main",
				worktrees: []repoWorktree{{path: wtB, branch: "feat/b", prunable: "gitdir missing"}},
			},
		},
		repoColors:      map[string]string{repoB: "blue"},
		repoColorFilter: "blue",
	}
	rows := a.sidebarRows(a.visibleRepoItems())
	if len(rows) != 2 || rows[1].parentRepo != repoB || rows[1].path != wtB {
		t.Fatalf("expected filtered worktree row to belong to %q, got %+v", repoB, rows)
	}
	if paths := a.visibleRepoPaths(); len(paths) != 2 || paths[0] != repoB || paths[1] != wtB {
		t.Fatalf("expected visibleRepoPaths to use filtered items, got %+v", paths)
	}

	// Sibling worktrees must not hide each other.
	wt1 := t.TempDir()
	wt2 := t.TempDir()
	b := &app{
		tabs: []*repoTab{
			{path: wt1, loaded: true, branch: "b1", worktrees: []repoWorktree{{path: wt2, branch: "b2"}}},
			{path: wt2, loaded: true, branch: "b2", worktrees: []repoWorktree{{path: wt1, branch: "b1"}}},
		},
	}
	sibRows := b.sidebarRows(b.repoItems())
	if len(sibRows) != 2 || sibRows[0].path != wt1 || sibRows[1].path != wt2 || sibRows[1].parentRepo != wt1 {
		t.Fatalf("expected wt1 as parent and wt2 as child, got %+v", sibRows)
	}
}

func TestWorktreeWSLPathAndCheckErrorDisablesConfirm(t *testing.T) {
	arg, err := resolveWorktreeGitArg(`\\wsl.localhost\Ubuntu\home\user\repo`, `\\wsl.localhost\Ubuntu\home\user\wt`)
	if err != nil || arg != "/home/user/wt" {
		t.Fatalf("expected /home/user/wt, got %q err=%v", arg, err)
	}
	if _, err := resolveWorktreeGitArg(`\\wsl.localhost\Ubuntu\home\user\repo`, `\\wsl.localhost\Debian\home\user\wt`); err == nil {
		t.Fatal("expected error for mismatched WSL distros")
	}

	updates := make(chan func(), 16)
	a := &app{dispatch: func(fn func()) { updates <- fn }}
	a.openWorktreeRemoveModal("/repo/main", repoWorktree{path: "/repo/wt", branch: "feat"})
	a.wtRemove.check = worktreeRemoveCheck{err: "status failed"}
	tt := ui.NewTester(a.worktreeRemoveModal, 700, 500)
	if !tt.HasText("安全检查失败，无法确认工作树是否安全移除。") {
		t.Fatal("expected safety check failure notice")
	}
	if err := tt.Click("移除工作树"); err == nil {
		// Disabled button click should not trigger removal.
		if a.wtRemove.busy {
			t.Fatal("expected confirm button to be disabled when check.err is set")
		}
	}
}
