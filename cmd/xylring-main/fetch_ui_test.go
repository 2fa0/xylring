package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFetchDialogOpensFromToolbar(t *testing.T) {
	tab := graphTabForWrite()
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("拉取或获取远程更新"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"获取远程更新", "拉取到当前分支", "获取选项…"} {
		found := false
		for _, item := range tt.Menu() {
			found = found || item == label
		}
		if !found {
			t.Fatalf("missing sync action %q in %q", label, tt.Menu())
		}
	}
	if err := tt.ChooseMenuItem("获取选项…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, text := range []string{"获取远程", "清理远端已删除的分支", "同时清理远端已删除的标签", "取消"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in %q", text, tt.Texts())
		}
	}
	if !a.showFetch || a.fetch.repo != tab.path {
		t.Fatalf("dialog %+v show=%v", a.fetch, a.showFetch)
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("拉取或获取远程更新"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("获取远程更新"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.showFetch || !a.fetch.busy || tab.notice != "正在获取远程更新…" {
		t.Fatalf("quickFetch did not start directly: show=%v busy=%v notice=%q", a.showFetch, a.fetch.busy, tab.notice)
	}
	a.stopFetch()
}

func TestFetchIgnoresPreviousDialogRemotes(t *testing.T) {
	firstDir, firstGit := initTestRepo(t)
	firstGit("remote", "add", "first", firstDir)
	secondDir, secondGit := initTestRepo(t)
	secondGit("remote", "add", "second", secondDir)
	for _, name := range []string{"old-first", "old-last"} {
		t.Run(name, func(t *testing.T) {
			first := newTab(1, firstDir)
			second := newTab(2, secondDir)
			first.loaded, second.loaded = true, true
			updates := make(chan func(), 8)
			a := &app{tabs: []*repoTab{first, second}, dispatch: func(fn func()) { updates <- fn }}
			tt := ui.NewTester(a.view, 1100, 720)
			a.openFetch(first)
			oldUpdate := nextDialogUpdate(t, updates)
			tt.Frame()
			if err := tt.Click("取消"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			a.openFetch(second)
			newUpdate := nextDialogUpdate(t, updates)
			if name == "old-first" {
				oldUpdate()
				if !a.fetch.preparing || len(a.fetch.remotes) != 0 || a.fetch.err != "" {
					t.Fatalf("old result completed new fetch dialog: %+v", a.fetch)
				}
				newUpdate()
			} else {
				newUpdate()
				oldUpdate()
			}
			tt.Frame()
			tt.Frame()
			if a.fetch.repo != secondDir || a.fetch.preparing || len(a.fetch.remotes) != 1 || a.fetch.remotes[0] != "second" || a.fetch.err != "" {
				t.Fatalf("wrong remotes in current dialog: %+v", a.fetch)
			}
		})
	}
}

// 推送是顶栏的独立图标按钮：一次点击进确认弹窗，确认后才真的推。
func TestTopbarPushAsksBeforePushing(t *testing.T) {
	tab := graphTabForWrite()
	tab.hasUpstream = true
	tab.upstream = "origin/main"
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("推送当前分支"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.showPush || a.push.upstream != "origin/main" || tab.syncBusy {
		t.Fatalf("push button should ask first: show=%v dialog=%+v busy=%v", a.showPush, a.push, tab.syncBusy)
	}
	for _, text := range []string{"推送当前分支？", "main → origin/main"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in confirmation: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.showPush || tab.syncBusy {
		t.Fatalf("cancel should leave nothing running: show=%v busy=%v", a.showPush, tab.syncBusy)
	}
}
