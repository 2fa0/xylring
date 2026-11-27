package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type heldGitLanesKey struct{}

func gitLaneHeld(ctx context.Context, key string) bool {
	held, _ := ctx.Value(heldGitLanesKey{}).(map[string]bool)
	return held[key]
}

// Hold the parent and target write lanes through inspection and removal.
// A fixed order lets overlapping worktree operations wait without a cycle.
func acquireWorktreeLanes(ctx context.Context, dirs ...string) (context.Context, func()) {
	keys := make([]string, 0, len(dirs))
	seen := make(map[string]bool)
	for _, dir := range dirs {
		key := canonicalRepoDir(dir)
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	var releases []func()
	for _, key := range keys {
		releases = append(releases, acquireGitLane(ctx, key, true))
	}
	return context.WithValue(ctx, heldGitLanesKey{}, seen), func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
}

// 每个仓库一条车道：只读的 Git 进程可以并行，写操作必须独占这个仓库。
// 车道按先来后到放行，成串的历史读取不会把后到的写操作饿死。
//
// 加锁顺序固定为「先车道、后全局槽」：车道决定这个仓库上谁跑，全局槽
// （gitSlots）是跨仓库的稀缺资源。先拿全局槽再等车道会把 4 个槽占死，
// 谁也别想再跑起来。
type gitWaiter struct {
	write bool
	ready chan struct{}
}

type gitLane struct {
	readers int
	writing bool
	queue   []*gitWaiter
}

type gitLaneTable struct {
	sync.Mutex
	byKey map[string]*gitLane
}

var (
	gitLanes    gitLaneTable
	remoteLanes gitLaneTable
)

// canonicalRepoDir 是命令日志和车道共用的仓库键：绝对路径，软链接已解开，
// 所以同一个仓库的两种写法（真目录、指向它的符号链接、带末尾斜杠）落到同一条车道。
// 容器键本身就是身份，不能当成本机路径做 Abs/EvalSymlinks。
func canonicalRepoDir(dir string) string {
	if target, ok := parseDockerRepoKey(dir); ok {
		return dockerRepoKey(target)
	}
	if distro, linuxPath, ok := parseWSLUNCPath(dir); ok {
		return formatWSLUNCPath(distro, linuxPath)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs)
}

// acquireGitLane 拿到 key 上的一个读（write=false）或写（write=true）位置。
// 立刻能进就直接进；否则排队等放行，ctx 取消时把自己从队列里摘掉。
// 返回的释放函数可以被重复调用（延迟释放只该调用一次）。
func acquireGitLane(ctx context.Context, key string, write bool) func() {
	return gitLanes.acquire(ctx, key, write)
}

// acquireRemoteLane 拿到 key 上远端网络车道（remoteLanes）的独占位置，
// 与本地读写车道（gitLanes）完全隔离。
func acquireRemoteLane(ctx context.Context, key string) func() {
	return remoteLanes.acquire(ctx, key, true)
}

func (t *gitLaneTable) acquire(ctx context.Context, key string, write bool) func() {
	if ctx.Err() != nil {
		// 让随后的 acquireGit/acquireRemoteGit 报出同一个错误，这里不占位置。
		return func() {}
	}
	t.Lock()
	if t.byKey == nil {
		t.byKey = make(map[string]*gitLane)
	}
	lane := t.byKey[key]
	if lane == nil {
		lane = &gitLane{}
		t.byKey[key] = lane
	}
	if len(lane.queue) == 0 && lane.free(write) {
		lane.take(write)
		t.Unlock()
		return onceRelease(func() { t.release(key, write) })
	}
	w := &gitWaiter{write: write, ready: make(chan struct{})}
	lane.queue = append(lane.queue, w)
	t.Unlock()

	select {
	case <-w.ready:
		return onceRelease(func() { t.release(key, write) })
	case <-ctx.Done():
		if t.unqueue(key, w) {
			return func() {}
		}
		// 放行和取消撞在一起：位置已经给出去了，必须按顺序还回去。
		<-w.ready
		return onceRelease(func() { t.release(key, write) })
	}
}

func onceRelease(release func()) func() {
	var once sync.Once
	return func() { once.Do(release) }
}

// free 说的是「进来的话能不能立刻跑」，不是「现在空闲」。
func (l *gitLane) free(write bool) bool {
	if l.writing {
		return false
	}
	return !write || l.readers == 0
}

func (l *gitLane) take(write bool) {
	if write {
		l.writing = true
		return
	}
	l.readers++
}

func releaseGitLane(key string, write bool) {
	gitLanes.release(key, write)
}

func (t *gitLaneTable) release(key string, write bool) {
	t.Lock()
	defer t.Unlock()
	lane := t.byKey[key]
	if lane == nil {
		return
	}
	if write {
		lane.writing = false
	} else if lane.readers > 0 {
		lane.readers--
	}
	lane.grant()
	if lane.idle() {
		delete(t.byKey, key)
	}
}

// grant 从队首按 FIFO 放行：连续的读一起进（没有写者在等），
// 队首是写就要等到读者都退出。
func (l *gitLane) grant() {
	for len(l.queue) > 0 {
		w := l.queue[0]
		if !l.free(w.write) {
			return
		}
		l.queue = l.queue[1:]
		l.take(w.write)
		close(w.ready)
	}
}

func (l *gitLane) idle() bool {
	return l.readers == 0 && !l.writing && len(l.queue) == 0
}

func (t *gitLaneTable) unqueue(key string, w *gitWaiter) bool {
	t.Lock()
	defer t.Unlock()
	lane := t.byKey[key]
	if lane == nil {
		return false
	}
	for i, queued := range lane.queue {
		if queued != w {
			continue
		}
		lane.queue = append(lane.queue[:i], lane.queue[i+1:]...)
		// 摘掉的可能正是队首，后面的读跟着放行。
		lane.grant()
		if lane.idle() {
			delete(t.byKey, key)
		}
		return true
	}
	return false
}

// gitLaneKind 表示一条 git 命令需要占用的仓库车道类型。
type gitLaneKind uint8

const (
	gitLaneLocalRead gitLaneKind = iota
	gitLaneLocalWrite
	gitLaneRemote
	gitLaneBoth
)

// gitGlobalOptionsWithValue 是 git 的全局选项里「后面还跟一个值」的那些。
// 分类子命令前要先跳过它们，否则 `-c a.b=c checkout` 会被当成 `a.b=c`。
var gitGlobalOptionsWithValue = map[string]bool{
	"-c":             true,
	"-C":             true,
	"--config-env":   true,
	"--exec-path":    true,
	"--git-dir":      true,
	"--namespace":    true,
	"--super-prefix": true,
	"--work-tree":    true,
	"--attr-source":  true,
}

func gitSubcommand(args []string) (sub string, rest []string, ok bool) {
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			i++
			continue
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			break
		}
		name, inline := a, false
		if j := strings.IndexByte(a, '='); j >= 0 {
			name, inline = a[:j], true
		}
		i++
		// `--git-dir=/tmp/x` 自带值，`--git-dir /tmp/x` 的值是下一个参数。
		if gitGlobalOptionsWithValue[name] && !inline && i < len(args) {
			i++
		}
	}
	if i >= len(args) {
		return "", nil, false
	}
	return args[i], args[i+1:], true
}

