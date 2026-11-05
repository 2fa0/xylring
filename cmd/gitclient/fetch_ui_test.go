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
	if err := tt.Click("获取选项"); err != nil {
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
	if err := tt.Click("获取"); err != nil {
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
