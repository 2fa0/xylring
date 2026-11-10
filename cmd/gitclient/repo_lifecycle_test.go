package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestRepositoryDeletionAndReinitialization(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "automatic"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			dir, git := initTestRepo(t)
			git("commit", "--allow-empty", "-m", "old history")
			tab := newTab(1, dir)
			updates := make(chan func(), 64)
			a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
			defer tab.cancel()
			a.reload(tab)
			waitUpdates(t, updates, func() bool { return !tab.loading })
			if len(tab.commits) != 1 {
				t.Fatalf("initial load: %s", tab.err)
			}
			tab.query.ref = "refs/heads/main"
			tab.commitMsg, tab.commitAmend = "keep my draft", true
			tab.inspecting = tab.head
			file := &fileDiffTab{id: 1}
			tab.fileTabs = []*fileDiffTab{file}
			tab.contentTab = 1
			if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
				t.Fatal(err)
			}
			if manual {
				a.reload(tab)
			} else {
				a.checkActiveRepo()
			}
			waitUpdates(t, updates, func() bool { return !tab.loading && !tab.checking })
			if !tab.unavailable || len(tab.commits) != 0 || tab.inspecting != zeroHash || tab.hasMore || !file.closed || tab.commitAmend || tab.commitMsg != "keep my draft" {
				t.Fatalf("stale repository state survived: unavailable=%v commits=%d err=%s", tab.unavailable, len(tab.commits), tab.err)
			}
			tt := ui.NewTester(a.view, 1000, 700)
			defer retireTester(tt)
			tt.Frame()
			if !tt.HasText("仓库不可用") || !tt.HasText("重新检查") || tt.HasText("old history") || tt.HasText("尚无提交") {
				t.Fatalf("unexpected missing-repository UI: %q", tt.Texts())
			}
			a.quickPull(tab)
			a.quickPush(tab)
			a.quickFetch(tab)
			a.maybeAutoFetch()
			a.runCommitWorktree(tab)
			if tab.syncBusy || tab.commitBusy || tab.fetching || a.fetch.busy {
				t.Fatal("unavailable repository started an operation")
			}
			git("init", "-b", "main")
			if err := os.WriteFile(filepath.Join(dir, "local.txt"), []byte("kept\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			a.checkActiveRepo()
			waitUpdates(t, updates, func() bool { return !tab.loading && !tab.checking })
			if tab.unavailable || tab.err != "" || tab.head != zeroHash || len(tab.commits) != 0 || tab.query.ref != "" || tab.worktreeCount != 1 || tab.branch != "main" {
				t.Fatalf("reinitialized repository did not reload: unavailable=%v branch=%s dirty=%d err=%s", tab.unavailable, tab.branch, tab.worktreeCount, tab.err)
			}
			tt.Frame()
			tt.Frame()
			if tt.HasText("仓库不可用") || tt.HasText("old history") {
				t.Fatalf("stale UI after initialization: %q", tt.Texts())
			}
			git("config", "user.email", "test@example.com")
			git("config", "user.name", "Test")
			git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "new history")
			a.checkActiveRepo()
			waitUpdates(t, updates, func() bool { return !tab.loading && !tab.checking })
			if tab.err != "" || len(tab.commits) != 1 || tab.commits[0].subject != "new history" {
				t.Fatalf("new history not loaded: %s", tab.err)
			}
		})
	}
}

func TestRepositoryReplacementBetweenChecks(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "manual"}[manual], func(t *testing.T) {
			dir, git := initTestRepo(t)
			tab := newTab(1, dir)
			updates := make(chan func(), 64)
			a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
			defer tab.cancel()
			a.reload(tab)
			waitUpdates(t, updates, func() bool { return !tab.loading })
			// Both repositories are empty with the same branch: metadata keys match.
			tab.query.ref = "refs/heads/vanished"
			tab.commitAmend = true
			if err := os.Rename(filepath.Join(dir, ".git"), filepath.Join(t.TempDir(), "old.git")); err != nil {
				t.Fatal(err)
			}
			git("init", "-b", "main")
			if manual {
				a.reload(tab)
			} else {
				a.checkActiveRepo()
			}
			waitUpdates(t, updates, func() bool { return !tab.loading && !tab.checking })
			if tab.err != "" || tab.unavailable || tab.query.ref != "" || tab.commitAmend {
				t.Fatalf("replacement retained old query: %+v, err=%s", tab.query, tab.err)
			}
		})
	}
}

