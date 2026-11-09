package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStrictProvenanceCommits(t *testing.T) {
	dir, git := ringTestRepo(t)
	ctx := context.Background()
	var output bytes.Buffer
	if err := runRingCLI(ctx, dir, []string{"ring", "strict", "install"}, &output); err != nil {
		t.Fatal(err)
	}
	git("commit", "--allow-empty", "-m", "baseline\n\nAI-Agent: human")
	for _, tc := range []struct {
		name, trailers string
		valid          bool
	}{
		{"missing", "", false},
		{"native only", "Assisted-by: Cindy (https://cindy.app)", false},
		{"AI without model", "AI-Agent: Cindy", true},
		{"AI with model", "AI-Agent: Cindy\nAI-Model: exact-model", true},
		{"case insensitive", "ai-agent: Codex\nai-model: exact-model", true},
		{"human", "AI-Agent: human", true},
		{"human coauthor", "AI-Agent: human\nCo-authored-by: Ada <ada@example.com>", true},
		{"empty agent", "AI-Agent:   ", false},
		{"empty model", "AI-Agent: Cindy\nAI-Model:", false},
		{"duplicate agent", "AI-Agent: Cindy\nai-agent: Codex", false},
		{"duplicate model", "AI-Agent: Cindy\nAI-Model: first\nAI-Model: second", false},
		{"placeholder", "AI-Agent: <实际Agent产品名>", false},
		{"unknown model", "AI-Agent: Cindy\nAI-Model: unknown", false},
		{"human model", "AI-Agent: human\nAI-Model: exact-model", false},
		{"human stamp", "AI-Agent: human\nAI-Attribution: stamp", false},
		{"false human", "AI-Agent: human\nAssisted-by: Cindy (https://cindy.app)", false},
		{"stamp", "AI-Agent: Cindy\nAI-Attribution: stamp", true},
		{"invalid source", "AI-Agent: Cindy\nAI-Attribution: nonsense", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := string(git("rev-parse", "HEAD"))
			cmd := exec.Command("git", gitArgs("commit", "--allow-empty", "-m", "change\n\n"+tc.trailers)...)
			cmd.Dir = dir
			cmd.Env = mergeGitEnv([]string{"XYLRING_GUI=1", "XYLRING_RING=0"})
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v\n%s", tc.valid, err, out)
			}
			if !tc.valid {
				if string(git("rev-parse", "HEAD")) != before {
					t.Fatal("rejected commit changed HEAD")
				}
				for _, instruction := range []string{"年轮错误", "提交说明模板", "git commit -m", "AI-Agent: human", "模型名确知", "git log -1 --format=%B", "不要使用 --no-verify"} {
					if !strings.Contains(string(out), instruction) {
						t.Fatalf("missing guidance %q: %s", instruction, out)
					}
				}
			}
		})
	}
	if _, err := runGit(ctx, dir, "commit", "--amend", "--allow-empty", "-m", "missing on amend"); err == nil {
		t.Fatal("amend bypassed strict check")
	}
	// A stamp is still usable, but a native signature alone cannot bypass strict checks.
	if err := installRingHook(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := writeStamp(ctx, dir, ringStamp{p: provenance{agent: "Cindy"}, expires: time.Now().Add(time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	git("commit", "--allow-empty", "-m", "stamped")
	if _, err := runGit(ctx, dir, "commit", "--allow-empty", "-m", "native\n\nAssisted-by: Cindy (https://cindy.app)"); err == nil {
		t.Fatal("native signature bypassed strict check")
	}
}

func TestStrictProvenanceRangeAndHooks(t *testing.T) {
	dir, git := ringTestRepo(t)
	ctx := context.Background()
	git("commit", "--allow-empty", "-m", "unmarked old history")
	base := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	path, _ := gitFile(ctx, dir, "hooks/commit-msg")
	original := []byte("#!/bin/sh\nexit 23\n")
	if err := os.WriteFile(path, original, 0755); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	for i := 0; i < 2; i++ {
		if err := runRingCLI(ctx, dir, []string{"ring", "strict", "install"}, &output); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", path, "unused")
	cmd.Dir = dir
	if err := cmd.Run(); err == nil || err.(*exec.ExitError).ExitCode() != 23 {
		t.Fatalf("original hook status lost: %v", err)
	}
	if err := runRingCLI(ctx, dir, []string{"ring", "strict", "uninstall"}, &output); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
		t.Fatal("original hook not restored")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := runRingCLI(ctx, dir, []string{"ring", "strict", "install"}, &output); err != nil {
		t.Fatal(err)
	}
	git("commit", "--allow-empty", "-m", "valid\n\nAI-Agent: human")
	check := func(wantOK bool) {
		t.Helper()
		cmd := exec.Command("sh", path, "--range", base+"..HEAD")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("range: %v\n%s", err, out)
		}
		if !wantOK && !strings.Contains(string(out), "不合规提交：") {
			t.Fatalf("missing failing commit: %s", out)
		}
	}
	check(true) // Historical unmarked commits are outside the checked range.
	git("-c", "core.hooksPath="+t.TempDir(), "commit", "--allow-empty", "-m", "bypassed locally")
	git("commit", "--allow-empty", "-m", "valid tip\n\nAI-Agent: Cindy")
	check(false) // An unmarked intermediate commit must still fail CI.
	git("config", "core.hooksPath", ".managed-hooks")
	if err := runRingCLI(ctx, dir, []string{"ring", "strict", "install"}, &output); err == nil {
		t.Fatal("managed hooks path overwritten")
	}
	cmd = exec.Command("sh", path, filepath.Join(dir, "missing-message"))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "提交说明模板") {
		t.Fatalf("unreadable message did not fail with guidance: %v %s", err, out)
	}
}

func TestHumanProvenanceDisplay(t *testing.T) {
	pool := &provenanceTable{}
	p := pool.get(pool.intern(provenance{agent: "human"}))
	if p.badge != "人工" || p.description() != "人工（显式）" {
		t.Fatalf("%+v: %s", p, p.description())
	}
}
