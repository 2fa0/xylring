package app

import (
	"context"
	"testing"
	"time"

	"github.com/2fa0/xylring/internal/radar"
	"github.com/egoist/mygo/ui"
)

func TestRadarUINavigationFilterAndActions(t *testing.T) {
	t.Setenv(radar.EnvDirKey, t.TempDir())
	repoA, _ := initTestRepo(t)
	updates := make(chan func(), 64)

	tab := newTab(1, repoA)
	tab.loaded, tab.branch = true, "main"
	tab.commits = []commit{{hash: hashN(1), author: "Ada", subject: "Initial commit", when: 1700000000}}
	tab.graph.add(&tab.commits[0])

	now := time.Now().UTC()
	dismissed := map[string]bool{}

	a := &app{
		tabs:            []*repoTab{tab},
		bottomFullWidth: true,
		dispatch:        func(fn func()) { updates <- fn },
		radar: radarState{
			scanFn: func(ctx context.Context, opts radar.ScanOptions) radar.Snapshot {
				var sessions []radar.Session
				if !dismissed["s-wait"] {
					sessions = append(sessions, radar.Session{
						ID:           "s-wait",
						Source:       radar.SourcePulse,
						State:        radar.StateWaiting,
						Agent:        "Codex",
						Model:        "gpt-5-codex",
						Repo:         repoA,
						Branch:       "feat/auth",
						Summary:      "Need approval for auth.go",
						ChangedFiles: 2,
						OverlapWith:  []string{"feat/other"},
						OverlapFiles: []string{"shared.go"},
						Target:       radar.FocusTarget{Kind: "tmux", TmuxPane: "%1", TmuxTarget: "work:1.0"},
						UpdatedAt:    now.Add(-2 * time.Minute),
					})
				}
				sessions = append(sessions, radar.Session{
					ID:        "s-run",
					Source:    radar.SourceTmux,
					State:     radar.StateRunning,
					Agent:     "Gemini CLI",
					Repo:      "/tmp/demo-other",
					Branch:    "feat/ui",
					Summary:   "Running test suite",
					Target:    radar.FocusTarget{Kind: "tmux", TmuxPane: "%2", TmuxTarget: "work:1.1"},
					UpdatedAt: now.Add(-4 * time.Second),
				})
				var att, run int
				for _, s := range sessions {
					if s.State.NeedsAttention() {
						att++
					} else if s.State == radar.StateRunning {
						run++
					}
				}
				return radar.Snapshot{
					Sessions:       sessions,
					AttentionCount: att,
					RunningCount:   run,
					UpdatedAt:      now,
				}
			},
		},
	}
	a.pollRadar(true)

	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	defer tab.cancel()

	tt.Frame()
	drainUpdates(updates)

	// Sidebar shows Agent Radar entry with pending badge "1".
	if !tt.HasText("Agent 雷达") || !tt.HasText("1") {
		t.Fatalf("expected sidebar Agent 雷达 and attention badge 1, texts=%q", tt.Texts())
	}

	// Open Agent Radar.
	if err := tt.Click("Agent 雷达"); err != nil {
		t.Fatalf("click Agent 雷达: %v", err)
	}
	tt.Frame()
	if !a.showRadar {
		t.Fatal("expected showRadar=true")
	}
	if !tt.HasText("Need approval for auth.go") || !tt.HasText("Running test suite") {
		t.Fatalf("expected both sessions rendered, texts=%q", tt.Texts())
	}
	if !tt.HasText("与 feat/other 重叠 1 文件") {
		t.Fatalf("expected overlap badge rendered, texts=%q", tt.Texts())
	}

	// Filter to Running only.
	if err := tt.Click("运行中"); err != nil {
		t.Fatalf("click 运行中: %v", err)
	}
	tt.Frame()
	if tt.HasText("Need approval for auth.go") || !tt.HasText("Running test suite") {
		t.Fatalf("expected running filter to hide waiting session, texts=%q", tt.Texts())
	}

	// Switch back to All.
	if err := tt.Click("全部"); err != nil {
		t.Fatalf("click 全部: %v", err)
	}
	tt.Frame()

	// Search filter.
	a.radar.search = "approval"
	tt.Frame()
	if !tt.HasText("Need approval for auth.go") || tt.HasText("Running test suite") {
		t.Fatalf("expected search filter to narrow results, texts=%q", tt.Texts())
	}
	a.radar.search = ""
	tt.Frame()

	// Open Hook modal and switch tabs.
	if err := tt.Click("接入 Hook"); err != nil {
		t.Fatalf("click 接入 Hook: %v", err)
	}
	tt.Frame()
	if !a.radar.showHookModal || !tt.HasText("接入 Agent Hook") {
		t.Fatalf("expected hook modal open, texts=%q", tt.Texts())
	}
	if err := tt.Click("Claude Code"); err != nil {
		t.Fatalf("click Claude Code tab: %v", err)
	}
	tt.Frame()
	if a.radar.hookTab != 1 {
		t.Fatalf("expected hookTab=1, got %d", a.radar.hookTab)
	}
	if err := tt.Click("完成"); err != nil {
		t.Fatalf("click 完成: %v", err)
	}
	tt.Frame()
	if a.radar.showHookModal {
		t.Fatal("expected hook modal closed")
	}

	// Dismiss waiting session.
	dismissed["s-wait"] = true
	if err := tt.Click("标记已处理"); err != nil {
		t.Fatalf("click 标记已处理: %v", err)
	}
	tt.Frame()
	if a.radar.snapshot.AttentionCount != 0 || tt.HasText("Need approval for auth.go") {
		t.Fatalf("expected dismissed session removed, att=%d texts=%q", a.radar.snapshot.AttentionCount, tt.Texts())
	}

	// Reset and click 查看改动 to jump back to repo.
	dismissed["s-wait"] = false
	a.pollRadar(true)
	tt.Frame()
	if err := tt.Click("查看改动"); err != nil {
		t.Fatalf("click 查看改动: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.showRadar && time.Now().Before(deadline) {
		select {
		case fn := <-updates:
			fn()
		case <-time.After(10 * time.Millisecond):
		}
	}
	tt.Frame()
	if a.showRadar {
		t.Fatal("expected showRadar=false after clicking 查看改动")
	}
	drainUpdates(updates)
}
