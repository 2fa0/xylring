package gitx

import (
	"bytes"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// 会话内的 Git 命令日志：最近 200 条、8MB 两个上限，只在内存里，
// 够回答「刚才到底跑了哪条命令、为什么失败了」。
const (
	GitLogMaxCalls  = 200
	GitLogMaxBytes  = 8 << 20
	GitLogTextLimit = 32 << 10
)

type GitCallStatus uint8

const (
	GitCallRunning GitCallStatus = iota
	GitCallOK
	GitCallFailed
	GitCallCancelled
)

type GitCallOutput struct {
	Text      string // 脱敏、截断后的前缀
	Bytes     int    // 真实字节数
	Truncated bool
	Binary    bool
}

type GitCall struct {
	At      time.Time
	ID      uint64
	Dir     string
	Command string
	MS      int64
	Exit    int
	Stream  bool // streamLog 的输出不记录
	Status  GitCallStatus
	Out     GitCallOutput
	Err     GitCallOutput
}

type GitLogStore struct {
	Mu    sync.Mutex
	Next  uint64
	Calls []GitCall // 旧的在前
	Bytes int
}

var GitLog = &GitLogStore{}

// Begin 记下一条命令开始执行，返回它的编号，Finish 时用它找回这一条。
func (l *GitLogStore) Begin(dir string, args []string) uint64 {
	return l.start(dir, args, false)
}

// BeginStream 记下一条流式命令：它的输出可能有几百 MB，只记命令、耗时和退出码。
func (l *GitLogStore) BeginStream(dir string, args []string) uint64 {
	return l.start(dir, args, true)
}

func (l *GitLogStore) start(dir string, args []string, stream bool) uint64 {
	call := GitCall{
		At:      time.Now(),
		Dir:     CanonicalRepoDir(dir),
		Command: RedactGitArgs(args),
		Stream:  stream,
		Status:  GitCallRunning,
	}
	l.Mu.Lock()
	defer l.Mu.Unlock()
	l.Next++
	call.ID = l.Next
	l.Calls = append(l.Calls, call)
	l.recount()
	l.trim()
	return call.ID
}

func (l *GitLogStore) Finish(id uint64, status GitCallStatus, dur time.Duration, exit int, stdout, stderr []byte) {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	for i := range l.Calls {
		if l.Calls[i].ID != id {
			continue
		}
		c := &l.Calls[i]
		c.Status, c.MS, c.Exit = status, dur.Milliseconds(), exit
		limit := GitLogTextLimit
		if c.Stream {
			limit = 0
		}
		// 直接吃 limitedBuffer 的结果，不复制整份输出：只留前 32KB。
		c.Out = SummarizeGitOutput(stdout, limit)
		c.Err = SummarizeGitOutput(stderr, GitLogTextLimit)
		l.recount()
		l.trim()
		return
	}
	// 清空后面板丢掉了这一条，没有可写的目标。
}

// trim 只从已经结束的条目开始丢，运行中的条目永不丢：
// 丢掉的若是正在跑的那条，它结束时就再也没地方落账。
func (l *GitLogStore) trim() {
	for len(l.Calls) > GitLogMaxCalls || l.Bytes > GitLogMaxBytes {
		oldest := -1
		for i := range l.Calls {
			if l.Calls[i].Status != GitCallRunning {
				oldest = i
				break
			}
		}
		if oldest < 0 {
			return
		}
		l.Calls = append(l.Calls[:oldest], l.Calls[oldest+1:]...)
		l.recount()
	}
}

func (l *GitLogStore) recount() {
	total := 0
	for _, c := range l.Calls {
		total += gitCallSize(c)
	}
	l.Bytes = total
}

func gitCallSize(c GitCall) int {
	return len(c.Dir) + len(c.Command) + len(c.Out.Text) + len(c.Err.Text)
}

// Entries 返回一份副本，最近的一条在前。dir 为空时返回整个会话的记录。
func (l *GitLogStore) Entries(dir string) []GitCall {
	key := ""
	if dir != "" {
		key = CanonicalRepoDir(dir)
	}
	l.Mu.Lock()
	defer l.Mu.Unlock()
	out := make([]GitCall, 0, len(l.Calls))
	for i := len(l.Calls) - 1; i >= 0; i-- {
		if key == "" || l.Calls[i].Dir == key {
			out = append(out, l.Calls[i])
		}
	}
	return out
}

// LastFailure 是最近一条失败或取消的调用，横幅的「查看 Git 日志」靠它决定出不出现。
func (l *GitLogStore) LastFailure(dir string) (GitCall, bool) {
	for _, c := range l.Entries(dir) {
		if c.Status == GitCallFailed || c.Status == GitCallCancelled {
			return c, true
		}
	}
	return GitCall{}, false
}

func (l *GitLogStore) Clear() {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	l.Calls = nil
	l.Bytes = 0
}

// SummarizeGitOutput 把输出收成面板能显示的一小段：二进制只留字节数，
// 文本留前 limit 字节并标出截断。limit 为 0 表示一个字都不留（流式输出）。
func SummarizeGitOutput(b []byte, limit int) GitCallOutput {
	out := GitCallOutput{Bytes: len(b)}
	if isBinaryOutput(b) {
		out.Binary = true
		return out
	}
	if limit <= 0 {
		return out
	}
	if len(b) > limit {
		b = TrimUTF8Prefix(b[:limit])
		out.Truncated = true
	}
	out.Text = string(b)
	return out
}

func isBinaryOutput(b []byte) bool {
	return bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b)
}

// TrimUTF8Prefix 把一个从中间截断的多字节字符去掉，免得面板显示成乱码。
func TrimUTF8Prefix(b []byte) []byte {
	for len(b) > 0 {
		r, size := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || size > 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

// 四条脱敏规则：URL 里的 user:pass@、authorization 头、token 类参数、http.extraheader。
// 头部规则吃掉整行：凭据可能不止一个词（`Authorization: Basic …`），
// 留一半就等于没脱敏。
var (
	gitLogURLCredentials = regexp.MustCompile(`(?i)([a-z][a-z0-9+.\-]*://)[^/\s@]+@`)
	gitLogAuthHeader     = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization["']?\s*[:=]\s*)[^\r\n]*`)
	gitLogTokenArg       = regexp.MustCompile(`(?i)\b((?:--?[a-z0-9-]*token|private-token|token)\s*(?:[=:]\s*|\s+))("?[^\s"']+"?|'[^']*')`)
	gitLogExtraHeader    = regexp.MustCompile(`(?i)\b(http\.extraheader["']?\s*[=:]\s*)[^\r\n]*`)
)

func RedactGitArgs(args []string) string {
	// 逐个参数脱敏：一个参数里的头信息不会连带吃掉后面的参数。
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, quoteGitArg(RedactGitText(a)))
	}
	return strings.Join(parts, " ")
}

func RedactGitText(s string) string {
	s = gitLogURLCredentials.ReplaceAllString(s, "${1}***@")
	s = gitLogAuthHeader.ReplaceAllString(s, "${1}***")
	s = gitLogTokenArg.ReplaceAllString(s, "${1}***")
	s = gitLogExtraHeader.ReplaceAllString(s, "${1}***")
	return s
}

// GitExitCode 从 git 的退出错误里取退出码；没能跑起来记 -1。
func GitExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// quoteGitArg 只用于面板里给带空格的参数加引号，让一条命令读起来还是原来的样子。
func quoteGitArg(a string) string {
	if a == "" || strings.ContainsAny(a, " \t\"'") {
		return strconv.Quote(a)
	}
	return a
}
