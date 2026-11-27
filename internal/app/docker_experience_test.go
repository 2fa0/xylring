package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDockerSuggestedPaths(t *testing.T) {
	info := dockerContainerInfo{WorkingDir: "/workspaces/api"}
	info.Mounts = append(info.Mounts, struct {
		Source      string
		Destination string
	}{"/host", "/workspaces/api"}, struct {
		Source      string
		Destination string
	}{"/other", "/src"}, struct {
		Source      string
		Destination string
	}{"", "/"})
	if got := dockerSuggestedPaths(info); !reflect.DeepEqual(got, []string{"/workspaces/api", "/src"}) {
		t.Fatalf("paths = %q", got)
	}
}

func TestDockerRepositoryLabels(t *testing.T) {
	api, web := dockerTestKey("/work/api"), dockerTestKey("/work/web")
	if got := dockerRepoDisplayLabel(api, []string{api}); got != repoTitle(api) {
		t.Fatal(got)
	}
	if got := dockerRepoDisplayLabel(api, []string{api, web}); got != repoTitle(api)+" / api" {
		t.Fatal(got)
	}
	other := dockerTestKey("/other/api")
	if got := dockerRepoDisplayLabel(api, []string{api, web, other}); got != repoTitle(api)+" / /work/api" {
		t.Fatal(got)
	}
}

func TestDockerDiscardAndBranchWorkflow(t *testing.T) {
	requireDockerExec(t)
	repo, git := initTestRepo(t)
	name := "a '[x].txt"
	if err := os.WriteFile(filepath.Join(repo, name), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")
	git("branch", "next")
	env := setupFakeDocker(t, repo)
	if err := os.WriteFile(filepath.Join(repo, name), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := discardFile(context.Background(), env.key, fileChange{path: name}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(repo, name))
	if string(b) != "base\n" {
		t.Fatalf("discarded content = %q", b)
	}
	untracked := "new '[x].txt"
	os.WriteFile(filepath.Join(repo, untracked), []byte("new"), 0600)
	if err := discardFile(context.Background(), env.key, fileChange{path: untracked, extra: "未跟踪"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, untracked)); !os.IsNotExist(err) {
		t.Fatalf("untracked file remains: %v", err)
	}
	if err := discardFile(context.Background(), env.key, fileChange{path: "../outside", extra: "未跟踪"}); err == nil {
		t.Fatal("unsafe path accepted")
	}
	if err := checkoutBranch(context.Background(), env.key, "next"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(git("branch", "--show-current"))); got != "next" {
		t.Fatal(got)
	}
}

func TestDockerUnknownWriteBlocksNewActions(t *testing.T) {
	a, tab := dockerTestApp(t)
	seedUnknownDockerWrite(t, tab.path)
	a.runCheckoutBranch(tab, "dev")
	if a.write.busy || a.writeSeq != 0 {
		t.Fatal("branch switch bypassed write protection")
	}
	a.runDiscardWorktreeFile(tab, fileChange{path: "a.txt"})
	if tab.commitBusy || a.writeSeq != 0 {
		t.Fatal("discard bypassed write protection")
	}
}

func TestDockerReplacementUsesNameEndpointAndExactPath(t *testing.T) {
	requireDockerExec(t)
	repo, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	env := setupFakeDocker(t, repo)
	root, err := repoRoot(context.Background(), env.key)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := parseDockerRepoKey(root)
	old.name = "demo-app"
	if key, err := dockerReplacement(context.Background(), old); err != nil || key != "" {
		t.Fatalf("running container replaced: %q %v", key, err)
	}
	old.container = strings.Repeat("cd", 32)
	key, err := dockerReplacement(context.Background(), old)
	if err != nil || key != root {
		t.Fatalf("replacement = %q %v", key, err)
	}
	old.name = "unrelated"
	if key, _ := dockerReplacement(context.Background(), old); key != "" {
		t.Fatal("matched a different name")
	}
	old.name, old.endpoint = "demo-app", "unix:///other.sock"
	if key, err := dockerReplacement(context.Background(), old); key != "" || err == nil {
		t.Fatal("endpoint change accepted")
	}
}

func TestDockerTerminalArgsKeepPathsLiteral(t *testing.T) {
	target, _ := parseDockerRepoKey(dockerTestKey("/workspace/a '$(touch nope)"))
	args := dockerTerminalArgs(target)
	if args[5] != target.path || args[6] != target.container {
		t.Fatalf("args = %q", args)
	}
	if got := shellQuoteDocker("a'b"); got != "'a'\"'\"'b'" {
		t.Fatal(got)
	}
}

func TestDockerExperienceControlsSurviveFrames(t *testing.T) {
	requireDockerRepos(t)
	a, tab := dockerTestApp(t)
	a.docker = dockerDialogState{open: true, selected: 0, containers: []dockerContainer{{ID: fakeDockerContainerID, Name: "dev-box"}}, path: "/workspace/api", suggestions: []string{"/workspace/api", "/src"}}
	tt := ui.NewTester(a.dockerRepoModal, 700, 700)
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if _, ok := tt.Find("/src"); !ok {
		t.Fatal("path suggestion missing")
	}
	saveLobePreview(t, tt, "docker-path-suggestions")
	a.docker.open = false
	tt.Frame()
	a.docker.open = true
	tt.Frame()
	a.dockerWrites.recovery = dockerRecoveryDialog{open: true, repo: tab.path, evidence: []dockerEvidenceLine{{text: tr("未发现 Git 锁文件"), tone: "success"}}}
	recovery := ui.NewTester(a.dockerRecoveryModal, 700, 700)
	for i := 0; i < 3; i++ {
		recovery.Frame()
	}
	for _, label := range []string{"未发现 Git 锁文件", "进入容器终端", "复制检查命令"} {
		if _, ok := recovery.Find(tr(label)); !ok {
			t.Fatalf("recovery control missing: %s", label)
		}
	}
	saveLobePreview(t, recovery, "docker-recovery")
	a.dockerWrites.recovery.open = false
	recovery.Frame()
	a.dockerWrites.recovery.open = true
	recovery.Frame()
}

func TestDiscardDialogKeepsOriginalRepository(t *testing.T) {
	a, original := dockerTestApp(t)
	other := newTab(2, dockerTestKey("/workspace/web"))
	a.tabs = append(a.tabs, other)
	a.promptDiscardWorktreeFile(original, fileChange{path: "api.txt"})
	a.active = 1
	tt := ui.NewTester(a.discardModal, 700, 500)
	tt.Frame()
	if _, ok := tt.Find("api.txt"); !ok {
		t.Fatal("switching tabs lost the original confirmation")
	}
	a.tabs = []*repoTab{other}
	tt.Frame()
	if a.showDiscard {
		t.Fatal("confirmation survived closing its repository")
	}
}
