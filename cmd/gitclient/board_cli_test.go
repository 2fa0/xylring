package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoardCLIEndToEndWithGitRepo(t *testing.T) {
	withTempBoardConfig(t)
	repoA, gitA := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repoA, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitA("add", "a.txt")
	gitA("commit", "-m", "feat: add board core\n\nAI-Agent: Claude Code\nAI-Model: Claude Opus 4")

	repoB, gitB := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repoB, "b.txt"), []byte("world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitB("add", "b.txt")
	gitB("commit", "-m", "fix: sync worker")

	ctx := context.Background()
	var out bytes.Buffer

	if err := runBoardCLI(ctx, repoA, []string{"add", "打通多仓库联动", "--desc", "需求说明", "--json"}, &out); err != nil {
		t.Fatalf("board add: %v", err)
	}
	var created boardTask
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal add json: %v (%s)", err, out.String())
	}
	if created.ID != "T-1" || created.Status != boardTodo {
		t.Fatalf("unexpected created task: %+v", created)
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"update", "T-1", "--status", "doing"}, &out); err != nil {
		t.Fatalf("board update doing: %v", err)
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"link", "T-1", "HEAD"}, &out); err != nil {
		t.Fatalf("board link repoA: %v", err)
	}
	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"link", "T-1", "HEAD", "--repo", repoB, "--json"}, &out); err != nil {
		t.Fatalf("board link repoB: %v", err)
	}
	var linked boardTask
	if err := json.Unmarshal(out.Bytes(), &linked); err != nil {
		t.Fatalf("unmarshal link json: %v", err)
	}
	if len(linked.Commits) != 2 || len(linked.Repos) != 2 {
		t.Fatalf("expected 2 commits and 2 repos, got commits=%d repos=%v", len(linked.Commits), linked.Repos)
	}
	if linked.Commits[0].Agent != "Claude Code" || linked.Commits[0].Model != "Claude Opus 4" {
		t.Fatalf("expected provenance captured on link, got %+v", linked.Commits[0])
	}
	if linked.Commits[0].Subject != "feat: add board core" || linked.Commits[0].Branch != "main" {
		t.Fatalf("expected subject and branch snapshot, got %+v", linked.Commits[0])
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"note", "T-1", "两端都已提交", "--agent", "Codex", "--model", "GPT-5"}, &out); err != nil {
		t.Fatalf("board note: %v", err)
	}
	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"update", "T-1", "--done"}, &out); err != nil {
		t.Fatalf("board update --done: %v", err)
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"list", "--status", "done", "--repo", repoB}, &out); err != nil {
		t.Fatalf("board list: %v", err)
	}
	if !strings.Contains(out.String(), "T-1") || !strings.Contains(out.String(), "打通多仓库联动") {
		t.Fatalf("unexpected list output: %q", out.String())
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"show", "T-1"}, &out); err != nil {
		t.Fatalf("board show: %v", err)
	}
	if !strings.Contains(out.String(), "Claude Code") || !strings.Contains(out.String(), "两端都已提交") {
		t.Fatalf("unexpected show output: %q", out.String())
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"snippet"}, &out); err != nil {
		t.Fatalf("board snippet: %v", err)
	}
	if !strings.Contains(out.String(), "xylring board link") {
		t.Fatalf("unexpected snippet: %q", out.String())
	}
}
