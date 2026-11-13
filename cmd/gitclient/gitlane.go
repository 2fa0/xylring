package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
)

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

var gitLanes struct {
	sync.Mutex
	byKey map[string]*gitLane
}

// canonicalRepoDir 是命令日志和车道共用的仓库键：绝对路径，软链接已解开，
// 所以同一个仓库的两种写法（真目录、指向它的符号链接、带末尾斜杠）落到同一条车道。
func canonicalRepoDir(dir string) string {
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
	if ctx.Err() != nil {
		// 让随后的 acquireGit 报出同一个错误，这里不占位置。
		return func() {}
	}
	gitLanes.Lock()
	if gitLanes.byKey == nil {
		gitLanes.byKey = make(map[string]*gitLane)
	}
	lane := gitLanes.byKey[key]
	if lane == nil {
		lane = &gitLane{}
		gitLanes.byKey[key] = lane
	}
	if len(lane.queue) == 0 && lane.free(write) {
		lane.take(write)
		gitLanes.Unlock()
		return onceRelease(func() { releaseGitLane(key, write) })
	}
	w := &gitWaiter{write: write, ready: make(chan struct{})}
	lane.queue = append(lane.queue, w)
	gitLanes.Unlock()

	select {
	case <-w.ready:
		return onceRelease(func() { releaseGitLane(key, write) })
	case <-ctx.Done():
		if unqueueGitWaiter(key, w) {
			return func() {}
		}
		// 放行和取消撞在一起：位置已经给出去了，必须按顺序还回去。
		<-w.ready
		return onceRelease(func() { releaseGitLane(key, write) })
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
	gitLanes.Lock()
	defer gitLanes.Unlock()
	lane := gitLanes.byKey[key]
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
		delete(gitLanes.byKey, key)
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

func unqueueGitWaiter(key string, w *gitWaiter) bool {
	gitLanes.Lock()
	defer gitLanes.Unlock()
	lane := gitLanes.byKey[key]
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
			delete(gitLanes.byKey, key)
		}
		return true
	}
	return false
}

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

// gitCommandWrites 判断一条 git 命令会不会改仓库。它只看子命令，不看调用它的文件：
// 同一个文件里读写混排，按文件判断不成立。
//
// 这张表由 TestGitCommandWrites 逐条钉住；新增子命令时表里不补，测试就会失败。
// 未知子命令一律按写处理：判断错方向的代价是多等一会儿，不是数据错乱。
func gitCommandWrites(args []string) bool {
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
		return true
	}
	sub, rest := args[i], args[i+1:]
	switch sub {
	case "log", "rev-list", "rev-parse", "for-each-ref", "symbolic-ref", "show",
		"diff", "ls-tree", "cat-file", "merge-base", "var", "version", "status":
		return false
	case "worktree":
		return len(rest) == 0 || rest[0] != "list"
	case "branch", "tag":
		// 只有列出来是读；`branch --list` 带模式也只读。
		return !hasAnyArg(rest, "--list", "-l")
	case "remote":
		return !hasAnyArg(rest, "-v", "--verbose")
	case "config":
		// status 之所以能算读，靠的是 GIT_OPTIONAL_LOCKS=0（见 mergeGitEnv）。
		return !hasAnyArg(rest, "--get", "--get-regexp", "--list")
	default:
		return true
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
