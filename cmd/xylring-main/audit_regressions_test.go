package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestStashViewFailureRequiresConfirmation(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchecked", true: "checked"}[confirmed], func(t *testing.T) {
			dir, git := initTestRepo(t)
			path := filepath.Join(dir, "file.txt")
			if err := os.WriteFile(path, []byte("base\n"), 0600); err != nil {
				t.Fatal(err)
			}
			git("add", ".")
			git("commit", "-m", "base")
			if err := os.WriteFile(path, []byte(strings.Repeat("x", (1<<20)+100)), 0600); err != nil {
				t.Fatal(err)
			}
			git("stash", "push", "-m", "large")
			updates := make(chan func(), 32)
			repo := newTab(1, dir)
			a := &app{tabs: []*repoTab{repo}, dispatch: func(f func()) { updates <- f }}
			t.Cleanup(func() { a.closed = true; repo.closed = true; repo.cancel() })
			a.openOperation("Stash", "", "")
			nextDialogUpdate(t, updates)()
			a.op.confirmed = confirmed
			a.runHubAction("查看 Stash", false)
			if a.op.confirmed != confirmed {
				t.Fatal("view changed the user's confirmation")
			}
			nextDialogUpdate(t, updates)()
			if !strings.Contains(a.op.err, "Git 输出超过") || a.op.confirmed {
				t.Fatalf("failed view retained confirmation: %+v", a.op)
			}
			a.runHubAction("删除 Stash", true)
			if a.op.busy || a.op.err != "请先确认再执行" {
				t.Fatal("delete ran without a fresh confirmation")
			}
			entries, err := listStashes(context.Background(), dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("stash was lost: %v %v", entries, err)
			}
			a.op.confirmed = true
			a.runHubAction("删除 Stash", true)
			nextDialogUpdate(t, updates)()
			entries, err = listStashes(context.Background(), dir)
			if err != nil || len(entries) != 0 || a.op.confirmed {
				t.Fatalf("explicit delete failed: %v %v", entries, err)
			}
		})
	}
}

func TestRemoteHubRequiresTagName(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	git("tag", "main")
	git("tag", "release")
	remote := filepath.Join(t.TempDir(), "remote.git")
	git("init", "--bare", remote)
	git("remote", "add", "origin", remote)
	updates := make(chan func(), 32)
	repo := newTab(1, dir)
	repo.branch = "main"
	a := &app{tabs: []*repoTab{repo}, dispatch: func(f func()) { updates <- f }}
	t.Cleanup(func() { a.closed = true; repo.closed = true; repo.cancel() })
	a.openOperation("远程", "", "")
	nextDialogUpdate(t, updates)()
	for _, name := range []string{"", "   "} {
		a.op.refName = name
		a.runHubAction("推送标签", false)
		if a.op.busy || a.op.err != "请填写标签名" {
			t.Fatalf("empty tag input accepted: %+v", a.op)
		}
	}
	if refs := git("ls-remote", "--tags", "origin"); len(refs) != 0 {
		t.Fatalf("unrequested tag pushed: %s", refs)
	}
	a.op.refName = "release"
	a.runHubAction("推送标签", false)
	nextDialogUpdate(t, updates)()
	refs := string(git("ls-remote", "--tags", "origin"))
	if a.op.err != "" || !strings.Contains(refs, "refs/tags/release") || strings.Contains(refs, "refs/tags/main") {
		t.Fatalf("wrong tag pushed: %q, error=%q", refs, a.op.err)
	}
}

