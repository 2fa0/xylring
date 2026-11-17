package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestMotionTokensStayQuiet(t *testing.T) {
	if colorMotion().Duration != 120*time.Millisecond {
		t.Fatal("hover color shifts should stay under 120ms")
	}
	if paneMotion().Duration != 180*time.Millisecond {
		t.Fatal("layout motion should stay under 180ms")
	}
	fade := fadeEnter()
	if fade.Enter == nil || fade.Enter.Opacity != 0 || fade.Exit != nil {
		t.Fatal("page changes should fade in without an exit ghost")
	}
	if workspaceMainMotion().Match == nil || commitGraphMotion(1).Match == nil {
		t.Fatal("pane layout changes need a match identity")
	}
}

func TestHomeGraphSwitchKeepsLabels(t *testing.T) {
	tab := newTab(1, "/tmp/work/myrepo")
	tab.loaded = true
	tab.branch = "main"
	a := &app{tabs: []*repoTab{tab}, showHome: true, recent: []string{"/tmp/work/myrepo"}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("最近的仓库") {
		t.Fatalf("home faded away: %q", tt.Texts())
	}
	a.showHome = false
	tt.Frame()
	if !tt.HasText("图谱") && !tt.HasText("仓库") {
		t.Fatalf("graph page missing after fade-in: %q", tt.Texts())
	}
}
