package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/2fa0/xylring/internal/radar"
)

func TestPulseCLIWorkflow(t *testing.T) {
	t.Setenv(radar.EnvDirKey, t.TempDir())
	repo, _ := initTestRepo(t)
	ctx := context.Background()

	var out bytes.Buffer
	if err := runPulseCLI(ctx, repo, []string{
		"--session", "cli-1",
		"--agent", "Gemini CLI",
		"--model", "gemini-2.5",
		"--state", "waiting",
		"--summary", "Approve tool execution?",
		"--json",
	}, &out); err != nil {
		t.Fatalf("pulse write: %v", err)
	}
	var written radar.Session
	if err := json.Unmarshal(out.Bytes(), &written); err != nil {
		t.Fatalf("unmarshal pulse json: %v", err)
	}
	if written.ID != "cli-1" || written.State != radar.StateWaiting || written.Repo != canonicalRepoDir(repo) {
		t.Fatalf("unexpected written session: %+v", written)
	}

	out.Reset()
	notifyPayload := `{"type":"agent-turn-complete","thread-id":"t-42","cwd":` + `"` + repo + `"` + `,"last-assistant-message":"Finished refactor"}`
	if err := runPulseCLI(ctx, repo, []string{"--from-codex-notify", notifyPayload}, &out); err != nil {
		t.Fatalf("pulse codex notify: %v", err)
	}

	out.Reset()
	if err := runPulseCLI(ctx, repo, []string{"list", "--json", "--no-tmux"}, &out); err != nil {
		t.Fatalf("pulse list: %v", err)
	}
	var snap radar.Snapshot
	if err := json.Unmarshal(out.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal list json: %v", err)
	}
	if snap.AttentionCount < 2 {
		t.Fatalf("expected at least 2 attention sessions, got %+v", snap)
	}

	out.Reset()
	if err := runPulseCLI(ctx, repo, []string{"hook", "codex"}, &out); err != nil {
		t.Fatalf("pulse hook: %v", err)
	}
	if !strings.Contains(out.String(), "--from-codex-notify") {
		t.Fatalf("unexpected hook snippet: %q", out.String())
	}

	if err := runPulseCLI(ctx, repo, []string{"clear", "--all"}, &out); err != nil {
		t.Fatalf("pulse clear --all: %v", err)
	}
	out.Reset()
	if err := runPulseCLI(ctx, repo, []string{"list", "--json", "--no-tmux"}, &out); err != nil {
		t.Fatalf("pulse list after clear: %v", err)
	}
}
