package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRefName(t *testing.T) {
	dir, _ := initTestRepo(t)
	ctx := context.Background()
	if err := validateRefName(ctx, dir, "", "branch"); err == nil || !strings.Contains(err.Error(), "分支名") {
		t.Fatalf("empty branch: %v", err)
	}
	if err := validateRefName(ctx, dir, "", "tag"); err == nil || !strings.Contains(err.Error(), "标签名") {
		t.Fatalf("empty tag: %v", err)
	}
	if err := validateRefName(ctx, dir, "..", "branch"); err == nil {
		t.Fatal("illegal branch accepted")
	}
	if err := validateRefName(ctx, dir, "feature/ok", "branch"); err != nil {
		t.Fatal(err)
	}
}

func TestCreateBranchAndTag(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", "a.txt")
	git("commit", "-m", "initial")
	hash, err := resolveCommit(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := createBranch(ctx, dir, "feature", hash, false, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := createBranch(ctx, dir, "feature", hash, false, false, ""); err == nil {
		t.Fatal("duplicate branch accepted")
	}
	if err := createBranch(ctx, dir, "feature", hash, true, true, ""); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.branch != "feature" {
		t.Fatalf("checkout: %q", meta.branch)
	}
	if err := createTag(ctx, dir, "v1", "release", hash, true, false); err != nil {
		t.Fatal(err)
	}
	out := string(git("tag", "-n1", "v1"))
	if !strings.Contains(out, "v1") || !strings.Contains(out, "release") {
		t.Fatalf("annotated tag: %q", out)
	}
	if err := createTag(ctx, dir, "v1", "", hash, false, false); err == nil {
		t.Fatal("duplicate tag accepted")
	}
	if err := createTag(ctx, dir, "v1", "", hash, false, true); err != nil {
		t.Fatal(err)
	}
}

func branchUpstream(t *testing.T, dir, name string) string {
	t.Helper()
	out, err := runGit(context.Background(), dir, "rev-parse", "--abbrev-ref", name+"@{upstream}")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestTrackingRemotesSkipsHeadAndExistingLocal(t *testing.T) {
	refs := []refInfo{
		{name: "main", kind: refBranch},
		{name: "origin/HEAD", kind: refRemote},
		{name: "origin/main", kind: refRemote},
		{name: "origin/feature", kind: refRemote},
		{name: "upstream/feature", kind: refRemote},
	}
	got := trackingRemotes(refs)
	want := []string{"origin/feature", "origin/main", "upstream/feature"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("remotes %v", got)
	}
	all := map[[20]byte][]refInfo{hashN(1): refs}
	pick, n := preferredTrackingRemote(got, localBranchNames(all))
	if pick != "origin/feature" || n != 2 {
		t.Fatalf("preferred %q %d", pick, n)
	}
}

func TestCreateTrackingBranch(t *testing.T) {
	origin, gitOrigin := initTestRepo(t)
	gitOrigin("commit", "--allow-empty", "-m", "base")
	gitOrigin("checkout", "-b", "feature")
	gitOrigin("commit", "--allow-empty", "-m", "feature")
	gitOrigin("checkout", "main")
	dir := cloneTestRepo(t, origin)
	ctx := context.Background()
	hash, err := resolveCommit(ctx, dir, "origin/feature")
	if err != nil {
		t.Fatal(err)
	}
	if err := createBranch(ctx, dir, "feature", hash, false, false, "origin/feature"); err != nil {
		t.Fatal(err)
	}
	if got := branchUpstream(t, dir, "feature"); got != "origin/feature" {
		t.Fatalf("upstream %q", got)
	}
	meta, err := loadMeta(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.branch != "main" {
		t.Fatalf("checked out %q", meta.branch)
	}
	if err := createBranch(ctx, dir, "feature", hash, false, false, "origin/feature"); err == nil {
		t.Fatal("duplicate tracking branch accepted")
	}
	if err := createBranch(ctx, dir, "feature", hash, true, true, "origin/feature"); err != nil {
		t.Fatal(err)
	}
	meta, err = loadMeta(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.branch != "feature" {
		t.Fatalf("checkout %q", meta.branch)
	}
	if got := branchUpstream(t, dir, "feature"); got != "origin/feature" {
		t.Fatalf("upstream after checkout %q", got)
	}
}

func TestRewordHeadAndAncestor(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", "a.txt")
	git("commit", "-m", "first")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600)
	git("commit", "-am", "second")
	ctx := context.Background()
	head, err := resolveCommit(ctx, dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolveCommit(ctx, dir, "HEAD~")
	if err != nil {
		t.Fatal(err)
	}
	newHead, err := rewordCommit(ctx, dir, head, "second edited")
	if err != nil {
		t.Fatal(err)
	}
	if newHead == head {
		t.Fatal("HEAD hash did not change")
	}
	msg, err := readCommitMessage(ctx, dir, newHead)
	if err != nil || msg != "second edited" {
		t.Fatalf("HEAD message %q %v", msg, err)
	}
	parent, err := resolveCommit(ctx, dir, "HEAD~")
	if err != nil {
		t.Fatal(err)
	}
	if parent != first {
		t.Fatal("rewording HEAD rewrote its parent")
	}
	newFirst, err := rewordCommit(ctx, dir, first, "first edited\n\nbody")
	if err != nil {
		t.Fatal(err)
	}
	msg, err = readCommitMessage(ctx, dir, newFirst)
	if err != nil || msg != "first edited\n\nbody" {
		t.Fatalf("ancestor message %q %v", msg, err)
	}
	headMsg, err := readCommitMessage(ctx, dir, mustResolve(t, dir, "HEAD"))
	if err != nil || headMsg != "second edited" {
		t.Fatalf("replayed HEAD %q %v", headMsg, err)
	}
}

func TestRewordRejectsMergeAndDirty(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", "a.txt")
	git("commit", "-m", "base")
	git("checkout", "-b", "side")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("side\n"), 0o600)
	git("add", "b.txt")
	git("commit", "-m", "side")
	git("checkout", "main")
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("main\n"), 0o600)
	git("add", "c.txt")
	git("commit", "-m", "main")
	git("merge", "--no-ff", "-m", "merge side", "side")
	ctx := context.Background()
	merge, err := resolveCommit(ctx, dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rewordCommit(ctx, dir, merge, "nope"); err == nil || !strings.Contains(err.Error(), "合并") {
		t.Fatalf("merge: %v", err)
	}
	base, err := resolveCommit(ctx, dir, "HEAD~2")
	if err != nil {
		base, err = resolveCommit(ctx, dir, "HEAD^")
		if err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty\n"), 0o600)
	git("add", "dirty.txt")
	if _, err := rewordCommit(ctx, dir, base, "edited base"); err == nil || !strings.Contains(err.Error(), "未提交") {
		t.Fatalf("dirty ancestor: %v", err)
	}
}

func mustResolve(t *testing.T, dir, ref string) [20]byte {
	t.Helper()
	h, err := resolveCommit(context.Background(), dir, ref)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestRewordRejectsLaterMergeWithoutChangingContents(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	base := mustResolve(t, dir, "HEAD")
	git("checkout", "-b", "side")
	os.WriteFile(filepath.Join(dir, "side.txt"), []byte("side\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "side")
	git("checkout", "main")
	os.WriteFile(filepath.Join(dir, "main.txt"), []byte("main\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "main")
	git("merge", "--no-ff", "--no-commit", "side")
	os.WriteFile(filepath.Join(dir, "resolution.txt"), []byte("merge resolution\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "merge with resolution")
	git("commit", "--allow-empty", "-m", "after merge")
	head := mustResolve(t, dir, "HEAD")
	tree := string(git("rev-parse", "HEAD^{tree}"))
	if _, err := rewordCommit(context.Background(), dir, base, "edited base"); err == nil || !strings.Contains(err.Error(), "后续历史包含合并") {
		t.Fatalf("later merge: %v", err)
	}
	if got := mustResolve(t, dir, "HEAD"); got != head {
		t.Fatal("rejecting reword changed HEAD")
	}
	if got := string(git("rev-parse", "HEAD^{tree}")); got != tree {
		t.Fatal("rejecting reword changed the tree")
	}
	if got, err := os.ReadFile(filepath.Join(dir, "resolution.txt")); err != nil || string(got) != "merge resolution\n" {
		t.Fatalf("merge resolution lost: %q %v", got, err)
	}
	// A merge before the target does not prevent rewording a later linear commit.
	if _, err := rewordCommit(context.Background(), dir, head, "edited after merge"); err != nil {
		t.Fatal(err)
	}
	if got := string(git("rev-parse", "HEAD^{tree}")); got != tree {
		t.Fatal("rewording after a merge changed the tree")
	}
}

func TestRewordPreservesExistingRebase(t *testing.T) {
	for _, linked := range []bool{false, true} {
		name := "repository"
		if linked {
			name = "linked-worktree"
		}
		t.Run(name, func(t *testing.T) {
			dir, git := initTestRepo(t)
			git("commit", "--allow-empty", "-m", "base")
			base := mustResolve(t, dir, "HEAD")
			for _, name := range []string{"one", "two", "three"} {
				os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600)
				git("add", ".")
				git("commit", "-m", name)
			}
			if linked {
				worktree := t.TempDir()
				git("worktree", "add", "-b", "linked", worktree)
				dir = worktree
			}
			ctx := context.Background()
			if _, err := runGit(ctx, dir, "rebase", "--exec", "false", "HEAD~2"); err == nil {
				t.Fatal("expected rebase to pause at exec")
			}
			head := mustResolve(t, dir, "HEAD")
			out, err := runGit(ctx, dir, "rev-parse", "--absolute-git-dir")
			if err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(strings.TrimSpace(string(out)), "rebase-merge", "orig-head")
			original, err := os.ReadFile(state)
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range [][20]byte{base, head} {
				if _, err := rewordCommit(ctx, dir, target, "edited during rebase"); err == nil || !strings.Contains(err.Error(), "正在进行") {
					t.Fatalf("reword during rebase: %v", err)
				}
				if got := mustResolve(t, dir, "HEAD"); got != head {
					t.Fatal("reword changed HEAD during an existing rebase")
				}
				if got, err := os.ReadFile(state); err != nil || string(got) != string(original) {
					t.Fatalf("existing rebase state changed: %q %v", got, err)
				}
			}
		})
	}
}

func TestRewordPreservesExistingMerge(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	git("checkout", "-b", "side")
	git("commit", "--allow-empty", "-m", "side")
	git("checkout", "main")
	head := mustResolve(t, dir, "HEAD")
	git("merge", "--no-ff", "--no-commit", "side")
	before := string(git("rev-parse", "MERGE_HEAD"))
	if _, err := rewordCommit(context.Background(), dir, head, "edited during merge"); err == nil || !strings.Contains(err.Error(), "正在进行") {
		t.Fatalf("reword during merge: %v", err)
	}
	if got := mustResolve(t, dir, "HEAD"); got != head {
		t.Fatal("reword changed HEAD during an existing merge")
	}
	if got := string(git("rev-parse", "MERGE_HEAD")); got != before {
		t.Fatal("existing merge state changed")
	}
}

func TestRewordLeavesOtherBranchesAtOriginalCommits(t *testing.T) {
	dir, git := initTestRepo(t)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "one")
	target := mustResolve(t, dir, "HEAD")
	os.WriteFile(path, []byte("two\n"), 0o600)
	git("commit", "-am", "two")
	git("branch", "other")
	other := mustResolve(t, dir, "other")
	os.WriteFile(path, []byte("three\n"), 0o600)
	git("commit", "-am", "three")
	git("config", "rebase.updateRefs", "true")
	if _, err := rewordCommit(context.Background(), dir, target, "one edited"); err != nil {
		t.Fatal(err)
	}
	if got := mustResolve(t, dir, "other"); got != other {
		t.Fatalf("unselected branch moved: %s -> %s", hashHex(other), hashHex(got))
	}
}

func TestCreateTagDoesNotInterpretNamesAsOptions(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	hash := mustResolve(t, dir, "HEAD")
	existing := hashHex(hash)
	git("tag", existing)
	for _, name := range []string{"-d", "--delete", "-l", "--list", "-f"} {
		t.Run(name, func(t *testing.T) {
			if err := createTag(context.Background(), dir, name, "", hash, false, false); err == nil {
				t.Fatalf("option-like name accepted: %q", name)
			}
			if got := mustResolve(t, dir, "refs/tags/"+existing); got != hash {
				t.Fatal("creating a tag changed an existing tag")
			}
		})
	}
	if err := createTag(context.Background(), dir, "release-v1", "", hash, false, false); err != nil {
		t.Fatal(err)
	}
	if err := deleteTag(context.Background(), dir, "release-v1"); err != nil {
		t.Fatal(err)
	}
}
