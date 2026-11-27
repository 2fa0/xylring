package gitx

import (
	"strings"
	"testing"
	"time"
)

func TestMergeGitEnvOverridesDefaults(t *testing.T) {
	env := MergeGitEnv([]string{"LC_ALL=en_US.UTF-8", "CUSTOM_KEY=1"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("missing GIT_TERMINAL_PROMPT=0 in %v", env)
	}
	if !strings.Contains(joined, "GIT_OPTIONAL_LOCKS=0") {
		t.Fatalf("missing GIT_OPTIONAL_LOCKS=0 in %v", env)
	}
	if !strings.Contains(joined, "CUSTOM_KEY=1") {
		t.Fatalf("missing custom env var in %v", env)
	}
}

func TestRedactGitArgsAndLogStore(t *testing.T) {
	redacted := RedactGitArgs([]string{
		"clone",
		"https://alice:secret@example.com/demo.git",
		"-c",
		"http.extraheader=Authorization: Bearer topsecret",
	})
	if strings.Contains(redacted, "secret") || strings.Contains(redacted, "topsecret") {
		t.Fatalf("credentials leaked in redacted args: %q", redacted)
	}

	store := &GitLogStore{}
	id := store.Begin("/tmp/demo-repo", []string{"status"})
	store.Finish(id, GitCallOK, 15*time.Millisecond, 0, []byte("clean\n"), nil)
	entries := store.Entries("/tmp/demo-repo")
	if len(entries) != 1 || entries[0].Status != GitCallOK || entries[0].Out.Text != "clean\n" {
		t.Fatalf("unexpected log entries: %+v", entries)
	}
}

func TestGitCommandLaneClassification(t *testing.T) {
	if got := GitCommandLane([]string{"-C", "/tmp/repo", "status", "--porcelain"}); got != GitLaneLocalRead {
		t.Fatalf("status lane = %v, want GitLaneLocalRead", got)
	}
	if got := GitCommandLane([]string{"commit", "-m", "msg"}); got != GitLaneLocalWrite {
		t.Fatalf("commit lane = %v, want GitLaneLocalWrite", got)
	}
	if got := GitCommandLane([]string{"fetch", "origin"}); got != GitLaneRemote {
		t.Fatalf("fetch lane = %v, want GitLaneRemote", got)
	}
	if got := GitCommandLane([]string{"pull", "--rebase"}); got != GitLaneBoth {
		t.Fatalf("pull lane = %v, want GitLaneBoth", got)
	}
}

func TestWSLPathRoundTripAndCanonicalHook(t *testing.T) {
	distro, linuxPath, ok := ParseWSLUNCPath(`\\wsl$\Ubuntu\home\alice\projects\demo-app`)
	if !ok || distro != "Ubuntu" || linuxPath != "/home/alice/projects/demo-app" {
		t.Fatalf("unexpected WSL parse: distro=%q path=%q ok=%v", distro, linuxPath, ok)
	}
	if got := FormatWSLUNCPath(distro, linuxPath); got != `\\wsl.localhost\Ubuntu\home\alice\projects\demo-app` {
		t.Fatalf("unexpected WSL format: %q", got)
	}

	oldHook := CanonicalizeSpecialRepoDir
	CanonicalizeSpecialRepoDir = func(dir string) (string, bool) {
		if strings.HasPrefix(dir, "special:") {
			return "canonical:" + dir, true
		}
		return "", false
	}
	t.Cleanup(func() { CanonicalizeSpecialRepoDir = oldHook })
	if got := CanonicalRepoDir("special:repo"); got != "canonical:special:repo" {
		t.Fatalf("hook not used: %q", got)
	}
}
