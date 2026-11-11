package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCommitDetailsAndClipboard(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "说明.txt"), []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "--author=Ada <ada@example.com>", "-m", "主题", "-m", "第一段\n第二行\n\n最后一段")
	ctx := context.Background()
	hash, _ := resolveCommit(ctx, dir, "HEAD")
	details, err := readCommitDetails(ctx, dir, hash)
	if err != nil || details.author != "Ada" || details.authorEmail != "ada@example.com" || details.committer != "Test" || details.parents != "" || !strings.Contains(details.message, "第一段\n第二行\n\n最后一段") {
		t.Fatalf("details %+v, %v", details, err)
	}
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.selectCommit(tab, 0)
	waitUpdates(t, updates, func() bool { return !tab.compareLoading && !tab.diffLoading })
	tt := ui.NewTester(a.view, 1100, 900)
	tt.Frame()
	if err := tt.Click("复制哈希"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != hashHex(hash) {
		t.Fatalf("copied short hash: %q", tt.Clipboard())
	}
	clickFileAction(t, tt, tab, "复制路径")
	if tt.Clipboard() != "说明.txt" {
		t.Fatalf("wrong path: %q", tt.Clipboard())
	}
	hoverSelectedFile(t, tt, tab)
	if err := tt.ClickWith(ui.Alt, "复制路径"); err != nil {
		t.Fatal(err)
	}
	wantAbs, err := filepath.Abs(filepath.Join(dir, "说明.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != wantAbs {
		t.Fatalf("wrong abs path: %q", tt.Clipboard())
	}
	if err := tt.Click("完整信息"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.detailsLoading })
	tt.Frame()
	if err := tt.Click("复制完整信息"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != details.text() {
		t.Fatalf("wrong metadata: %q", tt.Clipboard())
	}
	if err := tt.Click("收起信息"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("复制完整信息") || tab.detailsOpen {
		t.Fatal("metadata remained open")
	}
}