func TestMissingRepositoryDoesNotUseParent(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "parent")
	child := filepath.Join(dir, "nested")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := runGit(ctx, child, "status", "--porcelain"); err == nil {
		t.Fatal("command fell back to parent repository")
	}
	if err := streamCommits(ctx, child, 0, 10, func(commit) error { return nil }); err == nil {
		t.Fatal("history fell back to parent repository")
	}
	if _, err := loadMeta(ctx, child); !errors.Is(err, errRepoUnavailable) {
		t.Fatalf("missing repository metadata: %v", err)
	}
	if s := queryRepoSummary(ctx, child); s.branch != "仓库不可用" || s.dirty {
		t.Fatalf("misleading summary: %+v", s)
	}
	// Initial directory discovery still supports selecting a repository subdirectory.
	expected, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if root, err := repoRoot(ctx, child); err != nil || root != expected {
		t.Fatalf("directory discovery: root=%q err=%v", root, err)
	}
}

func TestMissingRepositoryRestored(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "restored history")
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	backup := filepath.Join(t.TempDir(), "backup.git")
	if err := os.Rename(filepath.Join(dir, ".git"), backup); err != nil {
		t.Fatal(err)
	}
	a.checkActiveRepo()
	waitUpdates(t, updates, func() bool { return !tab.checking })
	if !tab.unavailable {
		t.Fatal("missing repository not detected")
	}
	if err := os.Rename(backup, filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	a.checkActiveRepo()
	waitUpdates(t, updates, func() bool { return !tab.checking && !tab.loading })
	if tab.unavailable || tab.err != "" || len(tab.commits) != 1 || tab.commits[0].subject != "restored history" {
		t.Fatalf("restoration failed: %s", tab.err)
	}
}

func TestEvictedRepositoryReplacement(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "old history")
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	tab.query.ref = "refs/heads/main"
	tab.evictHistory()
	if err := os.Rename(filepath.Join(dir, ".git"), filepath.Join(t.TempDir(), "old.git")); err != nil {
		t.Fatal(err)
	}
	git("init", "-b", "main")
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	if tab.err != "" || tab.unavailable || tab.query.ref != "" || tab.restore != nil || len(tab.commits) != 0 {
		t.Fatalf("evicted tab retained old repository state: query=%+v err=%s", tab.query, tab.err)
	}
}

func TestLinkedWorktreeMissingGitDirectory(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "original")
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "-b", "linked", linked)
	tab := newTab(1, linked)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	if tab.err != "" || len(tab.commits) != 1 {
		t.Fatalf("initial worktree: %s", tab.err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	a.checkActiveRepo()
	waitUpdates(t, updates, func() bool { return !tab.checking })
	if !tab.unavailable || len(tab.commits) != 0 {
		t.Fatalf("worktree retained stale history: %s", tab.err)
	}
}

func TestRingCLIFromSubdirectory(t *testing.T) {
	dir, git := initTestRepo(t)
	child := filepath.Join(dir, "nested")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	git("config", "--unset", "core.hooksPath")
	ctx := context.Background()
	var out bytes.Buffer
	for _, args := range [][]string{
		{"stamp", "--agent", "Cindy", "--model", "script"},
		{"ring", "install"},
	} {
		if err := runRingCLI(ctx, child, args, &out); err != nil {
			t.Fatalf("CLI from subdirectory %v: %v", args, err)
		}
	}
	stamp, err := readStamp(ctx, dir)
	if err != nil || stamp.p.agent != "Cindy" || ringHookStatus(ctx, dir) != "已开启（v1）" {
		t.Fatalf("CLI did not target the repository: stamp=%+v err=%v", stamp, err)
	}
}
