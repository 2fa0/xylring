package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func resetGitLanes(t *testing.T) {
	t.Helper()
	gitLanes.Lock()
	saved := gitLanes.byKey
	gitLanes.byKey = nil
	gitLanes.Unlock()
	t.Cleanup(func() {
		gitLanes.Lock()
		gitLanes.byKey = saved
		gitLanes.Unlock()
	})
}

func gitLaneState(key string) (readers int, writing bool, queued int) {
	gitLanes.Lock()
	defer gitLanes.Unlock()
	lane := gitLanes.byKey[canonicalRepoDir(key)]
	if lane == nil {
		return 0, false, 0
	}
	return lane.readers, lane.writing, len(lane.queue)
}

// waitGitLaneQueue 等队列长到 n：放行和排队都在别的 goroutine 里发生。
func waitGitLaneQueue(t *testing.T, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, queued := gitLaneState(key); queued == n {
			return
		}
		if time.Now().After(deadline) {
			_, _, queued := gitLaneState(key)
			t.Fatalf("queue stayed at %d, want %d", queued, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGitLaneReadersShare(t *testing.T) {
	resetGitLanes(t)
	ctx := context.Background()
	key := canonicalRepoDir(t.TempDir())
	first := acquireGitLane(ctx, key, false)
	if readers, _, _ := gitLaneState(key); readers != 1 {
		t.Fatalf("readers=%d", readers)
	}
	second := make(chan func(), 1)
	go func() { second <- acquireGitLane(ctx, key, false) }()
	select {
	case release := <-second:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("a second reader waited for the first")
	}
	first()
	if readers, writing, queued := gitLaneState(key); readers != 0 || writing || queued != 0 {
		t.Fatalf("lane left readers=%d writing=%v queued=%d", readers, writing, queued)
	}
}

func TestGitLaneWriterExcludesReaders(t *testing.T) {
	resetGitLanes(t)
	ctx := context.Background()
	key := canonicalRepoDir(t.TempDir())
	write := acquireGitLane(ctx, key, true)
	if _, writing, _ := gitLaneState(key); !writing {
		t.Fatal("the writer did not take the lane")
	}
	reader := make(chan func(), 1)
	go func() { reader <- acquireGitLane(ctx, key, false) }()
	select {
	case release := <-reader:
		release()
		t.Fatal("a reader ran during the write")
	case <-time.After(50 * time.Millisecond):
	}
	write()
	select {
	case release := <-reader:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("the reader never ran after the write")
	}
}

func TestGitLaneFIFO(t *testing.T) {
	resetGitLanes(t)
	ctx := context.Background()
	key := canonicalRepoDir(t.TempDir())
	read := acquireGitLane(ctx, key, false)
	writer := make(chan func(), 1)
	go func() { writer <- acquireGitLane(ctx, key, true) }()
	waitGitLaneQueue(t, key, 1)
	// 后来的读者排在写者后面，不能插队。
	reader := make(chan func(), 1)
	go func() { reader <- acquireGitLane(ctx, key, false) }()
	waitGitLaneQueue(t, key, 2)
	read()
	select {
	case release := <-reader:
		release()
		t.Fatal("a later reader jumped ahead of the queued writer")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case release := <-writer:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("the queued writer never ran")
	}
	select {
	case release := <-reader:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("the reader never ran after the writer")
	}
}

func TestGitLaneCancelLeavesQueue(t *testing.T) {
	resetGitLanes(t)
	key := canonicalRepoDir(t.TempDir())
	write := acquireGitLane(context.Background(), key, true)
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan func(), 1)
	go func() { waiter <- acquireGitLane(ctx, key, false) }()
	waitGitLaneQueue(t, key, 1)
	cancel()
	select {
	case release := <-waiter:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling a waiter did not return")
	}
	if readers, writing, queued := gitLaneState(key); readers != 0 || !writing || queued != 0 {
		t.Fatalf("cancelled waiter left readers=%d writing=%v queued=%d", readers, writing, queued)
	}
	write()
	next := make(chan func(), 1)
	go func() { next <- acquireGitLane(context.Background(), key, false) }()
	select {
	case release := <-next:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("the lane stayed blocked after a cancelled waiter")
	}
}

func TestGitLaneDifferentKeysRunInParallel(t *testing.T) {
	resetGitLanes(t)
	ctx := context.Background()
	first := canonicalRepoDir(t.TempDir())
	second := canonicalRepoDir(t.TempDir())
	write := acquireGitLane(ctx, first, true)
	defer write()
	got := make(chan func(), 1)
	go func() { got <- acquireGitLane(ctx, second, false) }()
	select {
	case release := <-got:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("another repository waited for an unrelated write")
	}
}

func TestGitLaneCanonicalKey(t *testing.T) {
	resetGitLanes(t)
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if canonicalRepoDir(dir+string(filepath.Separator)) != canonicalRepoDir(dir) {
		t.Fatal("a trailing separator changed the key")
	}
	if canonicalRepoDir(link) != canonicalRepoDir(dir) {
		t.Fatal("a symlink to the repository got another key")
	}
	// 同一个仓库的两条路径共用一条车道。
	write := acquireGitLane(context.Background(), canonicalRepoDir(dir), true)
	reader := make(chan func(), 1)
	go func() { reader <- acquireGitLane(context.Background(), canonicalRepoDir(link), false) }()
	select {
	case release := <-reader:
		release()
		t.Fatal("the symlinked path took another lane")
	case <-time.After(50 * time.Millisecond):
	}
	write()
}

func TestGitCommandWrites(t *testing.T) {
	reads := [][]string{
		{"log"},
		{"rev-list", "--all"},
		{"rev-parse", "HEAD"},
		{"for-each-ref"},
		{"symbolic-ref", "-q", "--short", "HEAD"},
		{"show", "HEAD"},
		{"diff", "--name-status"},
		{"ls-tree", "HEAD"},
		{"cat-file", "-s", "HEAD:README.md"},
		{"merge-base", "a", "b"},
		{"var", "GIT_AUTHOR_IDENT"},
		{"version"},
		{"status", "--porcelain"},
		// `--no-optional-locks` 是最常见的全局选项，它不带值。
		{"--no-optional-locks", "status", "--porcelain=v1"},
		{"worktree", "list"},
		{"branch", "--list"},
		{"branch", "-l", "main"},
		{"tag", "--list"},
		{"remote", "-v"},
		{"remote", "--verbose"},
		{"config", "--get", "user.email"},
		{"config", "--get-regexp", `^user\.`},
		{"config", "--list"},
	}
	writes := [][]string{
		{"checkout", "main"},
		{"switch", "main"},
		{"branch", "feature"},
		{"tag", "v1"},
		{"reset", "--soft", "HEAD~1"},
		{"rebase", "main"},
		{"merge", "main"},
		{"commit", "-m", "x"},
		{"stash"},
		{"update-ref", "refs/heads/x", "HEAD"},
		{"worktree", "add", "../x"},
		{"remote", "add", "origin", "url"},
		{"config", "user.email", "x@example.com"},
		{"fetch", "--all"},
		{"pull"},
		{"push"},
		{"restore", "file"},
		{"gc"},
		{"prune"},
		// 未知子命令按写处理：判断错方向的代价只是多等一会儿。
		{"bisect", "start"},
		{"blame", "file"},
	}
	for _, args := range reads {
		// 真实调用都经过 gitArgs，分类前要先跳过带值的全局选项。
		if gitCommandWrites(gitArgs(args...)) {
			t.Fatalf("%v read as a write", args)
		}
		if gitCommandWrites(append([]string{"-C", "/tmp", "--git-dir=/tmp/repo.git"}, args...)) {
			t.Fatalf("%v with global options read as a write", args)
		}
	}
	for _, args := range writes {
		if !gitCommandWrites(gitArgs(args...)) {
			t.Fatalf("%v read as a read", args)
		}
	}
	if !gitCommandWrites(nil) {
		t.Fatal("no subcommand must fall back to write")
	}
}
