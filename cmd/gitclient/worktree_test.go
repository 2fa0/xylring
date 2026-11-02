package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParsePorcelainZ(t *testing.T) {
	files := parsePorcelainZ([]byte(" M file.txt\x00?? new.txt\x00R  old.txt\x00renamed.txt\x00"))
	if len(files) != 3 {
		t.Fatalf("files %+v", files)
	}
	if files[0].path != "file.txt" || files[0].extra != "未暂存" || files[0].status != "M" {
		t.Fatalf("modified %+v", files[0])
	}
	if files[1].path != "new.txt" || files[1].extra != "未跟踪" || files[1].status != "A" {
		t.Fatalf("untracked %+v", files[1])
	}
	if files[2].oldPath != "old.txt" || files[2].path != "renamed.txt" || files[2].extra != "重命名" {
		t.Fatalf("rename %+v", files[2])
	}
}

func TestWorktreeShowsUncommittedAndKeepsGraph(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o600)
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading && tab.worktreeCount > 0 })
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("工作区 · 2 个文件") || !tt.HasText("图谱") {
		t.Fatalf("missing worktree entry: %q", tt.Texts())
	}
	if err := tt.Click("工作区 · 2 个文件"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return tab.worktree && !tab.compareLoading && !tab.diffLoading })
	tt.Frame()
	for _, text := range []string{"工作区 · 2 个文件", "未暂存  a.txt", "未跟踪  b.txt", "图谱", "base"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tab.worktree || tt.HasText("未暂存  a.txt") {
		t.Fatalf("Esc 没有离开工作区：%q", tt.Texts())
	}
}
