package gitx

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// CanonicalizeSpecialRepoDir allows host layers (e.g. Docker integration) to
// canonicalize virtual repository keys without creating a dependency cycle.
var CanonicalizeSpecialRepoDir func(dir string) (string, bool)

type heldGitLanesKey struct{}

func GitLaneHeld(ctx context.Context, key string) bool {
	held, _ := ctx.Value(heldGitLanesKey{}).(map[string]bool)
	return held[key]
}

// AcquireWorktreeLanes holds the parent and target write lanes through inspection and removal.
// A fixed order lets overlapping worktree operations wait without a cycle.
func AcquireWorktreeLanes(ctx context.Context, dirs ...string) (context.Context, func()) {
	keys := make([]string, 0, len(dirs))
	seen := make(map[string]bool)
	for _, dir := range dirs {
		key := CanonicalRepoDir(dir)
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	var releases []func()
	for _, key := range keys {
		releases = append(releases, AcquireGitLane(ctx, key, true))
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

type GitLane struct {
	Readers int
	Writing bool
	Queue   []*gitWaiter
}

type GitLaneTable struct {
	sync.Mutex
	ByKey map[string]*GitLane
}

var (
	GitLanes    GitLaneTable
	RemoteLanes GitLaneTable
)

// CanonicalRepoDir 是命令日志和车道共用的仓库键：绝对路径，软链接已解开，
// 所以同一个仓库的两种写法（真目录、指向它的符号链接、带末尾斜杠）落到同一条车道。
// 容器键本身就是身份，不能当成本机路径做 Abs/EvalSymlinks。
func CanonicalRepoDir(dir string) string {
	if CanonicalizeSpecialRepoDir != nil {
		if key, ok := CanonicalizeSpecialRepoDir(dir); ok {
			return key
		}
	}
	if distro, linuxPath, ok := ParseWSLUNCPath(dir); ok {
		return FormatWSLUNCPath(distro, linuxPath)
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

// AcquireGitLane 拿到 key 上的一个读（write=false）或写（write=true）位置。
// 立刻能进就直接进；否则排队等放行，ctx 取消时把自己从队列里摘掉。
// 返回的释放函数可以被重复调用（延迟释放只该调用一次）。
func AcquireGitLane(ctx context.Context, key string, write bool) func() {
	return GitLanes.acquire(ctx, key, write)
}

// AcquireRemoteLane 拿到 key 上远端网络车道（RemoteLanes）的独占位置，
// 与本地读写车道（GitLanes）完全隔离。
func AcquireRemoteLane(ctx context.Context, key string) func() {
	return RemoteLanes.acquire(ctx, key, true)
}

func (t *GitLaneTable) acquire(ctx context.Context, key string, write bool) func() {
	if ctx.Err() != nil {
		// 让随后的 acquireGit/acquireRemoteGit 报出同一个错误，这里不占位置。
		return func() {}
	}
	t.Lock()
	if t.ByKey == nil {
		t.ByKey = make(map[string]*GitLane)
	}
	lane := t.ByKey[key]
	if lane == nil {
		lane = &GitLane{}
		t.ByKey[key] = lane
	}
	if len(lane.Queue) == 0 && lane.free(write) {
		lane.take(write)
		t.Unlock()
		return onceRelease(func() { t.release(key, write) })
	}
	w := &gitWaiter{write: write, ready: make(chan struct{})}
	lane.Queue = append(lane.Queue, w)
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
func (l *GitLane) free(write bool) bool {
	if l.Writing {
		return false
	}
	return !write || l.Readers == 0
}

func (l *GitLane) take(write bool) {
	if write {
		l.Writing = true
		return
	}
	l.Readers++
}

func ReleaseGitLane(key string, write bool) {
	GitLanes.release(key, write)
}

func (t *GitLaneTable) release(key string, write bool) {
	t.Lock()
	defer t.Unlock()
	lane := t.ByKey[key]
	if lane == nil {
		return
	}
	if write {
		lane.Writing = false
	} else if lane.Readers > 0 {
		lane.Readers--
	}
	lane.grant()
	if lane.idle() {
		delete(t.ByKey, key)
	}
}

// grant 从队首按 FIFO 放行：连续的读一起进（没有写者在等），
// 队首是写就要等到读者都退出。
func (l *GitLane) grant() {
	for len(l.Queue) > 0 {
		w := l.Queue[0]
		if !l.free(w.write) {
			return
		}
		l.Queue = l.Queue[1:]
		l.take(w.write)
		close(w.ready)
	}
}

func (l *GitLane) idle() bool {
	return l.Readers == 0 && !l.Writing && len(l.Queue) == 0
}

func (t *GitLaneTable) unqueue(key string, w *gitWaiter) bool {
	t.Lock()
	defer t.Unlock()
	lane := t.ByKey[key]
	if lane == nil {
		return false
	}
	for i, queued := range lane.Queue {
		if queued != w {
			continue
		}
		lane.Queue = append(lane.Queue[:i], lane.Queue[i+1:]...)
		// 摘掉的可能正是队首，后面的读跟着放行。
		lane.grant()
		if lane.idle() {
			delete(t.ByKey, key)
		}
		return true
	}
	return false
}

// GitLaneKind 表示一条 git 命令需要占用的仓库车道类型。
type GitLaneKind uint8

const (
	GitLaneLocalRead GitLaneKind = iota
	GitLaneLocalWrite
	GitLaneRemote
	GitLaneBoth
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

// GitCommandLane 判断一条 git 命令应占用本地读、本地写、远端还是双车道。
// 这张表由 TestGitCommandLane 与 TestGitCommandWrites 钉住；未知子命令一律按本地写处理。
func GitCommandLane(args []string) GitLaneKind {
	sub, rest, ok := gitSubcommand(args)
	if !ok {
		return GitLaneLocalWrite
	}
	switch sub {
	case "log", "rev-list", "rev-parse", "for-each-ref", "symbolic-ref", "show",
		"diff", "diff-tree", "ls-tree", "cat-file", "merge-base", "var", "version",
		"status", "check-ref-format":
		return GitLaneLocalRead
	case "worktree":
		if len(rest) > 0 && rest[0] == "list" {
			return GitLaneLocalRead
		}
		return GitLaneLocalWrite
	case "branch", "tag":
		// 只有列出来是读；`branch --list` 带模式也只读。
		if hasAnyArg(rest, "--list", "-l") {
			return GitLaneLocalRead
		}
		return GitLaneLocalWrite
	case "remote":
		if len(rest) == 0 || rest[0] == "-v" || rest[0] == "--verbose" || rest[0] == "get-url" {
			return GitLaneLocalRead
		}
		return GitLaneBoth
	case "config":
		// status 之所以能算读，靠的是 GIT_OPTIONAL_LOCKS=0（见 MergeGitEnv）。
		if hasAnyArg(rest, "--get", "--get-regexp", "--list") {
			return GitLaneLocalRead
		}
		return GitLaneLocalWrite
	case "fetch", "push":
		return GitLaneRemote
	case "pull", "gc", "prune":
		return GitLaneBoth
	default:
		return GitLaneLocalWrite
	}
}

// GitCommandWrites 判断一条 git 命令是否非只读。
func GitCommandWrites(args []string) bool {
	return GitCommandLane(args) != GitLaneLocalRead
}

// GitCommandUsesRemoteSlot 判断一条 git 命令是否应走独立的远端网络执行槽。
func GitCommandUsesRemoteSlot(args []string) bool {
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

// AcquireRepoLanes 按固定顺序申请仓库车道（先远端、后本地；释放逆序），避免死锁，
// 且确保 GitLaneBoth 在等待远端车道期间不会提前占住本地车道。
func AcquireRepoLanes(ctx context.Context, key string, kind GitLaneKind) (func(), error) {
	if err := ctx.Err(); err != nil {
		return func() {}, err
	}
	switch kind {
	case GitLaneLocalRead, GitLaneLocalWrite:
		if GitLaneHeld(ctx, key) {
			return func() {}, nil
		}
		release := AcquireGitLane(ctx, key, kind == GitLaneLocalWrite)
		if err := ctx.Err(); err != nil {
			release()
			return func() {}, err
		}
		return release, nil
	case GitLaneRemote:
		release := AcquireRemoteLane(ctx, key)
		if err := ctx.Err(); err != nil {
			release()
			return func() {}, err
		}
		return release, nil
	case GitLaneBoth:
		releaseRemote := AcquireRemoteLane(ctx, key)
		if err := ctx.Err(); err != nil {
			releaseRemote()
			return func() {}, err
		}
		releaseLocal := func() {}
		if !GitLaneHeld(ctx, key) {
			releaseLocal = AcquireGitLane(ctx, key, true)
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
