package main

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
	gitLogMaxCalls  = 200
	gitLogMaxBytes  = 8 << 20
	gitLogTextLimit = 32 << 10
)

type gitCallStatus uint8

const (
	gitCallRunning gitCallStatus = iota
	gitCallOK
	gitCallFailed
	gitCallCancelled
)

type gitCallOutput struct {
	text      string // 脱敏、截断后的前缀
	bytes     int    // 真实字节数
	truncated bool
	binary    bool
}

type gitCall struct {
	at      time.Time
	id      uint64
	dir     string
	command string
	ms      int64
	exit    int
	stream  bool // streamLog 的输出不记录
	status  gitCallStatus
	out     gitCallOutput
	err     gitCallOutput
}

type gitLogStore struct {
	mu    sync.Mutex
	next  uint64
	calls []gitCall // 旧的在前
	bytes int
}

var gitLog = &gitLogStore{}

// begin 记下一条命令开始执行，返回它的编号，finish 时用它找回这一条。
func (l *gitLogStore) begin(dir string, args []string) uint64 {
	return l.start(dir, args, false)
}

// beginStream 记下一条流式命令：它的输出可能有几百 MB，只记命令、耗时和退出码。
func (l *gitLogStore) beginStream(dir string, args []string) uint64 {
	return l.start(dir, args, true)
}

func (l *gitLogStore) start(dir string, args []string, stream bool) uint64 {
	call := gitCall{
		at:      time.Now(),
		dir:     canonicalRepoDir(dir),
		command: redactGitArgs(args),
		stream:  stream,
		status:  gitCallRunning,
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	call.id = l.next
	l.calls = append(l.calls, call)
	l.recount()
	l.trim()
	return call.id
}

func (l *gitLogStore) finish(id uint64, status gitCallStatus, dur time.Duration, exit int, stdout, stderr []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.calls {
		if l.calls[i].id != id {
			continue
		}
		c := &l.calls[i]
		c.status, c.ms, c.exit = status, dur.Milliseconds(), exit
		limit := gitLogTextLimit
		if c.stream {
			limit = 0
		}
		// 直接吃 limitedBuffer 的结果，不复制整份输出：只留前 32KB。
		c.out = summarizeGitOutput(stdout, limit)
		c.err = summarizeGitOutput(stderr, gitLogTextLimit)
		l.recount()
		l.trim()
		return
	}
	// 清空后面板丢掉了这一条，没有可写的目标。
}

// trim 只从已经结束的条目开始丢，运行中的条目永不丢：
// 丢掉的若是正在跑的那条，它结束时就再也没地方落账。
func (l *gitLogStore) trim() {
	for len(l.calls) > gitLogMaxCalls || l.bytes > gitLogMaxBytes {
		oldest := -1
		for i := range l.calls {
			if l.calls[i].status != gitCallRunning {
				oldest = i
				break
			}
		}
		if oldest < 0 {
			return
		}
		l.calls = append(l.calls[:oldest], l.calls[oldest+1:]...)
		l.recount()
	}
}

func (l *gitLogStore) recount() {
	total := 0
	for _, c := range l.calls {
		total += gitCallSize(c)
	}
	l.bytes = total
}

func gitCallSize(c gitCall) int {
	return len(c.dir) + len(c.command) + len(c.out.text) + len(c.err.text)
}

// entries 返回一份副本，最近的一条在前。dir 为空时返回整个会话的记录。
func (l *gitLogStore) entries(dir string) []gitCall {
	key := ""
	if dir != "" {
		key = canonicalRepoDir(dir)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]gitCall, 0, len(l.calls))
	for i := len(l.calls) - 1; i >= 0; i-- {
		if key == "" || l.calls[i].dir == key {
			out = append(out, l.calls[i])
		}
	}
	return out
}

// lastFailure 是最近一条失败或取消的调用，横幅的「查看 Git 日志」靠它决定出不出现。
func (l *gitLogStore) lastFailure(dir string) (gitCall, bool) {
	for _, c := range l.entries(dir) {
		if c.status == gitCallFailed || c.status == gitCallCancelled {
			return c, true
		}
	}
	return gitCall{}, false
}

func (l *gitLogStore) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = nil
	l.bytes = 0
}

// summarizeGitOutput 把输出收成面板能显示的一小段：二进制只留字节数，
// 文本留前 limit 字节并标出截断。limit 为 0 表示一个字都不留（流式输出）。
func summarizeGitOutput(b []byte, limit int) gitCallOutput {
	out := gitCallOutput{bytes: len(b)}
	if isBinaryOutput(b) {
		out.binary = true
		return out
	}
	if limit <= 0 {
		return out
	}
	if len(b) > limit {
		b = trimUTF8Prefix(b[:limit])
		out.truncated = true
	}
	out.text = string(b)
	return out
}

func isBinaryOutput(b []byte) bool {
	return bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b)
}

// trimUTF8Prefix 把一个从中间截断的多字节字符去掉，免得面板显示成乱码。
func trimUTF8Prefix(b []byte) []byte {
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

func redactGitArgs(args []string) string {
	// 逐个参数脱敏：一个参数里的头信息不会连带吃掉后面的参数。
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, quoteGitArg(redactGitText(a)))
	}
	return strings.Join(parts, " ")
}

func redactGitText(s string) string {
	s = gitLogURLCredentials.ReplaceAllString(s, "${1}***@")
	s = gitLogAuthHeader.ReplaceAllString(s, "${1}***")
	s = gitLogTokenArg.ReplaceAllString(s, "${1}***")
	s = gitLogExtraHeader.ReplaceAllString(s, "${1}***")
	return s
}

// gitExitCode 从 git 的退出错误里取退出码；没能跑起来记 -1。
func gitExitCode(err error) int {
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
