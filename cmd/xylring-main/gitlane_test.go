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
	savedLocal := gitLanes.byKey
	gitLanes.byKey = nil
	gitLanes.Unlock()
	remoteLanes.Lock()
	savedRemote := remoteLanes.byKey
	remoteLanes.byKey = nil
	remoteLanes.Unlock()
	t.Cleanup(func() {
		gitLanes.Lock()
		gitLanes.byKey = savedLocal
		gitLanes.Unlock()
		remoteLanes.Lock()
		remoteLanes.byKey = savedRemote
		remoteLanes.Unlock()
	})
}

func remoteLaneState(key string) (readers int, writing bool, queued int) {
	remoteLanes.Lock()
	defer remoteLanes.Unlock()
	lane := remoteLanes.byKey[canonicalRepoDir(key)]
	if lane == nil {
		return 0, false, 0
	}
	return lane.readers, lane.writing, len(lane.queue)
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

// TestGitLaneStatusRunsAlongsideReaders 钉住文档 §三 第 2 步里的一处判断：
// status 之所以能算读，靠的是 mergeGitEnv 里的 GIT_OPTIONAL_LOCKS=0（它只关
// 「可选锁」，写操作的强制锁不受影响）。一旦有人把这个环境变量去掉，status
// 就会改 index，那时候它必须改回写，否则会和读者的车道假设打架。
//
// 访问顺序固定为「先车道、后全局槽」：反例——先 acquireGit 再 acquireGitLane
// ——会把这 4 个全局槽占死在等车道的路上，同一个仓库的写者永远等不到读者退出。
// 这一条不写成断言（它落地就是死锁），写在这里是为了让改这条路径的人先看到。
func TestGitLaneStatusRunsAlongsideReaders(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	first := acquireGitLane(ctx, canonicalRepoDir(dir), gitCommandWrites([]string{"status", "--porcelain"}))
	second := make(chan func(), 1)
	go func() {
		second <- acquireGitLane(ctx, canonicalRepoDir(dir), gitCommandWrites([]string{"status", "--porcelain=v2"}))
	}()
	select {
	case release := <-second:
		release()
	case <-time.After(2 * time.Second):
		first()
		t.Fatal("two status readers of one repository must not queue behind each other")
	}
	first()
	if _, _, queued := gitLaneState(canonicalRepoDir(dir)); queued != 0 {
		t.Fatalf("queue left at %d after both readers released", queued)
	}
}

func TestGitCommandLane(t *testing.T) {
	cases := []struct {
		args       []string
		wantLane   gitLaneKind
		wantRemote bool
	}{
		{[]string{"log"}, gitLaneLocalRead, false},
		{[]string{"diff-tree", "-r", "HEAD"}, gitLaneLocalRead, false},
		{[]string{"check-ref-format", "--branch", "main"}, gitLaneLocalRead, false},
		{[]string{"remote"}, gitLaneLocalRead, false},
		{[]string{"remote", "-v"}, gitLaneLocalRead, false},
		{[]string{"remote", "--verbose"}, gitLaneLocalRead, false},
		{[]string{"remote", "get-url", "--", "origin"}, gitLaneLocalRead, false},
		{[]string{"branch", "--list"}, gitLaneLocalRead, false},
		{[]string{"tag", "-l"}, gitLaneLocalRead, false},
		{[]string{"worktree", "list"}, gitLaneLocalRead, false},
		{[]string{"config", "--list"}, gitLaneLocalRead, false},
		{[]string{"checkout", "main"}, gitLaneLocalWrite, false},
		{[]string{"commit", "-m", "x"}, gitLaneLocalWrite, false},
		{[]string{"branch", "feature"}, gitLaneLocalWrite, false},
		{[]string{"tag", "v1"}, gitLaneLocalWrite, false},
		{[]string{"worktree", "add", "../x"}, gitLaneLocalWrite, false},
		{[]string{"config", "user.email", "x@example.com"}, gitLaneLocalWrite, false},
		{[]string{"bisect", "start"}, gitLaneLocalWrite, false},
		{nil, gitLaneLocalWrite, false},
		{[]string{"fetch", "--all"}, gitLaneRemote, true},
		{[]string{"push"}, gitLaneRemote, true},
		{[]string{"push", "origin", "main"}, gitLaneRemote, true},
		{[]string{"pull"}, gitLaneBoth, true},
		{[]string{"pull", "--rebase"}, gitLaneBoth, true},
		{[]string{"gc"}, gitLaneBoth, false},
		{[]string{"prune"}, gitLaneBoth, false},
		{[]string{"remote", "add", "origin", "url"}, gitLaneBoth, false},
		{[]string{"remote", "remove", "origin"}, gitLaneBoth, false},
		{[]string{"remote", "set-url", "origin", "url"}, gitLaneBoth, false},
	}
	for _, tc := range cases {
		if got := gitCommandLane(gitArgs(tc.args...)); got != tc.wantLane {
			t.Fatalf("gitCommandLane(%v) = %v, want %v", tc.args, got, tc.wantLane)
		}
		if got := gitCommandUsesRemoteSlot(gitArgs(tc.args...)); got != tc.wantRemote {
			t.Fatalf("gitCommandUsesRemoteSlot(%v) = %v, want %v", tc.args, got, tc.wantRemote)
		}
		if len(tc.args) > 0 {
			prefixed := append([]string{"-C", "/tmp", "--git-dir=/tmp/repo.git"}, tc.args...)
			if got := gitCommandLane(prefixed); got != tc.wantLane {
				t.Fatalf("gitCommandLane(prefixed %v) = %v, want %v", tc.args, got, tc.wantLane)
			}
			if got := gitCommandUsesRemoteSlot(prefixed); got != tc.wantRemote {
				t.Fatalf("gitCommandUsesRemoteSlot(prefixed %v) = %v, want %v", tc.args, got, tc.wantRemote)
			}
		}
	}
}

func TestRemoteLaneDoesNotBlockLocalLane(t *testing.T) {
	resetGitLanes(t)
	ctx := context.Background()
	key := canonicalRepoDir(t.TempDir())

	releaseRemote, err := acquireRepoLanes(ctx, key, gitLaneRemote)
	if err != nil {
		t.Fatal(err)
	}

	// 本地读和本地写都不得被远端车道阻塞。
	readDone := make(chan func(), 1)
	go func() {
		rel, e := acquireRepoLanes(ctx, key, gitLaneLocalRead)
		if e == nil {
			readDone <- rel
		}
	}()
	select {
	case rel := <-readDone:
		rel()
	case <-time.After(2 * time.Second):
		releaseRemote()
		t.Fatal("local read blocked by remote lane")
	}

	writeDone := make(chan func(), 1)
	go func() {
		rel, e := acquireRepoLanes(ctx, key, gitLaneLocalWrite)
		if e == nil {
			writeDone <- rel
		}
	}()
	select {
	case rel := <-writeDone:
		rel()
	case <-time.After(2 * time.Second):
		releaseRemote()
		t.Fatal("local write blocked by remote lane")
	}

	// 同仓库的第二个远端操作以及双车道操作必须排队，且双车道在等远端期间不能先占本地车道。
	secondRemote := make(chan func(), 1)
	go func() {
		rel, e := acquireRepoLanes(ctx, key, gitLaneRemote)
		if e == nil {
			secondRemote <- rel
		}
	}()
	waitRemoteQueue(t, key, 1)

	bothDone := make(chan func(), 1)
	go func() {
		rel, e := acquireRepoLanes(ctx, key, gitLaneBoth)
		if e == nil {
			bothDone <- rel
		}
	}()
	waitRemoteQueue(t, key, 2)

	// both 还在等 remoteLanes 时，本地写依旧畅通无阻。
	localWriteDuringWait := make(chan func(), 1)
	go func() {
		rel, e := acquireRepoLanes(ctx, key, gitLaneLocalWrite)
		if e == nil {
			localWriteDuringWait <- rel
		}
	}()
	select {
	case rel := <-localWriteDuringWait:
		rel()
	case <-time.After(2 * time.Second):
		releaseRemote()
		t.Fatal("local write blocked while gitLaneBoth waited for remote lane")
	}

	select {
	case rel := <-secondRemote:
		rel()
		t.Fatal("second remote ran concurrently")
	case <-time.After(30 * time.Millisecond):
	}

	releaseRemote()
	select {
	case rel := <-secondRemote:
		rel()
	case <-time.After(2 * time.Second):
		t.Fatal("second remote did not run")
	}
	select {
	case rel := <-bothDone:
		rel()
	case <-time.After(2 * time.Second):
		t.Fatal("gitLaneBoth did not run")
	}
	if r, w, q := gitLaneState(key); r != 0 || w || q != 0 {
		t.Fatalf("local lane left dirty: r=%d w=%v q=%d", r, w, q)
	}
	if r, w, q := remoteLaneState(key); r != 0 || w || q != 0 {
		t.Fatalf("remote lane left dirty: r=%d w=%v q=%d", r, w, q)
	}
}

func waitRemoteQueue(t *testing.T, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, queued := remoteLaneState(key); queued == n {
			return
		}
		if time.Now().After(deadline) {
			_, _, queued := remoteLaneState(key)
			t.Fatalf("remote queue stayed at %d, want %d", queued, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRemoteSlotsIndependentFromLocalSlots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for range cap(remoteSlots) {
		if err := acquireRemoteGit(ctx); err != nil {
			t.Fatal(err)
		}
		defer func() { <-remoteSlots }()
	}
	if err := acquireGit(ctx); err != nil {
		t.Fatalf("acquireGit blocked when remoteSlots were full: %v", err)
	}
	<-gitSlots
}