func TestSessionRestoresDraftProvenance(t *testing.T) {
	for _, p := range []provenance{{agent: "Cindy", model: "exact-model", attribution: "stamp"}, {}} {
		t.Run(p.agent, func(t *testing.T) {
			config := t.TempDir()
			old := userConfigDir
			userConfigDir = func() (string, error) { return config, nil }
			defer func() { userConfigDir = old }()
			dir, git := initTestRepo(t)
			git("commit", "--allow-empty", "-m", "base")
			dir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			repo := newTab(1, dir)
			repo.commitMsg = "draft\n\nAI-Agent: old\nAI-Model: old-model"
			repo.commitAmend = true
			a := &app{tabs: []*repoTab{repo}, persist: true, sessionReady: true}
			a.queueSession()
			previous := a.lastSession
			repo.commitProv, repo.commitProvEdited = p, true
			a.queueSession()
			if a.lastSession == previous {
				t.Fatal("provenance-only edit did not queue a snapshot")
			}
			close(a.sessionWrites)
			<-a.sessionDone
			updates := make(chan func(), 32)
			restored := &app{dispatch: func(f func()) { updates <- f }}
			t.Cleanup(func() {
				restored.closed = true
				for _, tab := range restored.tabs {
					tab.closed = true
					tab.cancel()
				}
			})
			restored.restoreSession()
			nextDialogUpdate(t, updates)()
			if len(restored.tabs) != 1 {
				t.Fatal("draft repository not restored")
			}
			got := restored.tabs[0]
			if got.commitMsg != repo.commitMsg || !got.commitAmend || got.commitProv != p || !got.commitProvEdited {
				t.Fatalf("draft provenance lost: %+v edited=%t", got.commitProv, got.commitProvEdited)
			}
			msg, err := applyProvenanceTrailers(context.Background(), dir, got.commitMsg, got.commitProv)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := messageProvenance(context.Background(), dir, msg)
			if err != nil || parsed.agent != p.agent || parsed.model != p.model || parsed.attribution != p.attribution {
				t.Fatalf("restored draft changed attribution: %+v %v", parsed, err)
			}
		})
	}
}

func TestAmendLoadRespectsProvenanceEdits(t *testing.T) {
	for _, edit := range []string{"none", "clear", "replace"} {
		t.Run(edit, func(t *testing.T) {
			dir, git := initTestRepo(t)
			git("commit", "--allow-empty", "-m", "original\n\nAI-Agent: Cindy\nAI-Model: exact-model")
			head, err := resolveCommit(context.Background(), dir, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			repo := newTab(1, dir)
			repo.head, repo.commitAmend = head, true
			updates := make(chan func(), 8)
			a := &app{dispatch: func(f func()) { updates <- f }}
			a.loadAmendMessage(repo)
			want := provenance{agent: "Cindy", model: "exact-model"}
			switch edit {
			case "clear":
				repo.commitProvEdited = true
				want = provenance{}
			case "replace":
				repo.commitProv = provenance{agent: "Other", model: "other-model"}
				repo.commitProvEdited = true
				want = repo.commitProv
			}
			nextDialogUpdate(t, updates)()
			if repo.commitProv != want {
				t.Fatalf("amend overwrote user edit: %+v, want %+v", repo.commitProv, want)
			}
		})
	}
}

func TestImageCacheCountsSharedImagesAndRestoresSnapshot(t *testing.T) {
	data := makeTestPNG(64, 48)
	info, err := decodeImageInfo(data, "image.png")
	if err != nil {
		t.Fatal(err)
	}
	repo := newTab(1, "/unused")
	snapshot := &fileDiffTab{file: fileChange{path: "image.png"}, fullFile: true, fullImage: info}
	repo.fileTabs = []*fileDiffTab{snapshot}
	want := int64(len(data) + 64*48*4)
	if got := repo.cachedBytes(); got != want {
		t.Fatalf("image budget = %d, want %d", got, want)
	}
	repo.imageDiff = &imageDiff{old: info, new: info}
	repo.diffSource = &fileDiffSource{old: data, new: data}
	repo.historyBack = &historyLocation{imageDiff: repo.imageDiff, source: repo.diffSource}
	if got := repo.cachedBytes(); got != want {
		t.Fatalf("shared image counted twice: %d, want %d", got, want)
	}
	repo.imageDiff, repo.diffSource, repo.historyBack = nil, nil, nil
	active := newTab(2, "/active")
	updates := make(chan func(), 16)
	a := &app{tabs: []*repoTab{repo, active}, active: 1, dispatch: func(f func()) { updates <- f }}
	t.Cleanup(func() { a.closeAllFileTabs(repo) })
	// Use the shared budget with a small real image to avoid large allocations.
	repo.historyBytes = historyMemoryStep
	a.trimInactiveHistory()
	waitUpdates(t, updates, func() bool { return snapshot.evicted })
	if snapshot.fullImage != nil || snapshot.backing == "" || repo.cachedBytes() != historyMemoryStep {
		t.Fatal("inactive image did not spill")
	}
	a.active, repo.contentTab = 0, 1
	a.restoreFileTab(repo, snapshot)
	waitUpdates(t, updates, func() bool { return snapshot.fullImage != nil })
	if snapshot.fullImage.width != 64 || snapshot.fullImage.height != 48 || string(snapshot.fullImage.data) != string(data) {
		t.Fatal("restored image differs from snapshot")
	}
	backing := snapshot.backing
	a.closeFileTab(repo, 0)
	if _, err := os.Stat(backing); !os.IsNotExist(err) {
		t.Fatalf("image snapshot not cleaned up: %v", err)
	}
}

func TestTreeEmptyStateClearsActualFilter(t *testing.T) {
	repo := newTab(1, "/unused")
	repo.compareFiles = []fileChange{{path: "a.go"}}
	repo.fileFilter = "absent"
	a := &app{}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.fileTreeList(c, repo) })
	}, 500, 400)
	defer retireTester(tt)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("清除筛选"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if repo.fileFilter != "" || !tt.HasText("a.go") || tt.HasText("无匹配文件") {
		t.Fatalf("filter not cleared: %q, visible=%q", repo.fileFilter, tt.Texts())
	}
}

