package app

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
	gitLog.Mu.Lock()
	calls, bytes, next := gitLog.Calls, gitLog.Bytes, gitLog.Next
	gitLog.Calls, gitLog.Bytes, gitLog.Next = nil, 0, 0
	gitLog.Mu.Unlock()
	t.Cleanup(func() {
		gitLog.Mu.Lock()
		gitLog.Calls, gitLog.Bytes, gitLog.Next = calls, bytes, next
		gitLog.Mu.Unlock()
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
	if text.Binary || text.Truncated || text.Text != "hello\n" || text.Bytes != 6 {
		t.Fatalf("plain text: %+v", text)
	}
	// 二进制只留字节数。
	for _, b := range [][]byte{{0x00, 0x01, 0x02}, {0xff, 0xfe, 0xfd}} {
		out := summarizeGitOutput(b, gitLogTextLimit)
		if !out.Binary || out.Text != "" || out.Bytes != len(b) {
			t.Fatalf("binary %v: %+v", b, out)
		}
	}
	// 文本截断到 limit，字节数仍是真实的。
	big := []byte(strings.Repeat("a", 40<<10))
	cut := summarizeGitOutput(big, gitLogTextLimit)
	if !cut.Truncated || len(cut.Text) != gitLogTextLimit || cut.Bytes != len(big) {
		t.Fatalf("truncated: truncated=%v len=%d bytes=%d", cut.Truncated, len(cut.Text), cut.Bytes)
	}
	// 截断落在多字节字符中间时，去掉那半个字符而不是留乱码。
	wide := []byte(strings.Repeat("日", 20000))
	half := summarizeGitOutput(wide, gitLogTextLimit)
	if !utf8.ValidString(half.Text) || !half.Truncated {
		t.Fatalf("truncated multibyte: valid=%v truncated=%v", utf8.ValidString(half.Text), half.Truncated)
	}
	// limit 为 0 表示一个字都不留：流式输出只记字节数。
	stream := summarizeGitOutput(big, 0)
	if stream.Text != "" || stream.Bytes != len(big) || stream.Binary || stream.Truncated {
		t.Fatalf("streamed: %+v", stream)
	}
}

func TestGitLogEntryLimit(t *testing.T) {
	resetGitLog(t)
	dir := t.TempDir()
	for i := 0; i < gitLogMaxCalls; i++ {
		id := gitLog.Begin(dir, []string{"log", fmt.Sprintf("--skip=%d", i)})
		gitLog.Finish(id, gitCallOK, time.Millisecond, 0, []byte("x"), nil)
	}
	if got := len(gitLog.Entries(dir)); got != gitLogMaxCalls {
		t.Fatalf("entries=%d want %d", got, gitLogMaxCalls)
	}
	oldest := gitLog.Entries(dir)[len(gitLog.Entries(dir))-1].ID
	last := gitLog.Begin(dir, []string{"checkout", "main"})
	gitLog.Finish(last, gitCallOK, time.Millisecond, 0, nil, nil)
	entries := gitLog.Entries(dir)
	if len(entries) != gitLogMaxCalls {
		t.Fatalf("entries=%d want %d", len(entries), gitLogMaxCalls)
	}
	if entries[0].ID != last {
		t.Fatalf("newest entry is %d, want %d", entries[0].ID, last)
	}
	for _, e := range entries {
		if e.ID == oldest {
			t.Fatal("the oldest finished entry survived the limit")
		}
	}
}

func TestGitLogByteLimitTrimsOnlyFinished(t *testing.T) {
	resetGitLog(t)
	dir := t.TempDir()
	// 运行中的那条最老，却不能被丢：它结束时就再没地方落账了。
	running := gitLog.Begin(dir, []string{"log", "--stdin"})
	huge := strings.Repeat("a", 60<<10)
	for i := 0; i < gitLogMaxCalls; i++ {
		id := gitLog.Begin(dir, []string{"rev-parse", huge})
		gitLog.Finish(id, gitCallOK, time.Millisecond, 0, nil, nil)
	}
	entries := gitLog.Entries(dir)
	if len(entries) == 0 {
		t.Fatal("no entries kept")
	}
	if gitLog.Bytes > gitLogMaxBytes {
		t.Fatalf("bytes=%d over limit %d", gitLog.Bytes, gitLogMaxBytes)
	}
	kept := false
	for _, e := range entries {
		if e.ID == running {
			kept = true
			if e.Status != gitCallRunning {
				t.Fatalf("running entry has status %v", e.Status)
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
	for _, call := range gitLog.Entries(dir) {
		if !strings.HasPrefix(call.Command, "commit") {
			continue
		}
		found = true
		if call.Status != gitCallCancelled {
			t.Fatalf("cancelled call has status %v", call.Status)
		}
		if call.MS < 0 {
			t.Fatalf("cancelled call has %d ms", call.MS)
		}
	}
	if !found {
		t.Fatal("a cancelled call left no entry")
	}
}

func TestGitLogPanelShowsAndClears(t *testing.T) {
	resetGitLog(t)
	dir := t.TempDir()
	stream := gitLog.BeginStream(dir, []string{"log", "--date-order", "--format=%H"})
	gitLog.Finish(stream, gitCallOK, 240*time.Millisecond, 0, []byte(strings.Repeat("x", 4096)), nil)
	binary := gitLog.Begin(dir, []string{"cat-file", "blob", "HEAD:logo.png"})
	gitLog.Finish(binary, gitCallOK, 8*time.Millisecond, 0, []byte{0x89, 0x50, 0x00, 0x01}, nil)
	id := gitLog.Begin(dir, []string{"fetch", "--all"})
	gitLog.Finish(id, gitCallFailed, 12*time.Millisecond, 128, []byte("Fetching origin\n"), []byte("fatal: could not read from remote"))
	if _, ok := gitLog.LastFailure(dir); !ok {
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
	if len(gitLog.Entries(dir)) != 0 {
		t.Fatal("clearing the panel kept the entries")
	}
}
