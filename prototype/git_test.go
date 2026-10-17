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
