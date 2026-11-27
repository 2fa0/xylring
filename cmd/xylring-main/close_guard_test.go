package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestQuitBusyNamesTheRunningWrite(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*app)
		want  string
	}{
		{"idle", func(a *app) {}, ""},
		{"fetch-cancels-cleanly", func(a *app) { a.fetch.busy = true }, ""},
		{"board-writes-flush-on-close", func(a *app) { a.board.jobs = 3 }, ""},
		// 准备对话框只读仓库：查状态、读补丁时 a.op.busy 为真，但还没有写。
		{"preparing-dialog", func(a *app) { a.op.busy, a.write.busy = true, true }, ""},
		// 只读的批量操作（Blame、行历史、查看 Stash）同样占着这些标记。
		{"read-only-operations", func(a *app) {
			a.tabs[0].commitBusy = true
			a.tabs[0].pullBusy = true
			a.tabs[0].pushBusy = true
			a.tabs[0].manualFetchBusy = true
		}, ""},
		{"commit", func(a *app) { a.startGitWrite("正在提交") }, "正在提交"},
		{"push", func(a *app) { a.startGitWrite("正在推送") }, "正在推送"},
		{"finished", func(a *app) { a.finishGitWrite(a.startGitWrite("正在提交")) }, ""},
		// 同时有多个时只说最近开始的那一个，确认框不跟着 map 顺序跳。
		{"newest-wins", func(a *app) {
			a.startGitWrite("正在提交")
			a.startGitWrite("正在推送")
		}, "正在推送"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := newTab(1, "/tmp/example")
			defer tab.cancel()
			a := &app{tabs: []*repoTab{tab}}
			tc.setup(a)
			if got := a.quitBusy(); got != tc.want {
				t.Fatalf("quitBusy() = %q, want %q", got, tc.want)
			}
		})
	}
}

// 写操作的归属是应用，不是仓库标签页：关掉仓库不会停下已经发起的 Git。
func TestQuitBusySurvivesClosingTheRepo(t *testing.T) {
	tab := newTab(1, "/tmp/example")
	defer tab.cancel()
	a := &app{tabs: []*repoTab{tab}}
	a.startGitWrite("正在提交")

	a.closeTab(0)
	if len(a.tabs) != 0 {
		t.Fatalf("the repo should be gone, got %d tabs", len(a.tabs))
	}

	e := &mygo.CloseEvent{}
	a.closeGuardHook(e)
	if !e.DefaultPrevented() {
		t.Fatal("a commit started from a closed repo still must hold the window")
	}
	if !a.quitGuard.open {
		t.Fatal("the guard must still ask about the running commit")
	}
}

func TestCloseGuardHoldsTheWindowForARunningWrite(t *testing.T) {
	a := &app{}
	a.startGitWrite("正在提交")

	first := &mygo.CloseEvent{}
	a.closeGuardHook(first)
	if !first.DefaultPrevented() {
		t.Fatal("a running commit must keep the window open")
	}
	if !a.quitGuard.open {
		t.Fatal("the guard must ask before quitting on the commit")
	}

	// 同样的情况再点一次关闭按钮：窗口仍然留着，确认框不重复叠。
	a.closeGuardHook(&mygo.CloseEvent{})
	if !a.quitGuard.open {
		t.Fatal("the second close must still be held")
	}

	// 用户确认后这次关闭必须放行，否则窗口再也关不掉。
	a.quitGuard.allow = true
	confirmed := &mygo.CloseEvent{}
	a.closeGuardHook(confirmed)
	if confirmed.DefaultPrevented() {
		t.Fatal("a confirmed quit must not be blocked again")
	}
}

func TestCloseGuardLetsIdleWindowsClose(t *testing.T) {
	a := &app{fetch: fetchDialog{busy: true}}

	e := &mygo.CloseEvent{}
	a.closeGuardHook(e)
	if e.DefaultPrevented() {
		t.Fatal("a fetch alone must not hold the window")
	}
	if a.quitGuard.open {
		t.Fatal("nothing to confirm without a write")
	}
}

