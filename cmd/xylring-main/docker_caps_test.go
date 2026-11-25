package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func requireDockerRepos(t *testing.T) {
	t.Helper()
	if !dockerReposSupported() {
		t.Skip("container repositories are not offered on this platform")
	}
}

func dockerTestKey(path string) string {
	return dockerRepoKey(repoTarget{
		kind:      targetDocker,
		context:   "testctx",
		endpoint:  "unix:///tmp/fake-docker.sock",
		container: fakeDockerContainerID,
		path:      path,
	})
}

func dockerTestApp(t *testing.T) (*app, *repoTab) {
	t.Helper()
	key := dockerTestKey("/workspace/api")
	tab := newTab(1, key)
	a := &app{tabs: []*repoTab{tab}, active: 0}
	return a, tab
}

func seedUnknownDockerWrite(t *testing.T, repo string) {
	t.Helper()
	oldConfig := userConfigDir
	cfg := t.TempDir()
	userConfigDir = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { userConfigDir = oldConfig })

	recordsPath, err := dockerWriteRecordsPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal([]dockerWriteRecord{{Repo: repo, OpID: "pending-write", Action: "stage", Reason: "连接中断"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordsPath, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDockerBlockedActionsDoNotStartWrites(t *testing.T) {
	a, tab := dockerTestApp(t)
	a.openOperation("合并", "", "")
	if a.showOperation || !strings.Contains(a.banner, "暂不支持") {
		t.Fatalf("merge opened for a container repository: %q", a.banner)
	}
	a.banner = ""
	a.openCommitWrite(tab, writeBranch, commit{})
	if a.write.kind != "" || !strings.Contains(a.banner, "暂不支持") {
		t.Fatalf("branch write opened: %+v %q", a.write, a.banner)
	}
	a.banner = ""
	a.openRefWrite(tab, writeRenameBranch, commit{}, "main")
	if a.write.kind != "" {
		t.Fatalf("ref write opened: %+v", a.write)
	}
	a.banner = ""
	a.runCheckoutCommit(tab, zeroHash)
	if a.write.busy || a.writeSeq != 0 {
		t.Fatal("detached checkout started for a container repository")
	}
	a.quickFetch(tab)
	a.quickPull(tab)
	a.requestPush(tab)
	a.openFetch(tab)
	if a.showFetch || a.fetch.busy || a.writeSeq != 0 {
		t.Fatal("network operation started for a container repository")
	}
	a.promptDiscardWorktreeFile(tab, fileChange{path: "a.txt"})
	if !a.showDiscard {
		t.Fatal("discard dialog should open for a container repository")
	}
	if !dockerOperationAllowedInContainer("Blame") || !dockerOperationAllowedInContainer("行历史") {
		t.Fatal("read-only history views should stay available")
	}
	for _, action := range []string{"合并", "变基", "暂存", "审阅并暂存", "外部合并工具", "硬重置", "恢复历史文件", "查看 Stash", "添加标签", "设置身份"} {
		if dockerOperationAllowedInContainer(action) {
			t.Fatalf("%q must stay blocked", action)
		}
	}
}

func TestDockerCommitRejectsAmendAndUnknownWrite(t *testing.T) {
	a, tab := dockerTestApp(t)
	tab.commitMsg = "message"
	tab.commitAmend = true
	tab.worktreeFiles = []fileChange{{path: "a.txt", indexStatus: 'M', workStatus: ' '}}
	a.runCommitWorktree(tab)
	if !strings.Contains(tab.commitErr, "暂不支持修改上次提交") {
		t.Fatalf("amend not rejected: %q", tab.commitErr)
	}
	tab.commitAmend = false
	seedUnknownDockerWrite(t, tab.path)
	a.initDockerWrites()
	tab.commitErr = ""
	a.runCommitWorktree(tab)
	if !strings.Contains(tab.commitErr, "未确认") {
		t.Fatalf("unknown write did not block commit: %q", tab.commitErr)
	}
	if tab.commitBusy {
		t.Fatal("rejected commit left the tab busy")
	}
}

func TestDockerStagingBlockedByUnknownWrite(t *testing.T) {
	a, tab := dockerTestApp(t)
	seedUnknownDockerWrite(t, tab.path)
	a.stageWorktreeFile(tab, "a.txt")
	if tab.commitBusy {
		t.Fatal("staging started despite an unknown write")
	}
	if !strings.Contains(a.banner, "结果未知") {
		t.Fatalf("no reason for the blocked staging: %q", a.banner)
	}
	if rec := a.dockerWrites.records[tab.path]; rec == nil || rec.OpID != "pending-write" {
		t.Fatalf("pending staging record changed: %+v", rec)
	}
}

func TestDockerHomeEntryVisible(t *testing.T) {
	requireDockerRepos(t)
	a := &app{showHome: true}
	tt := ui.NewTester(a.view, 1100, 800)
	tt.SetTitleBar(ui.TitleBar{Height: 40, Left: 72, Right: 12})
	tt.Frame()
	if _, ok := tt.Find("打开容器仓库…"); !ok {
		t.Fatal("home screen lacks the container repository entry")
	}
	// 弹窗只列运行中的容器，不显示解释性副标题。
	a.docker = dockerDialogState{
		open:       true,
		context:    dockerContext{Name: "testctx", Endpoint: "unix:///tmp/fake-docker.sock"},
		containers: []dockerContainer{{ID: fakeDockerContainerID, Name: "demo-app", Image: "fake/image:latest"}},
		selected:   0,
		path:       "/workspace/api",
	}
	tt.Frame()
	for _, text := range []string{"demo-app", "fake/image:latest", "容器内仓库路径"} {
		if _, ok := tt.Find(text); !ok {
			t.Fatalf("dialog is missing %q", text)
		}
	}
}

func TestDockerSessionKeepsOfflineRepository(t *testing.T) {
	key := dockerTestKey("/workspace/api")
	if !restoredRepoUsable("", key, errDockerTestOffline) {
		t.Fatal("an offline container repository must keep its tab and draft")
	}
	if restoredRepoUsable("", "/tmp/local", errDockerTestOffline) {
		t.Fatal("a failed local repository must not be kept silently")
	}
	saved := savedRepo{Path: key, Draft: "draft", DockerName: "demo-app"}
	if !strings.Contains(saved.Path, "xylring-docker:v1:") {
		t.Fatal("session must persist the container identity, not a host path")
	}
	if !strings.Contains(saved.DockerName, "demo-app") {
		t.Fatal("session must keep the display name for offline tabs")
	}
}

var errDockerTestOffline = &dockerUnavailableError{message: "Docker 未运行或无法连接，请先启动 Docker"}

func TestDockerDraftCandidateOnlyOffersSameRecreatedContainer(t *testing.T) {
	oldConfig := userConfigDir
	userConfigDir = func() (string, error) { return t.TempDir(), nil }
	t.Cleanup(func() { userConfigDir = oldConfig })
	rememberDockerContainer(fakeDockerContainerID, "demo-app")
	tab := newTab(1, dockerTestKey("/workspace/api"))
	tab.commitMsg = "unfinished message"
	a := &app{tabs: []*repoTab{tab}, active: 0}
	if c := a.dockerDraftCandidate(dockerContainer{ID: fakeDockerContainerID, Name: "demo-app"}); c != nil {
		t.Fatal("a still-running container must not offer its own draft")
	}
	recreated := dockerContainer{ID: strings.Repeat("cd", 32), Name: "demo-app"}
	c := a.dockerDraftCandidate(recreated)
	if c == nil || c.Message != "unfinished message" {
		t.Fatalf("candidate = %+v", c)
	}
	if c := a.dockerDraftCandidate(dockerContainer{ID: recreated.ID, Name: "another"}); c != nil {
		t.Fatal("a different name must not inherit the draft")
	}
	noDraft := &app{tabs: []*repoTab{newTab(1, dockerTestKey("/workspace/other"))}, active: 0}
	if c := noDraft.dockerDraftCandidate(recreated); c != nil {
		t.Fatal("an empty draft must not be offered")
	}
}
