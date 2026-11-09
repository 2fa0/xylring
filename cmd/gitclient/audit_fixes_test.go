package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFileActionsUseLiteralPaths(t *testing.T) {
	for _, action := range []string{"stage", "unstage", "discard"} {
		t.Run(action, func(t *testing.T) {
			dir, git := initTestRepo(t)
			for _, name := range []string{"a[1].txt", "a1.txt"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("base\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			git("add", ".")
			git("commit", "-m", "base")
			for _, name := range []string{"a[1].txt", "a1.txt"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			var err error
			switch action {
			case "stage":
				err = stageFile(ctx, dir, "a[1].txt")
			case "unstage":
				git("add", ".")
				err = unstageFile(ctx, dir, "a[1].txt")
			case "discard":
				err = discardFile(ctx, dir, fileChange{path: "a[1].txt"})
			}
			if err != nil {
				t.Fatal(err)
			}
			files, err := worktreeStatus(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range files {
				if f.path != "a1.txt" {
					continue
				}
				if action == "stage" && f.hasStaged() || action == "unstage" && !f.hasStaged() {
					t.Fatalf("other file affected: %+v", f)
				}
			}
			data, err := os.ReadFile(filepath.Join(dir, "a1.txt"))
			if err != nil || string(data) != "changed\n" {
				t.Fatalf("other file overwritten: %q %v", data, err)
			}
		})
	}
}

func TestStagedDeletionCanCommit(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0600)
	git("add", ".")
	git("commit", "-m", "base")
	git("rm", "a.txt")
	files, err := worktreeStatus(context.Background(), dir)
	if err != nil || len(files) != 1 || !files[0].hasStaged() || files[0].hasUnstaged() {
		t.Fatalf("deletion state: %+v %v", files, err)
	}
	updates := make(chan func(), 16)
	tab := newTab(1, dir)
	tab.worktreeFiles = files
	tab.commitMsg = "delete file"
	a := &app{dispatch: func(f func()) { updates <- f }}
	a.runCommitWorktree(tab)
	waitUpdates(t, updates, func() bool { return !tab.commitBusy })
	if tab.commitErr != "" {
		t.Fatal(tab.commitErr)
	}
	if got := git("log", "-1", "--format=%s"); strings.TrimSpace(string(got)) != "delete file" {
		t.Fatal(got)
	}
}

func TestPorcelainKeepsIndexAndWorktreeStates(t *testing.T) {
	for _, tc := range []struct {
		code             string
		staged, unstaged bool
	}{{"D ", true, false}, {" D", false, true}, {"R ", true, false}, {" R", false, true}, {"MD", true, true}, {"MM", true, true}, {"??", false, false}, {"UU", false, false}} {
		files := parsePorcelainZ([]byte(tc.code + " file\x00old\x00"))
		if files[0].hasStaged() != tc.staged || files[0].hasUnstaged() != tc.unstaged {
			t.Fatalf("%s: %+v", tc.code, files[0])
		}
	}
}

func TestSplitPatchAllowsMetadataWordsInContent(t *testing.T) {
	patch := "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new file mode 100644; rename from old; GIT binary patch\n"
	hunks, err := splitPatch(patch)
	if err != nil || len(hunks) != 1 || hunks[0].patch != patch {
		t.Fatalf("%+v %v", hunks, err)
	}
	if _, err = splitPatch("diff --git a/a b/a\nnew file mode 100644\n@@ -0,0 +1 @@\n+new\n"); err == nil {
		t.Fatal("new file accepted")
	}
}

func BenchmarkSplitLargePatch(b *testing.B) {
	patch := "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n" + strings.Repeat("+abcdefghijklmnop\n", 20000)
	b.ReportAllocs()
	b.SetBytes(int64(len(patch)))
	b.ResetTimer()
	for range b.N {
		if _, err := splitPatch(patch); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSessionRetainsEvictedSelection(t *testing.T) {
	dir := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return dir, nil }
	defer func() { userConfigDir = old }()
	tab := newTab(1, "/repo")
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{{path: "a.go"}}
	tab.fileSel = 0
	tab.evictHistory()
	a := &app{tabs: []*repoTab{tab}, persist: true, sessionReady: true}
	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone
	s := loadSession()
	if len(s.Repos) != 1 || s.Repos[0].Commit != hashHex(hashN(1)) || s.Repos[0].File != "a.go" {
		t.Fatalf("%+v", s)
	}
}

func TestSessionWriterRetriesAndFlushesLatest(t *testing.T) {
	ch := make(chan savedSession, 1)
	results := make(chan error, 1)
	done := make(chan struct{})
	written := make(chan savedSession, 4)
	var attempts atomic.Int32
	go func() {
		defer close(done)
		writeSessionQueue(ch, results, func(s savedSession) error {
			if attempts.Add(1) == 1 {
				return errors.New("temporary failure")
			}
			written <- s
			return nil
		})
	}()
	ch <- savedSession{Active: "first"}
	select {
	case s := <-written:
		if s.Active != "first" {
			t.Fatal(s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no retry")
	}
	ch <- savedSession{Active: "latest"}
	close(ch)
	<-done
	if s := <-written; s.Active != "latest" {
		t.Fatal(s)
	}
}

func TestAmendLoadDoesNotOverwriteDraft(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "original")
	head, err := resolveCommit(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tab := newTab(1, dir)
	tab.head = head
	tab.commitAmend = true
	updates := make(chan func(), 4)
	a := &app{dispatch: func(f func()) { updates <- f }}
	a.loadAmendMessage(tab)
	tab.commitMsg = "typed while loading"
	select {
	case f := <-updates:
		f()
	case <-time.After(5 * time.Second):
		t.Fatal("no result")
	}
	if tab.commitMsg != "typed while loading" {
		t.Fatal(tab.commitMsg)
	}
}
