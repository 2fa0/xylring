package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func importTestHistory(t *testing.T, dir string, n int) {
	t.Helper()
	var input strings.Builder
	for i := 0; i < n; i++ {
		msg := fmt.Sprintf("commit %d", i)
		fmt.Fprintf(&input, "commit refs/heads/main\ncommitter Test <test@example.com> %d +0000\ndata %d\n%s\n\n", 1700000000+i, len(msg), msg)
	}
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fast-import: %v\n%s", err, out)
	}
}

func TestBranchFilteringAndRefResolution(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	git("checkout", "-b", "feature")
	git("commit", "--allow-empty", "-m", "feature-only")
	feature, err := resolveCommit(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	git("checkout", "main")
	git("commit", "--allow-empty", "-m", "main-only")
	git("tag", "-a", "release", "-m", "release", "feature")
	for _, ref := range []string{"feature", "release", shortHash(feature)} {
		got, err := resolveCommit(context.Background(), dir, ref)
		if err != nil || got != feature {
			t.Fatalf("resolve %s: %x %v", ref, got, err)
		}
	}
	for _, ref := range []string{"--all", "missing-ref", "HEAD^{tree}"} {
		if _, err := resolveCommit(context.Background(), dir, ref); err == nil {
			t.Fatalf("accepted non-commit ref %q", ref)
		}
	}
	for _, tc := range []struct {
		refs []string
		want string
		n    int
	}{
		{[]string{"HEAD"}, "main-only", 2},
		{[]string{"refs/heads/feature"}, "feature-only", 2},
		{[]string{"refs/heads/main", "refs/heads/feature"}, "", 3},
	} {
		var commits []commit
		err := streamHistory(context.Background(), dir, 0, pageFirst, tc.refs, func(c commit) error {
			commits = append(commits, c)
			return nil
		})
		if err != nil || len(commits) != tc.n {
			t.Fatalf("refs %v: commits=%v err=%v", tc.refs, commits, err)
		}
		if tc.want != "" && commits[0].subject != tc.want {
			t.Fatalf("refs %v: newest %s", tc.refs, commits[0].subject)
		}
	}
	if branch := strings.TrimSpace(string(git("symbolic-ref", "--short", "HEAD"))); branch != "main" {
		t.Fatalf("filter checked out %s", branch)
	}
	git("checkout", "--orphan", "empty")
	if err := streamHistory(context.Background(), dir, 0, 10, []string{"HEAD"}, func(commit) error {
		t.Fatal("unborn current branch returned a commit")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteCommitInfo(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "--author", "Writer <writer@example.com>", "-m", "标题\n\n多行正文\n第二行")
	hash, err := resolveCommit(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	info, err := loadCommitInfo(context.Background(), dir, hashHex(hash))
	if err != nil {
		t.Fatal(err)
	}
	if info.author != "Writer" || info.authorEmail != "writer@example.com" || info.committer != "Test" || info.committerEmail != "test@example.com" || info.authorDate == "" || info.committerDate == "" || info.message != "标题\n\n多行正文\n第二行" {
		t.Fatalf("details: %+v", info)
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

func TestParseNameStatusAndPorcelain(t *testing.T) {
	files := parseNameStatusZ([]byte("R050\x00说明.txt\x00文档.txt\x00A\x00sub/a.go\x00"))
	if len(files) != 2 || files[0].status != "R" || files[0].oldPath != "说明.txt" || files[0].path != "文档.txt" {
		t.Fatalf("name-status %+v", files)
	}
	if files[1].status != "A" || files[1].path != "sub/a.go" {
		t.Fatalf("add %+v", files[1])
	}
	st := parsePorcelainZ([]byte(" M 文档.txt\x00R  终稿.txt\x00文档.txt\x00"))
	if len(st) != 2 {
		t.Fatalf("porcelain %+v", st)
	}
	if st[0].status != "M" || st[0].path != "文档.txt" || st[0].oldPath != "" {
		t.Fatalf("mod %+v", st[0])
	}
	if st[1].status != "R" || st[1].path != "终稿.txt" || st[1].oldPath != "文档.txt" {
		t.Fatalf("rename %+v", st[1])
	}
}

func TestGitRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "说明.txt"), []byte("一\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "说明.txt")
	git("commit", "-m", "初始")
	if err := os.WriteFile(filepath.Join(dir, "说明.txt"), []byte("二\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "a.go"), []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-m", "修改并新增")
	git("mv", "说明.txt", "文档.txt")
	git("commit", "-m", "重命名")

	root, err := repoRoot(ctx, dir)
	wantRoot, _ := filepath.EvalSymlinks(dir)
	gotRoot, _ := filepath.EvalSymlinks(root)
	if err != nil || gotRoot != wantRoot {
		t.Fatalf("root %q err %v", root, err)
	}
	var got []commit
	if err := streamCommits(ctx, dir, 0, 10, func(c commit) error {
		got = append(got, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].subject != "重命名" || got[2].subject != "初始" || len(got[2].parents) != 0 {
		t.Fatalf("%d commits, newest %q oldest parents %d", len(got), got[0].subject, len(got[2].parents))
	}
	files, err := commitFiles(ctx, dir, hashHex(got[0].hash))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].status != "R" || files[0].oldPath != "说明.txt" || files[0].path != "文档.txt" {
		t.Fatalf("rename diff %+v", files)
	}
	oldb, note, err := readBlob(ctx, dir, hashHex(got[0].parents[0]), "说明.txt")
	if err != nil || note != "" || !bytes.Equal(oldb, []byte("二\n")) {
		t.Fatalf("blob %q note %q err %v", oldb, note, err)
	}
	meta, err := loadMeta(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.branch != "main" || meta.dirty != 0 || meta.head != got[0].hash {
		t.Fatalf("meta %+v", meta)
	}
	if refs := meta.refs[meta.head]; len(refs) != 1 || refs[0].name != "main" {
		t.Fatalf("refs %+v", refs)
	}

	var page []commit
	if err := streamCommits(ctx, dir, 2, 2, func(c commit) error {
		page = append(page, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].hash != got[2].hash {
		t.Fatalf("skip page %+v", page)
	}
}
