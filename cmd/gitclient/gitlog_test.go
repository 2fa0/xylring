package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// resetGitLog 清空会话日志，测试结束后把原来那份放回去：
// 别的测试也会真的跑 git，日志是进程级的。
func resetGitLog(t *testing.T) {
	t.Helper()
	gitLog.mu.Lock()
	calls, bytes, next := gitLog.calls, gitLog.bytes, gitLog.next
	gitLog.calls, gitLog.bytes, gitLog.next = nil, 0, 0
	gitLog.mu.Unlock()
	t.Cleanup(func() {
		gitLog.mu.Lock()
		gitLog.calls, gitLog.bytes, gitLog.next = calls, bytes, next
		gitLog.mu.Unlock()
	})
}

func TestGitLogRedaction(t *testing.T) {
	cases := []struct {
		name, in   string
		secret     string
		wantSuffix string
	}{
		{
			name:       "url credentials",
			in:         "https://user:s3cret@example.com/team/repo.git",
			secret:     "s3cret",
			wantSuffix: "https://***@example.com/team/repo.git",
		},
		{
			name:       "url username only",
			in:         "ssh://git@example.com/repo.git",
			secret:     "git@",
			wantSuffix: "ssh://***@example.com/repo.git",
		},
		{
			name:       "authorization header",
			in:         "fatal: Authorization: Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ==",
			secret:     "QWxhZGRpbjpvcGVuIHNlc2FtZQ==",
			wantSuffix: "fatal: Authorization: ***",
		},
		{
			name:       "proxy authorization header",
			in:         "proxy-authorization: Bearer cHJveHktc2VjcmV0",
			secret:     "cHJveHktc2VjcmV0",
			wantSuffix: "proxy-authorization: ***",
		},
		{
			name:       "token argument",
			in:         "--token=ghp_tokenSecretValue",
			secret:     "ghp_tokenSecretValue",
			wantSuffix: "--token=***",
		},
		{
			name:       "access token argument",
			in:         "--access-token ghp_accessSecretValue",
			secret:     "ghp_accessSecretValue",
			wantSuffix: "--access-token ***",
		},
		{
			name:       "private token query",
			in:         "private-token=glpat_secretValue",
			secret:     "glpat_secretValue",
			wantSuffix: "private-token=***",
		},
		{
			name:       "http extra header",
			in:         "-c http.extraheader=Authorization: Basic QUJDREVG",
			secret:     "QUJDREVG",
			wantSuffix: "http.extraheader=***",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactGitText(tc.in)
			if strings.Contains(got, tc.secret) {
				t.Fatalf("secret survived redaction: %q", got)
			}
			if !strings.Contains(got, tc.wantSuffix) {
				t.Fatalf("redacted %q, want it to contain %q", got, tc.wantSuffix)
			}
		})
	}
	// 参数逐个脱敏：头信息不会把后面的参数一起吃掉。
	command := redactGitArgs([]string{"-c", "http.extraheader=Authorization: Basic QUJDREVG", "fetch", "origin"})
	if strings.Contains(command, "QUJDREVG") {
		t.Fatalf("argument secret survived: %q", command)
	}
	if !strings.HasSuffix(command, "fetch origin") {
		t.Fatalf("following arguments were swallowed: %q", command)
	}
}

func TestGitLogSummarizeOutput(t *testing.T) {
	text := summarizeGitOutput([]byte("hello\n"), gitLogTextLimit)
	if text.binary || text.truncated || text.text != "hello\n" || text.bytes != 6 {
		t.Fatalf("plain text: %+v", text)
	}
	// 二进制只留字节数。
	for _, b := range [][]byte{{0x00, 0x01, 0x02}, {0xff, 0xfe, 0xfd}} {
		out := summarizeGitOutput(b, gitLogTextLimit)
		if !out.binary || out.text != "" || out.bytes != len(b) {
			t.Fatalf("binary %v: %+v", b, out)
		}
	}
	// 文本截断到 limit，字节数仍是真实的。
	big := []byte(strings.Repeat("a", 40<<10))
	cut := summarizeGitOutput(big, gitLogTextLimit)
	if !cut.truncated || len(cut.text) != gitLogTextLimit || cut.bytes != len(big) {
		t.Fatalf("truncated: truncated=%v len=%d bytes=%d", cut.truncated, len(cut.text), cut.bytes)
	}
	// 截断落在多字节字符中间时，去掉那半个字符而不是留乱码。
	wide := []byte(strings.Repeat("日", 20000))
	half := summarizeGitOutput(wide, gitLogTextLimit)
	if !utf8.ValidString(half.text) || !half.truncated {
		t.Fatalf("truncated multibyte: valid=%v truncated=%v", utf8.ValidString(half.text), half.truncated)
	}
	// limit 为 0 表示一个字都不留：流式输出只记字节数。
	stream := summarizeGitOutput(big, 0)
	if stream.text != "" || stream.bytes != len(big) || stream.binary || stream.truncated {
		t.Fatalf("streamed: %+v", stream)
	}
}

