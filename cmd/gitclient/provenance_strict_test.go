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
		{"AI without model", "AI-Agent: Cindy", false},
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
		{"stamp without model", "AI-Agent: Cindy\nAI-Attribution: stamp", false},
		{"stamp", "AI-Agent: Cindy\nAI-Model: exact-model\nAI-Attribution: stamp", true},
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
				for _, instruction := range []string{"年轮错误", "提交说明模板", "git commit -m", "AI-Agent: human", "必须填写真实的 AI-Model", "git log -1 --format=%B", "不要使用 --no-verify"} {
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
	if err := writeStamp(ctx, dir, ringStamp{p: provenance{agent: "Cindy", model: "exact-model"}, expires: time.Now().Add(time.Minute).Unix()}); err != nil {
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
	git("commit", "--allow-empty", "-m", "valid tip\n\nAI-Agent: Cindy\nAI-Model: exact-model")
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

func TestProvenanceModelRequiredOnWrite(t *testing.T) {
	ctx := context.Background()
	dir, _ := ringTestRepo(t)
	for _, tc := range []struct {
		name  string
		p     provenance
		valid bool
	}{
		{"AI missing model", provenance{agent: "Cindy"}, false},
		{"AI blank model", provenance{agent: "Cindy", model: "  "}, false},
		{"AI with model", provenance{agent: "Cindy", model: "exact-model"}, true},
		{"human", provenance{agent: "human"}, true},
		{"human with model", provenance{agent: "human", model: "exact-model"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := applyProvenanceTrailers(ctx, dir, "change", tc.p); (err == nil) != tc.valid {
				t.Fatalf("GUI write: valid=%t, err=%v", tc.valid, err)
			}
			if err := writeStamp(ctx, dir, ringStamp{p: tc.p, expires: time.Now().Add(time.Minute).Unix()}); (err == nil) != tc.valid {
				t.Fatalf("stamp write: valid=%t, err=%v", tc.valid, err)
			}
		})
	}
	var out bytes.Buffer
	if err := runRingCLI(ctx, dir, []string{"stamp", "--agent", "Cindy"}, &out); err == nil {
		t.Fatal("CLI accepted an AI stamp without a model")
	}
}

func TestLegacyStampWithoutModelIsIgnored(t *testing.T) {
	ctx := context.Background()
	dir, git := ringTestRepo(t)
	if err := installRingHook(ctx, dir); err != nil {
		t.Fatal(err)
	}
	path, err := gitFile(ctx, dir, "xylring-stamp")
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte("agent=Cindy\nmodel=\nexpires=9999999999\nonce=1\n")
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	git("commit", "--allow-empty", "-m", "legacy stamp")
	if p := parseProvenance(string(git("log", "-1", "--format=%B"))); p.agent != "" {
		t.Fatalf("legacy stamp applied: %+v", p)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, legacy) {
		t.Fatalf("ignored stamp was consumed: %v", err)
	}
}

func TestProvenanceHookUpgrade(t *testing.T) {
	ctx := context.Background()
	dir, _ := ringTestRepo(t)
	path, err := provenanceHookPath(ctx, dir, "commit-msg")
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(path, original, 0755); err != nil {
		t.Fatal(err)
	}
	oldScript := "#!/bin/sh\n" + strictHookMarker + "\nexit 0\n"
	if err := installProvenanceHook(ctx, dir, "commit-msg", strictHookMarker, oldScript); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := installProvenanceHook(ctx, dir, "commit-msg", strictHookMarker, strictRingScript); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != strictRingScript {
		t.Fatalf("hook not upgraded: %v", err)
	}
	if err := uninstallProvenanceHook(ctx, dir, "commit-msg", strictHookMarker); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("original hook not preserved: %v", err)
	}
}