func TestCloseGuardDialogOffersStayingOrQuitting(t *testing.T) {
	a := &app{}
	a.startGitWrite("正在提交")

	tt := ui.NewTester(a.closeGuardModal, 900, 700)
	defer retireTester(tt)
	a.quitGuard = closeGuardState{open: true}
	tt.Frame()
	if !tt.HasText("正在提交，退出不会取消这次操作。") {
		t.Fatalf("missing the running write: %v", tt.Texts())
	}
	if err := tt.Click("先不退出"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.quitGuard.open || a.quitGuard.allow {
		t.Fatal("staying must only close the dialog")
	}

	a.quitGuard = closeGuardState{open: true}
	tt.Frame()
	if err := tt.Click("仍要退出"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.quitGuard.allow || a.quitGuard.open {
		t.Fatalf("quitting anyway must release the guard: %+v", a.quitGuard)
	}
}

// 用户在确认框前犹豫的时候操作可能已经跑完，这时不能还拿旧操作名当理由。
func TestCloseGuardDialogDropsAFinishedOperation(t *testing.T) {
	a := &app{quitGuard: closeGuardState{open: true}}
	a.finishGitWrite(a.startGitWrite("正在提交"))
	tt := ui.NewTester(a.closeGuardModal, 900, 700)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("操作已完成。") {
		t.Fatalf("missing the finished notice: %v", tt.Texts())
	}
}

func TestCloseGuardDialogSpeaksEnglish(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	a := &app{quitGuard: closeGuardState{open: true}}
	a.startGitWrite("正在提交")
	tt := ui.NewTester(a.closeGuardModal, 900, 700)
	defer retireTester(tt)
	tt.SetLocale("en")
	tt.Frame()
	assertEnglishUITexts(t, tt)
	for _, text := range []string{
		"Quit Xylring?",
		"Commit in progress; quitting will not cancel this operation.",
		"Stay",
		"Quit anyway",
	} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in %v", text, tt.Texts())
		}
	}
}

// 真正发起一次提交，确认写操作在干活期间被应用级地记着，而不是只在准备阶段亮个灯。
// Hook 用文件打点而不是 sleep：等到它就说明 Git 确实卡在 pre-commit 里，这一刻才算「正在写」。
func TestQuitBusyTracksARealCommit(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")

	// initTestRepo 把 core.hooksPath 指到 no-hooks，所以 Hook 要写在那儿才会真的运行。
	hooks := filepath.Join(dir, "no-hooks")
	started := filepath.Join(dir, "hook-started")
	release := filepath.Join(dir, "hook-release")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	// 等待有上限（约 5 秒），测试中途失败也不会把 Git 留在原地等一个永远不来的文件。
	script := fmt.Sprintf("#!/bin/sh\n: > '%s'\nwaited=0\nwhile [ ! -f '%s' ] && [ \"$waited\" -lt 150 ]; do\n\tsleep 0.02\n\twaited=$((waited + 1))\ndone\n", started, release)
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// 断言失败提前返回时也放它走：Hook 最多等到上限，通常在这里就被解开。
	defer os.WriteFile(release, nil, 0o600)

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o600)
	git("add", "a.txt")
	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	tab := newTab(1, dir)
	defer tab.cancel()
	tab.worktreeFiles, tab.commitMsg = files, "commit with a slow hook"
	updates := make(chan func(), 16)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	a.runCommitWorktree(tab)
	waitForFile(t, started)
	if got := a.quitBusy(); got != "正在提交" {
		t.Fatalf("quitBusy() = %q, want 正在提交", got)
	}

	// 提交还在跑的时候关掉仓库，保护不能跟着仓库一起消失。
	a.closeTab(0)
	if got := a.quitBusy(); got != "正在提交" {
		t.Fatalf("after closing the repo quitBusy() = %q, want 正在提交", got)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return a.quitBusy() == "" })
	if tab.commitErr != "" {
		t.Fatal(tab.commitErr)
	}
	if got := strings.TrimSpace(string(git("log", "-1", "--format=%s"))); got != "commit with a slow hook" {
		t.Fatalf("commit did not finish: %q", got)
	}
}

// waitForFile 等到 Hook 真的跑到，最多等 10 秒。
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 %s 超时：Hook 没有跑起来", filepath.Base(path))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 对话框只读仓库时（查状态、读补丁）不是在写，不能拿它当退出理由；
// 只读的批量操作（Blame）跑起来同样不算写。
func TestReadOnlyWorkDoesNotHoldTheWindow(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o600)

	tab := newTab(1, dir)
	defer tab.cancel()
	tab.loaded = true
	updates := make(chan func(), 32)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}

	a.openOperation("审阅并暂存", "", "a.txt")
	if !a.op.busy {
		t.Fatal("opening the dialog reads the patch")
	}
	if got := a.quitBusy(); got != "" {
		t.Fatalf("preparing a dialog reported a write: %q", got)
	}
	waitUpdates(t, updates, func() bool { return !a.op.busy })

	a.op.action = "Blame"
	a.op.gen++
	a.runOperation()
	if got := a.quitBusy(); got != "" {
		t.Fatalf("a read-only operation reported a write: %q", got)
	}
	waitUpdates(t, updates, func() bool { return !a.op.busy && !a.write.busy })
	drainUpdates(updates)
}