func TestGitLogEntryLimit(t *testing.T) {
	resetGitLog(t)
	dir := t.TempDir()
	for i := 0; i < gitLogMaxCalls; i++ {
		id := gitLog.begin(dir, []string{"log", fmt.Sprintf("--skip=%d", i)})
		gitLog.finish(id, gitCallOK, time.Millisecond, 0, []byte("x"), nil)
	}
	if got := len(gitLog.entries(dir)); got != gitLogMaxCalls {
		t.Fatalf("entries=%d want %d", got, gitLogMaxCalls)
	}
	oldest := gitLog.entries(dir)[len(gitLog.entries(dir))-1].id
	last := gitLog.begin(dir, []string{"checkout", "main"})
	gitLog.finish(last, gitCallOK, time.Millisecond, 0, nil, nil)
	entries := gitLog.entries(dir)
	if len(entries) != gitLogMaxCalls {
		t.Fatalf("entries=%d want %d", len(entries), gitLogMaxCalls)
	}
	if entries[0].id != last {
		t.Fatalf("newest entry is %d, want %d", entries[0].id, last)
	}
	for _, e := range entries {
		if e.id == oldest {
			t.Fatal("the oldest finished entry survived the limit")
		}
	}
}

func TestGitLogByteLimitTrimsOnlyFinished(t *testing.T) {
	resetGitLog(t)
	dir := t.TempDir()
	// 运行中的那条最老，却不能被丢：它结束时就再没地方落账了。
	running := gitLog.begin(dir, []string{"log", "--stdin"})
	huge := strings.Repeat("a", 60<<10)
	for i := 0; i < gitLogMaxCalls; i++ {
		id := gitLog.begin(dir, []string{"rev-parse", huge})
		gitLog.finish(id, gitCallOK, time.Millisecond, 0, nil, nil)
	}
	entries := gitLog.entries(dir)
	if len(entries) == 0 {
		t.Fatal("no entries kept")
	}
	if gitLog.bytes > gitLogMaxBytes {
		t.Fatalf("bytes=%d over limit %d", gitLog.bytes, gitLogMaxBytes)
	}
	kept := false
	for _, e := range entries {
		if e.id == running {
			kept = true
			if e.status != gitCallRunning {
				t.Fatalf("running entry has status %v", e.status)
			}
		}
	}
	if !kept {
		t.Fatal("the running entry was trimmed")
	}
}

func TestGitLogCancelledKeepsEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook script needs a POSIX shell")
	}
	dir, _ := initTestRepo(t)
	resetGitLog(t)
	hooks := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(dir, "hook-started")
	script := "#!/bin/sh\n: > " + started + "\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), dir, "config", "core.hooksPath", hooks); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runGitExtra(ctx, dir, 16<<20, nil, "commit", "--allow-empty", "-m", "blocked")
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the hook never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled command: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled command did not return")
	}
	var found bool
	for _, call := range gitLog.entries(dir) {
		if !strings.HasPrefix(call.command, "commit") {
			continue
		}
		found = true
		if call.status != gitCallCancelled {
			t.Fatalf("cancelled call has status %v", call.status)
		}
		if call.ms < 0 {
			t.Fatalf("cancelled call has %d ms", call.ms)
		}
	}
	if !found {
		t.Fatal("a cancelled call left no entry")
	}
}

func TestGitLogPanelShowsAndClears(t *testing.T) {
	resetGitLog(t)
	dir := t.TempDir()
	stream := gitLog.beginStream(dir, []string{"log", "--date-order", "--format=%H"})
	gitLog.finish(stream, gitCallOK, 240*time.Millisecond, 0, []byte(strings.Repeat("x", 4096)), nil)
	binary := gitLog.begin(dir, []string{"cat-file", "blob", "HEAD:logo.png"})
	gitLog.finish(binary, gitCallOK, 8*time.Millisecond, 0, []byte{0x89, 0x50, 0x00, 0x01}, nil)
	id := gitLog.begin(dir, []string{"fetch", "--all"})
	gitLog.finish(id, gitCallFailed, 12*time.Millisecond, 128, []byte("Fetching origin\n"), []byte("fatal: could not read from remote"))
	if _, ok := gitLog.lastFailure(dir); !ok {
		t.Fatal("no failure to point the banner button at")
	}
	a := &app{}
	a.openGitLog(dir, true)
	defer func() { gitLogView.open, gitLogView.dir, gitLogView.state = false, "", ui.ListState{} }()
	tt := ui.NewTester(func(c *ui.Context) { a.gitLogModal(c) }, 900, 620)
	defer retireTester(tt)
	tt.Frame()
	saveLobePreview(t, tt, "git-log-panel")
	for _, text := range []string{"Git 日志", "fetch --all", "清空", "输出", "错误"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in the panel: %q", text, tt.Texts())
		}
	}
	if gitLogView.selected != 0 {
		t.Fatalf("the panel did not select the recent failure: %d", gitLogView.selected)
	}
	// 流式调用只记命令，不记输出。
	if err := tt.Click("log --date-order --format=%H"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("流式输出不记录") {
		t.Fatalf("a streamed call showed its output: %q", tt.Texts())
	}
	// 二进制输出只说字节数。
	if err := tt.Click("cat-file blob HEAD:logo.png"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("二进制输出（4 字节）") {
		t.Fatalf("binary output not summarized: %q", tt.Texts())
	}
	saveLobePreview(t, tt, "git-log-rows")
	if err := tt.Click("清空"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("没有记录") {
		t.Fatalf("panel did not go empty: %q", tt.Texts())
	}
	if len(gitLog.entries(dir)) != 0 {
		t.Fatal("clearing the panel kept the entries")
	}
}