// gitCommandLane 判断一条 git 命令应占用本地读、本地写、远端还是双车道。
// 这张表由 TestGitCommandLane 与 TestGitCommandWrites 钉住；未知子命令一律按本地写处理。
func gitCommandLane(args []string) gitLaneKind {
	sub, rest, ok := gitSubcommand(args)
	if !ok {
		return gitLaneLocalWrite
	}
	switch sub {
	case "log", "rev-list", "rev-parse", "for-each-ref", "symbolic-ref", "show",
		"diff", "diff-tree", "ls-tree", "cat-file", "merge-base", "var", "version",
		"status", "check-ref-format":
		return gitLaneLocalRead
	case "worktree":
		if len(rest) > 0 && rest[0] == "list" {
			return gitLaneLocalRead
		}
		return gitLaneLocalWrite
	case "branch", "tag":
		// 只有列出来是读；`branch --list` 带模式也只读。
		if hasAnyArg(rest, "--list", "-l") {
			return gitLaneLocalRead
		}
		return gitLaneLocalWrite
	case "remote":
		if len(rest) == 0 || rest[0] == "-v" || rest[0] == "--verbose" || rest[0] == "get-url" {
			return gitLaneLocalRead
		}
		return gitLaneBoth
	case "config":
		// status 之所以能算读，靠的是 GIT_OPTIONAL_LOCKS=0（见 mergeGitEnv）。
		if hasAnyArg(rest, "--get", "--get-regexp", "--list") {
			return gitLaneLocalRead
		}
		return gitLaneLocalWrite
	case "fetch", "push":
		return gitLaneRemote
	case "pull", "gc", "prune":
		return gitLaneBoth
	default:
		return gitLaneLocalWrite
	}
}

// gitCommandWrites 判断一条 git 命令是否非只读。
func gitCommandWrites(args []string) bool {
	return gitCommandLane(args) != gitLaneLocalRead
}

// gitCommandUsesRemoteSlot 判断一条 git 命令是否应走独立的远端网络执行槽。
func gitCommandUsesRemoteSlot(args []string) bool {
	sub, _, ok := gitSubcommand(args)
	if !ok {
		return false
	}
	switch sub {
	case "fetch", "push", "pull":
		return true
	default:
		return false
	}
}

// acquireRepoLanes 按固定顺序申请仓库车道（先远端、后本地；释放逆序），避免死锁，
// 且确保 gitLaneBoth 在等待远端车道期间不会提前占住本地车道。
func acquireRepoLanes(ctx context.Context, key string, kind gitLaneKind) (func(), error) {
	if err := ctx.Err(); err != nil {
		return func() {}, err
	}
	switch kind {
	case gitLaneLocalRead, gitLaneLocalWrite:
		if gitLaneHeld(ctx, key) {
			return func() {}, nil
		}
		release := acquireGitLane(ctx, key, kind == gitLaneLocalWrite)
		if err := ctx.Err(); err != nil {
			release()
			return func() {}, err
		}
		return release, nil
	case gitLaneRemote:
		release := acquireRemoteLane(ctx, key)
		if err := ctx.Err(); err != nil {
			release()
			return func() {}, err
		}
		return release, nil
	case gitLaneBoth:
		releaseRemote := acquireRemoteLane(ctx, key)
		if err := ctx.Err(); err != nil {
			releaseRemote()
			return func() {}, err
		}
		releaseLocal := func() {}
		if !gitLaneHeld(ctx, key) {
			releaseLocal = acquireGitLane(ctx, key, true)
			if err := ctx.Err(); err != nil {
				releaseLocal()
				releaseRemote()
				return func() {}, err
			}
		}
		return func() {
			releaseLocal()
			releaseRemote()
		}, nil
	default:
		return func() {}, nil
	}
}

func hasAnyArg(args []string, names ...string) bool {
	for _, a := range args {
		for _, name := range names {
			if a == name {
				return true
			}
		}
	}
	return false
}
