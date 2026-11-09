package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestOmniSearchShortcutsAndSuggestions(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "initial commit")
	git("checkout", "-b", "feature-xyz")
	git("commit", "--allow-empty", "-m", "feature commit")
	git("tag", "v1.0.0")
	git("checkout", "main")

	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// 1. Shortcut Cmd+P focuses search and opens suggestions
	tt.Key(commandModifier(), ui.KeyP)
	tt.Frame()
	if !tab.searchSuggestOpen {
		t.Fatal("Cmd+P did not open suggestions")
	}

	// 2. Type "feature" and verify branch suggestion appears
	tt.Type("feature")
	tt.Frame()
	if !tt.HasText("feature-xyz") {
		t.Fatalf("suggestion for feature-xyz not shown: %q", tt.Texts())
	}

	// 3. Click the suggestion to locate
	if err := tt.Click("feature-xyz"); err != nil {
		t.Fatalf("failed to click branch suggestion: %v", err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if tab.searchSuggestOpen {
		t.Fatal("suggestions should close after selection")
	}
	if tab.selected < 0 || tab.commits[tab.selected].subject != "feature commit" {
		t.Fatalf("did not locate feature-xyz commit: selected=%d", tab.selected)
	}

	retireTester(tt)
}

func TestOmniSearchAutoRecognition(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "first commit", "--author=Alice <alice@example.com>")
	git("commit", "--allow-empty", "-m", "second needle commit", "--author=Bob <bob@example.com>")

	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// 1. Type "author:Bob" and press Enter -> should auto-switch to author search
	tt.Key(commandModifier(), ui.KeyK)
	tt.Type("author:Bob")
	tt.Key(0, ui.KeyEnter)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if tab.query.term != "Bob" || tab.query.kind != "作者" {
		t.Fatalf("auto author search failed: query=%+v", tab.query)
	}
	if len(tab.commits) != 1 || tab.commits[0].author != "Bob" {
		t.Fatalf("wrong commits returned for author search: %v", tab.commits)
	}

	// Return to full history
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()

	// 2. Select all and type general keyword "needle" -> should search commit description
	tt.Key(commandModifier(), ui.KeyF)
	tt.Key(commandModifier(), ui.KeyA)
	tt.Type("needle")
	tt.Key(0, ui.KeyEnter)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()
	if tab.query.term != "needle" || tab.query.kind != "说明" {
		t.Fatalf("auto description search failed: query=%+v", tab.query)
	}
	if len(tab.commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(tab.commits))
	}

	retireTester(tt)
}

func TestSearchFilterBannerAndPreserveGraphColumn(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base commit")
	git("commit", "--allow-empty", "-m", "special search target")

	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// Start search
	tab.searchText = "special"
	a.searchHistory(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()

	// Verify "图谱" column is still present (prevents column jumping/flashing)
	if !tt.HasText("图谱") {
		t.Fatal("图谱 column was hidden during search, causing layout flash")
	}

	// Verify search filter banner is shown with text and clear button
	if !tt.HasText("正在按说明筛选") || !tt.HasText("special") {
		t.Fatalf("search banner missing or incomplete: %q", tt.Texts())
	}
	if !tt.HasText("退出筛选") {
		t.Fatal("search banner missing exit button")
	}

	// Click "退出筛选" on the banner
	if err := tt.Click("退出筛选"); err != nil {
		t.Fatalf("failed to click 退出筛选: %v", err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()

	// Verify restored to full history
	if tab.query.term != "" || len(tab.commits) != 2 {
		t.Fatalf("history not restored: query=%+v commits=%d", tab.query, len(tab.commits))
	}
	if !tt.HasText("图谱") {
		t.Fatal("图谱 column missing after restore")
	}

	retireTester(tt)
}

func TestOmniSearchCodeChanges(t *testing.T) {
	dir, git := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc secretAlgorithm() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.go")
	git("commit", "-m", "add secret algorithm")

	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package main\n\nfunc otherAlgorithm() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "b.go")
	git("commit", "-m", "add other algorithm")

	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// 1. Search code changes via prefix "code:secretAlgorithm"
	tt.Key(commandModifier(), ui.KeyP)
	tt.Type("code:secretAlgorithm")
	tt.Key(0, ui.KeyEnter)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()

	if tab.query.term != "secretAlgorithm" || tab.query.kind != "代码改动" {
		t.Fatalf("expected code search query, got %+v", tab.query)
	}
	if len(tab.commits) != 1 || tab.commits[0].subject != "add secret algorithm" {
		t.Fatalf("expected 1 commit matching secretAlgorithm, got %v", tab.commits)
	}
	if !tt.HasText("正在按代码改动筛选") || !tt.HasText("secretAlgorithm") {
		t.Fatalf("search banner for code change missing: %v", tt.Texts())
	}

	// 2. Return to full history
	if err := tt.Click("退出筛选"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()

	// 3. Search via dropdown suggestion for code change
	tt.Key(commandModifier(), ui.KeyP)
	tt.Type("secretAlgorithm")
	tt.Frame()
	if !tt.HasText("在代码改动中搜索 “secretAlgorithm”") {
		t.Fatalf("code search suggestion not shown: %v", tt.Texts())
	}
	if err := tt.Click("在代码改动中搜索 “secretAlgorithm”"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tt.Frame()

	if tab.query.term != "secretAlgorithm" || tab.query.kind != "代码改动" {
		t.Fatalf("suggestion click did not set code search query: %+v", tab.query)
	}
	if len(tab.commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(tab.commits))
	}

	retireTester(tt)
}
