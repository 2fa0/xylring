package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDockerMissingChildNeverWritesParent(t *testing.T) {
	requireDockerExec(t)
	parent, git := initTestRepo(t)
	child := filepath.Join(parent, "child")
	os.Mkdir(child, 0700)
	fake := setupFakeDocker(t, child)
	os.WriteFile(filepath.Join(child, "a.txt"), []byte("keep"), 0600)
	if _, err := runGit(context.Background(), fake.key, "rev-parse", "--show-toplevel"); err == nil {
		t.Fatal("missing child resolved as parent")
	}
	if err := stageFile(context.Background(), fake.key, "a.txt"); err == nil {
		t.Fatal("staged in parent")
	}
	if out := git("ls-files"); len(out) != 0 {
		t.Fatalf("parent index changed: %s", out)
	}
}

func TestCheckoutMissingBranchKeepsSameNamedFile(t *testing.T) {
	for _, container := range []bool{false, true} {
		t.Run(fmt.Sprint(container), func(t *testing.T) {
			repo, git := initTestRepo(t)
			file := filepath.Join(repo, "next")
			os.WriteFile(file, []byte("committed"), 0600)
			git("add", "next")
			git("commit", "-m", "base")
			git("branch", "next")
			key := repo
			if container {
				requireDockerExec(t)
				key = setupFakeDocker(t, repo).key
			}
			git("branch", "-D", "next")
			os.WriteFile(file, []byte("unsaved changes"), 0600)
			if err := checkoutBranch(context.Background(), key, "next"); err == nil {
				t.Fatal("missing branch accepted")
			}
			data, _ := os.ReadFile(file)
			if string(data) != "unsaved changes" {
				t.Fatalf("discarded file: %q", data)
			}
		})
	}
}

func TestDockerSettingsCannotBypassWriteGuard(t *testing.T) {
	a, tab := dockerTestApp(t)
	a.openSettings()
	a.dockerWrites.unknown = map[string]string{tab.path: "unknown"}
	a.loadSettingsGit(true, true)
	if a.settings.local.busy || !strings.Contains(a.settings.local.err, "暂不支持") {
		t.Fatal("container project settings write not rejected")
	}
	if err := saveGitSettings(context.Background(), tab.path, "--local", nil); err == nil {
		t.Fatal("low-level settings write allowed")
	}
	if _, err := configCommand(context.Background(), tab.path, "--local", "user.name", "Changed"); err == nil {
		t.Fatal("direct config write allowed")
	}
	tt := ui.NewTester(a.projectSettings, 700, 500)
	defer retireTester(tt)
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find(tr("容器仓库暂不支持修改 Git 配置")); !ok {
		t.Fatal("missing project settings restriction")
	}
	if _, ok := tt.Find(tr("提交姓名")); ok {
		t.Fatal("container identity editor still available")
	}
}

func TestDockerSelectedGlobalSettingsUseHostGit(t *testing.T) {
	isolateSettingsGit(t)
	a, _ := dockerTestApp(t)
	a.openSettings()
	updates := make(chan func(), 4)
	a.dispatch = func(f func()) { updates <- f }
	a.loadSettingsGit(false, false)
	select {
	case f := <-updates:
		f()
	case <-time.After(10 * time.Second):
		t.Fatal("settings read timed out")
	}
	if a.settings.global.err != "" || !a.settings.global.loaded {
		t.Fatalf("host settings not read: %+v", a.settings.global)
	}
	a.settings.global.fields[0].enabled = true
	a.settings.global.fields[0].value = "Host Identity"
	a.loadSettingsGit(false, true)
	select {
	case f := <-updates:
		f()
	case <-time.After(10 * time.Second):
		t.Fatal("settings save timed out")
	}
	if a.settings.global.err != "" {
		t.Fatal(a.settings.global.err)
	}
	out, err := configCommand(context.Background(), "", "--global", "--get", "user.name")
	if err != nil || strings.TrimSpace(string(out)) != "Host Identity" {
		t.Fatalf("host settings: %q %v", out, err)
	}
}

func TestDockerPathsPreserveTrailingWhitespace(t *testing.T) {
	requireDockerExec(t)
	base, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	spaced := base + " \n"
	os.Rename(base, spaced)
	alias := filepath.Join(t.TempDir(), "alias")
	os.Symlink(spaced, alias)
	fake := setupFakeDocker(t, alias)
	root, err := repoRoot(context.Background(), fake.key)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := parseDockerRepoKey(root)
	want, _ := filepath.EvalSymlinks(spaced)
	if target.path != want {
		t.Fatalf("wrong root: %q want %q", target.path, want)
	}
	clean, err := normalizeContainerRepoPath("/workspace/repo \n")
	if err != nil || clean != "/workspace/repo \n" {
		t.Fatalf("input altered: %q %v", clean, err)
	}
	if got := resolveRepoGitPath(root, ".git/hooks \n"); got != filepath.ToSlash(filepath.Join(want, ".git/hooks ")) {
		t.Fatalf("Git path altered: %q", got)
	}
}

func TestDockerUntrackedStatsBatchAndCache(t *testing.T) {
	requireDockerExec(t)
	repo, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	repo, _ = filepath.EvalSymlinks(repo)
	fake := setupFakeDocker(t, repo)
	for i := 0; i < 200; i++ {
		os.WriteFile(filepath.Join(repo, fmt.Sprintf("new%03d.txt", i)), []byte("one\ntwo"), 0600)
	}
	os.WriteFile(filepath.Join(repo, "empty.txt"), nil, 0600)
	os.WriteFile(filepath.Join(repo, "binary.bin"), []byte{'a', 0, 'b'}, 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	files, cache, err := pollWorktreeStatus(ctx, fake.key, nil)
	if err != nil || len(files) != 202 {
		t.Fatalf("first refresh: %d %v", len(files), err)
	}
	for _, f := range files {
		if !f.unstagedStat.has {
			t.Fatalf("missing stats: %+v", f)
		}
		if strings.HasPrefix(f.path, "new") && f.unstagedStat.added != 2 {
			t.Fatalf("wrong lines: %+v", f)
		}
		if f.path == "binary.bin" && !f.unstagedStat.binary {
			t.Fatal("binary file treated as text")
		}
	}
	before, _ := os.ReadFile(fake.logPath)
	cache.contentChecked = time.Now().Add(-2 * dockerContentCheckInterval)
	next, _, err := pollWorktreeStatus(ctx, fake.key, cache)
	if err != nil || len(next) != len(files) {
		t.Fatalf("cached refresh: %d %v", len(next), err)
	}
	after, _ := os.ReadFile(fake.logPath)
	if strings.Count(string(before), "for tool in head base64 tr") != 2 {
		t.Fatal("expected two bounded batches for 202 files")
	}
	if strings.Count(string(after), "for tool in head base64 tr") != 2 {
		t.Fatal("unchanged files reloaded instead of reusing stats")
	}
	if len(readFakeDockerLog(t, fake.logPath)) > 40 {
		t.Fatal("Docker calls grew per file")
	}
}
