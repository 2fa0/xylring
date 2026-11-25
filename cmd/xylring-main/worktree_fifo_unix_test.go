//go:build !windows

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestInspectWorktreeRemovalSkipsNamedPipeWithoutBlocking(t *testing.T) {
	dir, git := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")

	wtDir := filepath.Join(t.TempDir(), "wt-fifo")
	git("worktree", "add", "-b", "feat/fifo", wtDir)

	// Place a FIFO inside an untracked directory so git status reports the directory
	// and hashWorktreePath traverses into it.
	subDir := filepath.Join(wtDir, "untracked-dir")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "note.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fifoPath := filepath.Join(subDir, "hang.fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Direct hashWorktreePath call on the FIFO must return promptly without blocking.
	var buf bytes.Buffer
	if err := hashWorktreePath(ctx, &buf, wtDir, "untracked-dir/hang.fifo"); err != nil {
		t.Fatalf("hashWorktreePath on FIFO returned error: %v", err)
	}

	done := make(chan worktreeRemoveCheck, 1)
	go func() {
		_, check := inspectWorktreeRemoval(ctx, dir, repoWorktree{path: wtDir})
		done <- check
	}()

	select {
	case check := <-done:
		if check.err != "" {
			t.Fatalf("expected FIFO to be hashed by metadata without error, got %q", check.err)
		}
		if !check.dirty {
			t.Fatal("expected worktree with untracked directory containing FIFO to be marked dirty")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("inspectWorktreeRemoval blocked on named pipe (FIFO)")
	}
}
