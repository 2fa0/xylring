package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cloneTestRepo(t *testing.T, origin string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := runGit(context.Background(), dir, "clone", origin, "."); err != nil {
		t.Fatal(err)
	}
	return dir
}

func hasRefName(refs map[[20]byte][]refInfo, name string) bool {
	for _, list := range refs {
		for _, ref := range list {
			if ref.name == name {
				return true
			}
		}
	}
	return false
}

func TestFetchAllUpdatesRemoteBranch(t *testing.T) {
	origin, gitOrigin := initTestRepo(t)
	os.WriteFile(filepath.Join(origin, "a.txt"), []byte("one\n"), 0o600)
	gitOrigin("add", "a.txt")
	gitOrigin("commit", "-m", "base")
	local := cloneTestRepo(t, origin)
	gitOrigin("checkout", "-b", "feature")
	gitOrigin("commit", "--allow-empty", "-m", "feature")
	gitOrigin("checkout", "main")
	ctx := context.Background()
	if err := fetchAll(ctx, local, fetchOpts{}); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRefName(meta.refs, "origin/feature") {
		t.Fatalf("missing origin/feature: %+v", meta.refs)
	}
}

func TestFetchAllPrunesDeletedBranch(t *testing.T) {
	isolateSettingsGit(t)
	origin, gitOrigin := initTestRepo(t)
	gitOrigin("commit", "--allow-empty", "-m", "base")
	gitOrigin("checkout", "-b", "gone")
	gitOrigin("commit", "--allow-empty", "-m", "gone")
	gitOrigin("checkout", "main")
	local := cloneTestRepo(t, origin)
	ctx := context.Background()
	meta, err := loadMeta(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRefName(meta.refs, "origin/gone") {
		t.Fatal("clone missing origin/gone")
	}
	gitOrigin("branch", "-D", "gone")
	if err := fetchAll(ctx, local, fetchOpts{explicit: true}); err != nil {
		t.Fatal(err)
	}
	meta, err = loadMeta(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRefName(meta.refs, "origin/gone") {
		t.Fatal("fetch without prune dropped origin/gone")
	}
	if err := fetchAll(ctx, local, fetchOpts{prune: true}); err != nil {
		t.Fatal(err)
	}
	meta, err = loadMeta(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	if hasRefName(meta.refs, "origin/gone") {
		t.Fatal("prune left origin/gone")
	}
}

func TestFetchAllRequiresRemote(t *testing.T) {
	dir, _ := initTestRepo(t)
	err := fetchAll(context.Background(), dir, fetchOpts{})
	if err == nil || !strings.Contains(err.Error(), "没有配置远程") {
		t.Fatalf("no remote: %v", err)
	}
}

func TestFetchReloadsNewRemoteBranch(t *testing.T) {
	origin, gitOrigin := initTestRepo(t)
	gitOrigin("commit", "--allow-empty", "-m", "base")
	local := cloneTestRepo(t, origin)
	gitOrigin("checkout", "-b", "feature")
	gitOrigin("commit", "--allow-empty", "-m", "feature")
	gitOrigin("checkout", "main")
	tab := newTab(1, local)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.openFetch(tab)
	waitUpdates(t, updates, func() bool { return !a.fetch.preparing })
	if len(a.fetch.remotes) != 1 || a.fetch.remotes[0] != "origin" {
		t.Fatalf("remotes %+v err=%q", a.fetch.remotes, a.fetch.err)
	}
	a.runFetch()
	waitUpdates(t, updates, func() bool { return !a.fetch.busy && !tab.loading })
	if !hasRefName(tab.refs, "origin/feature") {
		t.Fatal("graph missing origin/feature")
	}
	if tab.notice != "已获取远程更新" {
		t.Fatalf("notice %q", tab.notice)
	}
}

func TestAutoFetchUpdatesRemoteBranch(t *testing.T) {
	origin, gitOrigin := initTestRepo(t)
	gitOrigin("commit", "--allow-empty", "-m", "base")
	local := cloneTestRepo(t, origin)
	gitOrigin("checkout", "-b", "feature")
	gitOrigin("commit", "--allow-empty", "-m", "feature")
	gitOrigin("checkout", "main")
	tab := newTab(1, local)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	if hasRefName(tab.refs, "origin/feature") {
		t.Fatal("clone already had origin/feature")
	}
	a.maybeAutoFetch()
	waitUpdates(t, updates, func() bool { return !tab.fetching && !tab.checking && !tab.loading })
	if !hasRefName(tab.refs, "origin/feature") {
		t.Fatal("auto fetch missed origin/feature")
	}
	gen := tab.generation
	a.maybeAutoFetch()
	if tab.fetching {
		t.Fatal("auto fetch ran again before the interval")
	}
	if tab.generation != gen {
		t.Fatal("interval skip still reloaded")
	}
}

func TestAutoFetchSkipsRepoWithoutRemote(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })
	a.maybeAutoFetch()
	waitUpdates(t, updates, func() bool { return !tab.fetching })
	if tab.lastFetch.IsZero() {
		t.Fatal("missing remote should still record a fetch attempt")
	}
	if tab.notice != "" {
		t.Fatalf("auto fetch without remote set notice %q", tab.notice)
	}
}

func TestFetchRespectsConfiguredPruneAndExplicitOptOut(t *testing.T) {
	isolateSettingsGit(t)
	origin, gitOrigin := initTestRepo(t)
	gitOrigin("commit", "--allow-empty", "-m", "base")
	gitOrigin("branch", "gone")
	local := cloneTestRepo(t, origin)
	gitOrigin("branch", "-D", "gone")
	ctx := context.Background()
	if _, err := runGit(ctx, local, "config", "fetch.prune", "true"); err != nil {
		t.Fatal(err)
	}
	if err := fetchAll(ctx, local, fetchOpts{explicit: true}); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRefName(meta.refs, "origin/gone") {
		t.Fatal("explicit opt-out ignored")
	}
	if err := fetchAll(ctx, local, fetchOpts{}); err != nil {
		t.Fatal(err)
	}
	meta, err = loadMeta(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	if hasRefName(meta.refs, "origin/gone") {
		t.Fatal("configured prune ignored")
	}
}