func TestHumanStampAppliesWithoutAIFields(t *testing.T) {
	for _, agent := range []string{"human", " HUMAN "} {
		t.Run(agent, func(t *testing.T) {
			dir, git := ringTestRepo(t)
			ctx := context.Background()
			if err := installRingHook(ctx, dir); err != nil {
				t.Fatal(err)
			}
			if err := installProvenanceHook(ctx, dir, "commit-msg", strictHookMarker, strictRingScript); err != nil {
				t.Fatal(err)
			}
			if err := writeStamp(ctx, dir, ringStamp{p: provenance{agent: agent}, expires: time.Now().Add(time.Minute).Unix(), once: true}); err != nil {
				t.Fatal(err)
			}
			git("commit", "--allow-empty", "-m", "manual sample")
			p, err := messageProvenance(ctx, dir, string(git("log", "-1", "--format=%B")))
			if err != nil || p.agent != "human" || p.model != "" || p.attribution != "" {
				t.Fatalf("invalid human attribution: %+v %v", p, err)
			}
			s, err := readStamp(ctx, dir)
			if err != nil || s.p.agent != "" {
				t.Fatalf("single-use human stamp retained: %+v %v", s, err)
			}
		})
	}
}

func TestFullFilePreviewShowsReadFailures(t *testing.T) {
	for _, test := range []struct{ name, path, rev, content, notice string }{
		{"oversized", "large.txt", "worktree", strings.Repeat("x", maxDiffBytes+1), "文件过大"},
		{"binary", "data.bin", "worktree", "\x00binary", "二进制文件"},
		{"invalid revision", "file.txt", "invalid-revision", "text", "invalid"},
		{"empty text", "empty.txt", "worktree", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir, git := initTestRepo(t)
			git("commit", "--allow-empty", "-m", "base")
			if err := os.WriteFile(filepath.Join(dir, test.path), []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			repo := newTab(1, dir)
			updates := make(chan func(), 16)
			a := &app{tabs: []*repoTab{repo}, dispatch: func(f func()) { updates <- f }}
			a.openFullFileTab(repo, test.path, test.rev, "version")
			nextDialogUpdate(t, updates)()
			tab := repo.fileTabs[0]
			tt := ui.NewTester(func(c *ui.Context) { a.fullFileTabPane(c, repo, tab) }, 800, 600)
			defer retireTester(tt)
			tt.Frame()
			tt.Frame()
			if test.notice == "" {
				if tab.note != "" || tab.fullContent.Buffer == nil {
					t.Fatal("valid empty file was treated as a read failure")
				}
			} else if tab.fullContent.Buffer != nil || !strings.Contains(tab.note, test.notice) || !tt.HasText(tab.note) {
				t.Fatalf("read failure hidden: %q, visible=%q", tab.note, tt.Texts())
			}
		})
	}
}
