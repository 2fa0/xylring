package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSyncSets(t *testing.T) {
	const (
		a = "1111111111111111111111111111111111111111"
		b = "2222222222222222222222222222222222222222"
		c = "3333333333333333333333333333333333333333"
	)
	hashes := func(lines ...string) [][20]byte {
		var out [][20]byte
		for _, line := range lines {
			h, err := parseHash(line)
			if err != nil {
				t.Fatalf("parseHash(%q): %v", line, err)
			}
			out = append(out, h)
		}
		return out
	}
	same := func(t *testing.T, side string, got, want [][20]byte) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %d hashes, want %d", side, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s[%d]: got %s, want %s", side, i, hashHex(got[i]), hashHex(want[i]))
			}
		}
	}

	t.Run("empty", func(t *testing.T) {
		incoming, outgoing := parseSyncSets("")
		if len(incoming) != 0 || len(outgoing) != 0 {
			t.Fatalf("got incoming %v outgoing %v", incoming, outgoing)
		}
	})

	// `rev-list --left-right HEAD...@{upstream}`: 左边是 HEAD，所以 '<' 是本地独有。
	t.Run("one side only", func(t *testing.T) {
		incoming, outgoing := parseSyncSets("<" + a + "\n<" + b + "\n")
		same(t, "outgoing", outgoing, hashes(a, b))
		same(t, "incoming", incoming, nil)

		incoming, outgoing = parseSyncSets(">" + c + "\n")
		same(t, "incoming", incoming, hashes(c))
		same(t, "outgoing", outgoing, nil)
	})

	t.Run("both sides", func(t *testing.T) {
		incoming, outgoing := parseSyncSets(">" + b + "\n<" + a + "\n>" + c + "\n")
		same(t, "incoming", incoming, hashes(b, c))
		same(t, "outgoing", outgoing, hashes(a))
	})

	t.Run("noisy lines", func(t *testing.T) {
		// Trailing spaces, blank lines and a short hash are skipped, not counted.
		incoming, outgoing := parseSyncSets("  >" + c + "  \n\n<" + a + "\n<deadbeef\n")
		same(t, "incoming", incoming, hashes(c))
		same(t, "outgoing", outgoing, hashes(a))
		if ahead, behind := len(outgoing), len(incoming); ahead != 1 || behind != 1 {
			t.Fatalf("counts: ahead %d behind %d", ahead, behind)
		}
	})
}

// TestTrackingStatusSides 拿真 git 钉住左右两边的含义：只信上面的字符串表，
// 符号一反过来就把「待推送」和「远端独有」整个对调，用户会看到自己的提交被
// 标成「远端独有」。（就是这么错过的：单测当时把 '<' 当成远端独有。）
func TestTrackingStatusSides(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	remote := t.TempDir()
	if err := os.MkdirAll(filepath.Join(remote, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, remote, "init", "--bare", "-b", "main", "--quiet"); err != nil {
		t.Fatal(err)
	}
	dir, git := initTestRepo(t)
	writeFile := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) [20]byte {
		t.Helper()
		git("add", "-A")
		git("commit", "--allow-empty", "-m", msg)
		out := git("rev-parse", "HEAD")
		h, err := parseHash(string(out))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}

	writeFile("base.txt", "base\n")
	commit("base")
	git("remote", "add", "origin", remote)
	git("push", "-u", "origin", "main")

	s, err := queryTrackingStatus(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.hasUpstream || s.ahead != 0 || s.behind != 0 {
		t.Fatalf("clean: upstream %v ahead %d behind %d", s.hasUpstream, s.ahead, s.behind)
	}
	if len(s.incoming) != 0 || len(s.outgoing) != 0 {
		t.Fatalf("clean sets: incoming %d outgoing %d", len(s.incoming), len(s.outgoing))
	}

	local := commit("local only")
	s, err = queryTrackingStatus(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.ahead != 1 || s.behind != 0 {
		t.Fatalf("after local commit: ahead %d behind %d", s.ahead, s.behind)
	}
	if len(s.outgoing) != 1 || s.outgoing[0] != local {
		t.Fatalf("outgoing = %v, want the local commit", s.outgoing)
	}
	if len(s.incoming) != 0 {
		t.Fatalf("incoming = %v, want none", s.incoming)
	}

	// 另一个克隆往上游推一条，本地 fetch 之后它才算「远端独有」。
	other := t.TempDir()
	if _, err := runGit(ctx, remote, "config", "user.email", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, remote, "config", "user.name", "Test"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, other, "clone", "--quiet", remote, "."); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "remote.txt"), []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, other, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, other, "commit", "-m", "remote only"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, other, "push", "--quiet", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	remoteHead, err := runGit(ctx, other, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want, err := parseHash(string(remoteHead))
	if err != nil {
		t.Fatal(err)
	}
	git("fetch", "--quiet", "origin")

	s, err = queryTrackingStatus(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.ahead != 1 || s.behind != 1 {
		t.Fatalf("diverged: ahead %d behind %d", s.ahead, s.behind)
	}
	if len(s.incoming) != 1 || s.incoming[0] != want {
		t.Fatalf("incoming = %v, want the upstream commit %s", s.incoming, hashHex(want))
	}
	if len(s.outgoing) != 1 || s.outgoing[0] != local {
		t.Fatalf("outgoing = %v, want the local commit", s.outgoing)
	}
}
