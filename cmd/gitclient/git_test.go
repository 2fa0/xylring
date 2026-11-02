package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func initTestRepo(t *testing.T) (string, func(...string) []byte) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	git := func(args ...string) []byte {
		t.Helper()
		out, err := runGit(context.Background(), dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", filepath.Join(dir, "no-hooks"))
	return dir, git
}

func TestEmptyAndDetachedHistory(t *testing.T) {
	dir, git := initTestRepo(t)
	read := func() []commit {
		t.Helper()
		var commits []commit
		err := streamCommits(context.Background(), dir, 0, pageFirst, func(c commit) error {
			commits = append(commits, c)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return commits
	}
	if got := read(); len(got) != 0 {
		t.Fatalf("empty repo has %d commits", len(got))
	}
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o600)
	git("add", "new.txt")
	if got := read(); len(got) != 0 {
		t.Fatalf("unborn repo has %d commits", len(got))
	}
	git("commit", "-m", "initial")
	git("checkout", "--orphan", "unborn")
	if got := read(); len(got) != 1 || got[0].subject != "initial" {
		t.Fatalf("other branch history lost: %+v", got)
	}
	git("checkout", "--detach", "main")
	git("commit", "--allow-empty", "-m", "detached")
	if got := read(); len(got) != 2 || got[0].subject != "detached" {
		t.Fatalf("detached history lost: %+v", got)
	}
}

func TestGitQueueCancellation(t *testing.T) {
	for i := 0; i < cap(gitSlots); i++ {
		if err := acquireGit(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer func() { <-gitSlots }()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runGit(ctx, t.TempDir(), "status")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued command: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued command did not cancel")
	}
}

func TestParseCommitRecord(t *testing.T) {
	rec := "0123456789abcdef0123456789abcdef01234567\x1f\x1fAda\x1f1700000000\x1f初始"
	c, err := parseCommitRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if shortHash(c.hash) != "0123456" || c.author != "Ada" || c.subject != "初始" || len(c.parents) != 0 {
		t.Fatalf("%+v", c)
	}
	rec = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\x1fbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\x1fBob\x1f1700000001\x1f改了一行"
	c, err = parseCommitRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.parents) != 1 || shortHash(c.parents[0]) != "bbbbbbb" {
		t.Fatalf("parents %+v", c.parents)
	}
}

func TestParseRefsPlacesAnnotatedTagOnCommit(t *testing.T) {
	commitHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tagObject := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	raw := []byte(tagObject + " refs/tags/v1 " + commitHash + "\n" + commitHash + " refs/heads/main\n")
	refs := parseRefs(raw)
	got := refs[mustHash(t, commitHash)]
	if len(got) != 2 || got[0].kind != refBranch || got[0].name != "main" || got[1].kind != refTag || got[1].name != "v1" {
		t.Fatalf("refs %+v", got)
	}
	if _, ok := refs[mustHash(t, tagObject)]; ok {
		t.Fatal("annotated tag stayed on the tag object")
	}
}

func mustHash(t *testing.T, s string) [20]byte {
	t.Helper()
	h, err := parseHash(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func initGitDir(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), dir, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
}

func TestFindGitRepos(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	nested := filepath.Join(alpha, "lib", "nested")
	junk := filepath.Join(root, "node_modules", "pkg")
	initGitDir(t, alpha)
	initGitDir(t, beta)
	initGitDir(t, nested)
	initGitDir(t, junk)

	got, err := findGitRepos(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{alpha, nested, beta}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}

	got, err = findGitRepos(context.Background(), alpha)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{alpha, nested}
	if len(got) != len(want) {
		t.Fatalf("alpha: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("alpha: got %v want %v", got, want)
		}
	}
}

func TestFindGitReposFallback(t *testing.T) {
	dir, _ := initTestRepo(t)
	src := filepath.Join(dir, "src")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := findGitRepos(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	want, err := repoRoot(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v want [%s]", got, want)
	}
}

func TestFindGitReposNone(t *testing.T) {
	_, err := findGitRepos(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenPathPresentsChoices(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	initGitDir(t, alpha)
	initGitDir(t, beta)
	ready := make(chan func(), 16)
	a := &app{
		persist:  false,
		dispatch: func(fn func()) { ready <- fn },
	}
	a.openPath(root)
	select {
	case fn := <-ready:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	defer func() {
		for _, tab := range a.tabs {
			tab.cancel()
		}
	}()
	if len(a.tabs) != 1 {
		t.Fatalf("tabs=%d", len(a.tabs))
	}
	if len(a.found) != 2 {
		t.Fatalf("found=%v", a.found)
	}
	if a.foundFrom != root {
		t.Fatalf("foundFrom=%q want %q", a.foundFrom, root)
	}
}

func TestOpenPathSingleOpens(t *testing.T) {
	dir, _ := initTestRepo(t)
	ready := make(chan func(), 16)
	a := &app{
		persist:  false,
		dispatch: func(fn func()) { ready <- fn },
	}
	a.openPath(dir)
	select {
	case fn := <-ready:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	defer func() {
		for _, tab := range a.tabs {
			tab.cancel()
		}
	}()
	if len(a.found) != 0 || len(a.tabs) != 1 || a.tabs[0].path != dir {
		t.Fatalf("found=%v tabs=%d", a.found, len(a.tabs))
	}
	if a.foundFrom != dir {
		t.Fatalf("foundFrom=%q want %q", a.foundFrom, dir)
	}
}

func TestRefreshDiscoversNestedRepo(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	initGitDir(t, alpha)
	ready := make(chan func(), 16)
	a := &app{
		persist:  false,
		dispatch: func(fn func()) { ready <- fn },
	}
	a.openPath(root)
	select {
	case fn := <-ready:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	defer func() {
		for _, tab := range a.tabs {
			tab.cancel()
		}
	}()
	if len(a.found) != 0 || a.foundFrom != root || len(a.tabs) != 1 {
		t.Fatalf("found=%v from=%q tabs=%d", a.found, a.foundFrom, len(a.tabs))
	}
	initGitDir(t, filepath.Join(root, "beta"))
	a.refresh(a.tabs[0])
	deadline := time.After(5 * time.Second)
	for {
		select {
		case fn := <-ready:
			fn()
		case <-deadline:
			t.Fatalf("found=%v", a.found)
		}
		if len(a.found) == 2 {
			break
		}
	}
	if len(a.tabs) != 1 {
		t.Fatalf("tabs=%d", len(a.tabs))
	}
}

func TestRefreshDiscoversNestedUnderOpenRepo(t *testing.T) {
	dir, _ := initTestRepo(t)
	ready := make(chan func(), 16)
	tab := newTab(1, dir)
	tab.loaded = true
	a := &app{
		tabs:     []*repoTab{tab},
		persist:  false,
		dispatch: func(fn func()) { ready <- fn },
	}
	initGitDir(t, filepath.Join(dir, "lib", "nested"))
	a.refresh(tab)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case fn := <-ready:
			fn()
		case <-deadline:
			t.Fatalf("found=%v from=%q", a.found, a.foundFrom)
		}
		if len(a.found) == 2 {
			break
		}
	}
	if a.foundFrom != dir {
		t.Fatalf("foundFrom=%q want %q", a.foundFrom, dir)
	}
	if len(a.tabs) != 1 {
		t.Fatalf("tabs=%d", len(a.tabs))
	}
}

func TestCommitFiles(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", "a.txt")
	git("commit", "-m", "initial")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600)
	git("add", "a.txt")
	git("commit", "-m", "change")
	git("checkout", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("feat\n"), 0o600)
	git("add", "b.txt")
	git("commit", "-m", "feature")
	git("checkout", "main")
	git("merge", "--no-ff", "-m", "merge feature", "feature")

	ctx := context.Background()
	initial, err := resolveCommit(ctx, dir, "HEAD~2")
	if err != nil {
		t.Fatal(err)
	}
	change, err := resolveCommit(ctx, dir, "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	merge, err := resolveCommit(ctx, dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	var commits []commit
	err = streamCommits(ctx, dir, 0, pageFirst, func(c commit) error {
		commits = append(commits, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	byHash := map[[20]byte]commit{}
	for _, c := range commits {
		byHash[c.hash] = c
	}
	root, err := commitFiles(ctx, dir, initial, byHash[initial].parents)
	if err != nil {
		t.Fatal(err)
	}
	if len(root) != 1 || root[0].status != "A" || root[0].path != "a.txt" {
		t.Fatalf("root %+v", root)
	}
	mid, err := commitFiles(ctx, dir, change, byHash[change].parents)
	if err != nil {
		t.Fatal(err)
	}
	if len(mid) != 1 || mid[0].status != "M" || mid[0].path != "a.txt" {
		t.Fatalf("change %+v", mid)
	}
	merged, err := commitFiles(ctx, dir, merge, byHash[merge].parents)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 || merged[0].status != "A" || merged[0].path != "b.txt" {
		t.Fatalf("merge first-parent %+v", merged)
	}
}
