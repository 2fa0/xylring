package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerKilledClientKeepsWriteProtection(t *testing.T) {
	requireDockerExec(t)
	repo := setupFakeDocker(t, t.TempDir()).key
	a := &app{}
	a.initDockerWrites()
	rec, err := a.beginDockerWrite(repo, "提交")
	if err != nil {
		t.Fatal(err)
	}
	raw := exec.Command("sh", "-c", "kill -9 $$").Run()
	target, _ := parseDockerRepoKey(repo)
	err = dockerRunError(target, "docker", raw, []byte("hook still running"))
	a.finishDockerWrite(rec, err)
	if a.dockerWriteBlockReason(repo) == "" {
		t.Fatal("killed client unlocked write")
	}
	b := &app{}
	b.initDockerWrites()
	if _, err := b.beginDockerWrite(repo, "提交"); err == nil {
		t.Fatal("restart unlocked write")
	}
	// 普通 Hook 拒绝不能误判为失联。
	hookErr := dockerRunError(target, "docker", exec.Command("false").Run(), []byte("hook rejected"))
	if dockerCommandGone(hookErr, "") {
		t.Fatal("hook rejection treated as lost client")
	}
}

func TestDockerWriteRecordsFailClosed(t *testing.T) {
	requireDockerRepos(t)
	repo := dockerRepoKey(repoTarget{kind: targetDocker, context: "testctx", endpoint: "unix:///tmp/fake-docker.sock", container: fakeDockerContainerID, path: "/repo"})
	old := userConfigDir
	defer func() { userConfigDir = old }()
	userConfigDir = func() (string, error) { return "", errors.New("config unavailable") }
	a := &app{}
	a.initDockerWrites()
	if _, err := a.beginDockerWrite(repo, "提交"); err == nil {
		t.Fatal("write allowed without record storage")
	}
	dir := t.TempDir()
	userConfigDir = func() (string, error) { return dir, nil }
	p, err := dockerWriteRecordsPath()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte("broken json"), 0600)
	a.initDockerWrites()
	if _, err := a.beginDockerWrite(repo, "提交"); err == nil {
		t.Fatal("corrupt records ignored")
	}
	if err := a.clearDockerWrite(repo); err == nil {
		t.Fatal("recovery overwrote unreadable records")
	}
	os.WriteFile(p, []byte("[]"), 0600)
	rec, err := a.beginDockerWrite(repo, "提交")
	if err != nil {
		t.Fatal(err)
	}
	a.finishDockerWrite(rec, &dockerUnknownResultError{err: errors.New("lost client")})
	// 清除落盘失败时，内存仍保留保护。
	os.Remove(p)
	os.Mkdir(p, 0700)
	if err := a.clearDockerWrite(repo); err == nil {
		t.Fatal("expected save error")
	}
	if a.dockerWriteBlockReason(repo) == "" {
		t.Fatal("save failure unlocked write")
	}
}

func TestDockerEndpointPinnedForEveryCommand(t *testing.T) {
	requireDockerExec(t)
	fake := setupFakeDocker(t, t.TempDir())
	target, _ := parseDockerRepoKey(fake.key)
	t.Setenv("FAKE_CONTEXT_NAME", "unrelated-context")
	if _, err := runGit(context.Background(), fake.key, "--version"); err != nil {
		t.Fatal("global context switch affected pinned target", err)
	}
	t.Setenv("FAKE_ENDPOINT", "tcp://elsewhere:2375")
	if _, err := runGit(context.Background(), fake.key, "--version"); err == nil {
		t.Fatal("Git ran after endpoint changed")
	}
	if _, err := dockerRun(context.Background(), target, target.path, nil, 1024, "", "true"); err == nil {
		t.Fatal("file command ran after endpoint changed")
	}
}

func TestDockerBackslashFileAndOpenedDescriptor(t *testing.T) {
	requireDockerExec(t)
	repo, _ := filepath.EvalSymlinks(t.TempDir())
	fake := setupFakeDocker(t, repo)
	target, _ := parseDockerRepoKey(fake.key)
	os.WriteFile(filepath.Join(repo, `a\b.txt`), []byte("correct"), 0600)
	os.Mkdir(filepath.Join(repo, "a"), 0700)
	os.WriteFile(filepath.Join(repo, "a/b.txt"), []byte("wrong"), 0600)
	data, note, err := readWorktreeFile(context.Background(), fake.key, `a\b.txt`)
	if err != nil || note != "" || string(data) != "correct" {
		t.Fatalf("wrong file: %q %q %v", data, note, err)
	}
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	secret := filepath.Join(outside, "secret")
	os.WriteFile(secret, []byte("outside-secret"), 0600)
	os.WriteFile(filepath.Join(repo, "file"), []byte("inside"), 0600)
	t.Setenv("TEST_OUTSIDE", secret)
	prologue := `p=$1; root=$2; limit=$3
`
	for _, beforeOpen := range []bool{true, false} {
		script := dockerOpenFileScript
		swap := `mv file original; ln -s "$TEST_OUTSIDE" file
`
		if beforeOpen {
			script = strings.Replace(script, "exec 3<", swap+"exec 3<", 1)
		} else {
			script += swap
		}
		script += `head -c "$limit" <&3`
		data, err := dockerRun(context.Background(), target, repo, nil, 1024, "", "sh", "-c", prologue+script, "sh", "file", repo, "1024")
		os.Remove(filepath.Join(repo, "file"))
		os.Rename(filepath.Join(repo, "original"), filepath.Join(repo, "file"))
		if beforeOpen && (err == nil || strings.Contains(string(data), "outside-secret")) {
			t.Fatalf("outside descriptor accepted: %q %v", data, err)
		}
		if !beforeOpen && (err != nil || string(data) != "inside") {
			t.Fatalf("read reopened path: %q %v", data, err)
		}
	}
}
