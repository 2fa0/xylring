package radar

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestPulseStoreRoundTripExpireAndDismiss(t *testing.T) {
	t.Setenv(EnvDirKey, t.TempDir())
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	s1, err := WritePulse(PulseInput{
		ID:      "sess-1",
		State:   StateWaiting,
		Agent:   "Codex",
		Model:   "GPT-5",
		Repo:    "/tmp/demo-repo",
		Summary: "Approve migration?",
		TTL:     10 * time.Minute,
		Now:     now,
	})
	if err != nil {
		t.Fatalf("WritePulse s1: %v", err)
	}
	if _, err := WritePulse(PulseInput{
		ID:      "sess-expired",
		State:   StateRunning,
		Agent:   "Gemini CLI",
		Repo:    "/tmp/demo-repo-2",
		TTL:     1 * time.Minute,
		Now:     now,
	}); err != nil {
		t.Fatalf("WritePulse expired: %v", err)
	}

	got, err := ListPulses(now.Add(5 * time.Minute))
	if err != nil {
		t.Fatalf("ListPulses: %v", err)
	}
	if len(got) != 1 || got[0].ID != s1.ID || got[0].State != StateWaiting {
		t.Fatalf("unexpected pulses: %+v", got)
	}

	// Dismissing hides the session until a newer update arrives.
	if err := DismissSession(s1.ID, now.Add(1*time.Minute)); err != nil {
		t.Fatalf("DismissSession: %v", err)
	}
	snap := CollectSnapshot(context.Background(), ScanOptions{
		Now:       now.Add(2 * time.Minute),
		SkipTmux:  true,
		SkipCodex: true,
	})
	if len(snap.Sessions) != 0 {
		t.Fatalf("expected dismissed session hidden, got %+v", snap.Sessions)
	}

	// A newer pulse reactivates it.
	if _, err := WritePulse(PulseInput{
		ID:      "sess-1",
		State:   StateReview,
		Agent:   "Codex",
		Repo:    "/tmp/demo-repo",
		Summary: "Done",
		Now:     now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatalf("WritePulse update: %v", err)
	}
	snap = CollectSnapshot(context.Background(), ScanOptions{
		Now:       now.Add(4 * time.Minute),
		SkipTmux:  true,
		SkipCodex: true,
	})
	if len(snap.Sessions) != 1 || snap.Sessions[0].State != StateReview {
		t.Fatalf("expected updated session to reappear, got %+v", snap.Sessions)
	}

	if err := ClearPulse(s1.ID); err != nil {
		t.Fatalf("ClearPulse: %v", err)
	}
	got, err = ListPulses(now.Add(4 * time.Minute))
	if err != nil || len(got) != 0 {
		t.Fatalf("expected empty after clear, got %+v err=%v", got, err)
	}
}

func TestParseCodexNotifyPayload(t *testing.T) {
	raw := []byte(`{"type":"agent-turn-complete","thread-id":"abc-123","cwd":"/tmp/demo-repo","model":"gpt-5-codex","last-assistant-message":"Should I apply the schema change?"}`)
	in, err := ParseCodexNotifyPayload(raw)
	if err != nil {
		t.Fatalf("ParseCodexNotifyPayload: %v", err)
	}
	if in.ID != "codex-abc-123" || in.State != StateWaiting || in.Agent != "Codex" {
		t.Fatalf("unexpected parsed notify: %+v", in)
	}
}

func TestCollectCodexSessions(t *testing.T) {
	codexHome := t.TempDir()
	sessDir := filepath.Join(codexHome, "sessions", "2026", "11", "28")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	writeFile := func(name, content string, mtime time.Time) {
		p := filepath.Join(sessDir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("rollout-running.jsonl",
		`{"timestamp":"2026-11-28T11:55:00Z","type":"session_meta","payload":{"id":"run-1","cwd":"/tmp/demo-a","model":"gpt-5-codex"}}`+"\n"+
			`{"timestamp":"2026-11-28T11:59:55Z","type":"event_msg","payload":{"type":"agent_message","message":"Running unit tests"}}`+"\n",
		now.Add(-3*time.Second),
	)
	writeFile("rollout-waiting.jsonl",
		`{"timestamp":"2026-11-28T11:50:00Z","type":"session_meta","payload":{"id":"wait-1","cwd":"/tmp/demo-b"}}`+"\n"+
			`{"timestamp":"2026-11-28T11:52:00Z","type":"event_msg","payload":{"type":"agent_message","message":"Proceed with deleting old files?"}}`+"\n",
		now.Add(-2*time.Minute),
	)
	writeFile("rollout-error.jsonl",
		`{"timestamp":"2026-11-28T11:40:00Z","type":"session_meta","payload":{"id":"err-1","cwd":"/tmp/demo-c"}}`+"\n"+
			`{"timestamp":"2026-11-28T11:41:00Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"Permission denied"}}`+"\n",
		now.Add(-5*time.Minute),
	)

	got := CollectCodexSessions(context.Background(), codexHome, now)
	if len(got) != 3 {
		t.Fatalf("expected 3 codex sessions, got %d: %+v", len(got), got)
	}
	byID := map[string]Session{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if byID["codex-run-1"].State != StateRunning || byID["codex-run-1"].Model != "gpt-5-codex" {
		t.Fatalf("unexpected running session: %+v", byID["codex-run-1"])
	}
	if byID["codex-wait-1"].State != StateWaiting {
		t.Fatalf("unexpected waiting session: %+v", byID["codex-wait-1"])
	}
	if byID["codex-err-1"].State != StateError || byID["codex-err-1"].Summary != "Permission denied" {
		t.Fatalf("unexpected error session: %+v", byID["codex-err-1"])
	}
}

func TestCollectTmuxSessionsAndMergeOverlapSorting(t *testing.T) {
	t.Setenv(EnvDirKey, t.TempDir())
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("ANTIGRAVITY_AGENT", "")
	t.Setenv("ANTIGRAVITY_CLI_ALIAS", "")
	t.Setenv("CURSOR_TRACE_ID", "")
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	origTmux := runTmuxCmd
	t.Cleanup(func() { runTmuxCmd = origTmux })
	runTmuxCmd = func(ctx context.Context, args ...string) (string, error) {
		return fmt.Sprintf(
			"%%1\twork:1.0\tcodex\t/tmp/wt-auth\tcodex\t%d\t0\t0\n"+
				"%%2\twork:1.1\tzsh\t/tmp/wt-other\tGemini CLI\t%d\t0\t0\n"+
				"%%3\twork:1.2\tclaude\t/tmp/wt-err\tclaude\t%d\t1\t1\n",
			now.Add(-2*time.Second).Unix(),
			now.Add(-3*time.Minute).Unix(),
			now.Add(-1*time.Minute).Unix(),
		), nil
	}

	// Write a pulse for /tmp/wt-auth Codex that is waiting; tmux %1 provides the pane focus target.
	if _, err := WritePulse(PulseInput{
		ID:      "pulse-auth",
		State:   StateWaiting,
		Agent:   "Codex",
		Repo:    "/tmp/wt-auth",
		Summary: "Need approval for auth.go",
		Now:     now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	snap := CollectSnapshot(context.Background(), ScanOptions{
		Now:       now,
		SkipCodex: true,
		Worktrees: []WorktreeHint{
			{Path: "/tmp/wt-auth", CommonDir: "/tmp/demo-repo/.git", Branch: "feat/auth", DirtyFiles: []string{"auth.go", "shared.go"}},
			{Path: "/tmp/wt-other", CommonDir: "/tmp/demo-repo/.git", Branch: "feat/other", DirtyFiles: []string{"shared.go"}},
			{Path: "/tmp/wt-dirty", CommonDir: "/tmp/demo-repo/.git", Branch: "feat/dirty", DirtyFiles: []string{"README.md"}},
		},
	})

	if snap.AttentionCount != 4 || snap.RunningCount != 0 {
		t.Fatalf("unexpected counts: attention=%d running=%d sessions=%+v", snap.AttentionCount, snap.RunningCount, snap.Sessions)
	}
	// Waiting sessions come first, oldest waiting first (pulse-auth -5m before tmux %2 -3m).
	if snap.Sessions[0].ID != "pulse-auth" || snap.Sessions[0].Target.TmuxPane != "%1" {
		t.Fatalf("expected pulse-auth enriched with tmux %%1 first, got %+v", snap.Sessions[0])
	}
	if !slices.Equal(snap.Sessions[0].OverlapFiles, []string{"shared.go"}) {
		t.Fatalf("expected overlap shared.go, got %+v", snap.Sessions[0].OverlapFiles)
	}
	if snap.Sessions[1].ID != "tmux-%2" || snap.Sessions[1].State != StateWaiting {
		t.Fatalf("expected tmux %%2 second, got %+v", snap.Sessions[1])
	}
	if snap.Sessions[2].ID != "tmux-%3" || snap.Sessions[2].State != StateError {
		t.Fatalf("expected dead tmux %%3 error third, got %+v", snap.Sessions[2])
	}
	if snap.Sessions[3].Source != SourceWorktree || snap.Sessions[3].State != StateReview {
		t.Fatalf("expected dirty worktree fallback fourth, got %+v", snap.Sessions[3])
	}
}
