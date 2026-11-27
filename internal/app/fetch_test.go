package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func writeBlockingUploadPackScript(t *testing.T, started, release string) string {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "blocking-upload-pack.sh")
	script := fmt.Sprintf("#!/bin/sh\n: > '%s'\nwaited=0\nwhile [ ! -f '%s' ] && [ \"$waited\" -lt 250 ]; do\n\tsleep 0.02\n\twaited=$((waited + 1))\ndone\nexit 1\n", started, release)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return scriptPath
}

func TestHangingFetchDoesNotBlockLocalRefreshOrWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell upload-pack helper")
	}
	origin, gitOrigin := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOrigin("add", "a.txt")
	gitOrigin("commit", "-m", "base")
	local := cloneTestRepo(t, origin)
	if _, err := runGit(context.Background(), local, "config", "user.email", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), local, "config", "user.name", "Test"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), local, "config", "commit.gpgsign", "false"); err != nil {
		t.Fatal(err)
	}

	tab := newTab(1, local)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, autoFetch: "2", dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })

	// 场景 1：后台自动获取卡住时，本地刷新、暂存、提交正常执行。
	startedAuto := filepath.Join(t.TempDir(), "started-auto")
	releaseAuto := filepath.Join(t.TempDir(), "release-auto")
	defer os.WriteFile(releaseAuto, nil, 0o600)
	scriptAuto := writeBlockingUploadPackScript(t, startedAuto, releaseAuto)
	if _, err := runGit(context.Background(), local, "config", "remote.origin.uploadpack", scriptAuto); err != nil {
		t.Fatal(err)
	}

	a.maybeAutoFetch()
	waitForFile(t, startedAuto)
	if !tab.fetching {
		t.Fatal("expected auto fetch to be in flight")
	}

	if err := os.WriteFile(filepath.Join(local, "a.txt"), []byte("during-auto\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.checkActiveRepo()
	waitUpdates(t, updates, func() bool { return !tab.checking })
	if tab.err != "" || tab.worktreeCount != 1 {
		t.Fatalf("checkActiveRepo blocked or failed during auto fetch: err=%q dirty=%d", tab.err, tab.worktreeCount)
	}

	a.stageWorktreeFile(tab, "a.txt")
	waitUpdates(t, updates, func() bool {
		return !tab.commitBusy && len(tab.worktreeFiles) > 0 && tab.worktreeFiles[0].hasStaged()
	})
	tab.commitMsg = "commit during auto fetch"
	a.runCommitWorktree(tab)
	waitUpdates(t, updates, func() bool { return !tab.commitBusy && !tab.loading && tab.worktreeCache != nil })
	if tab.commitErr != "" {
		t.Fatalf("commit failed during auto fetch: %s", tab.commitErr)
	}
	if !tab.fetching {
		t.Fatal("auto fetch should still have been isolated in background")
	}

	if err := os.WriteFile(releaseAuto, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.fetching })

	// 场景 2：手动 quickFetch 卡住时，本地刷新与提交照样成功，且可以一键取消。
	startedManual := filepath.Join(t.TempDir(), "started-manual")
	releaseManual := filepath.Join(t.TempDir(), "release-manual")
	defer os.WriteFile(releaseManual, nil, 0o600)
	scriptManual := writeBlockingUploadPackScript(t, startedManual, releaseManual)
	if _, err := runGit(context.Background(), local, "config", "remote.origin.uploadpack", scriptManual); err != nil {
		t.Fatal(err)
	}

	a.quickFetch(tab)
	waitForFile(t, startedManual)
	if !tab.manualFetchBusy {
		t.Fatal("expected quickFetch to be running")
	}

	if err := os.WriteFile(filepath.Join(local, "a.txt"), []byte("during-manual\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.checkActiveRepo()
	waitUpdates(t, updates, func() bool { return !tab.checking })
	if tab.err != "" || tab.worktreeCount != 1 {
		t.Fatalf("checkActiveRepo blocked during quickFetch: err=%q dirty=%d", tab.err, tab.worktreeCount)
	}

	a.stageWorktreeFile(tab, "a.txt")
	waitUpdates(t, updates, func() bool {
		return !tab.commitBusy && len(tab.worktreeFiles) > 0 && tab.worktreeFiles[0].hasStaged()
	})
	tab.commitMsg = "commit during quick fetch"
	a.runCommitWorktree(tab)
	waitUpdates(t, updates, func() bool { return !tab.commitBusy && !tab.loading && tab.worktreeCache != nil })
	if tab.commitErr != "" {
		t.Fatalf("commit failed during quickFetch: %s", tab.commitErr)
	}
	if !tab.manualFetchBusy {
		t.Fatal("quickFetch should still be running independently")
	}

	a.cancelManualFetch(tab)
	if tab.manualFetchBusy || tab.notice != "" || a.pendingToast != "已取消获取远程" {
		t.Fatalf("cancelManualFetch left unexpected state: busy=%v notice=%q toast=%q", tab.manualFetchBusy, tab.notice, a.pendingToast)
	}
	out, err := runGit(context.Background(), local, "log", "-1", "--format=%s")
	if err != nil || strings.TrimSpace(string(out)) != "commit during quick fetch" {
		t.Fatalf("unexpected HEAD commit: out=%q err=%v", out, err)
	}
}

func TestAutoFetchFailureBackoff(t *testing.T) {
	origin, gitOrigin := initTestRepo(t)
	gitOrigin("commit", "--allow-empty", "-m", "base")
	local := cloneTestRepo(t, origin)
	missing := filepath.Join(t.TempDir(), "missing.git")
	if _, err := runGit(context.Background(), local, "remote", "set-url", "origin", missing); err != nil {
		t.Fatal(err)
	}

	tab := newTab(1, local)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, autoFetch: "2", dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading })

	// 第 1 次失败：退避约 4 分钟。
	a.maybeAutoFetch()
	waitUpdates(t, updates, func() bool { return !tab.fetching })
	if tab.fetchFailCount != 1 {
		t.Fatalf("fetchFailCount=%d, want 1", tab.fetchFailCount)
	}
	if d := time.Until(tab.nextAutoFetch); d < 3*time.Minute+50*time.Second || d > 4*time.Minute+5*time.Second {
		t.Fatalf("1st backoff=%v, want ~4m", d)
	}

	// 退避期内再次调用不触发。
	a.maybeAutoFetch()
	if tab.fetching {
		t.Fatal("auto fetch ran inside backoff window")
	}

	// 第 2 次失败：退避约 8 分钟。
	tab.nextAutoFetch = time.Now().Add(-time.Second)
	a.maybeAutoFetch()
	waitUpdates(t, updates, func() bool { return !tab.fetching })
	if tab.fetchFailCount != 2 {
		t.Fatalf("fetchFailCount=%d, want 2", tab.fetchFailCount)
	}
	if d := time.Until(tab.nextAutoFetch); d < 7*time.Minute+50*time.Second || d > 8*time.Minute+5*time.Second {
		t.Fatalf("2nd backoff=%v, want ~8m", d)
	}

	// 第 3 次失败：封顶 15 分钟。
	tab.nextAutoFetch = time.Now().Add(-time.Second)
	a.maybeAutoFetch()
	waitUpdates(t, updates, func() bool { return !tab.fetching })
	if tab.fetchFailCount != 3 {
		t.Fatalf("fetchFailCount=%d, want 3", tab.fetchFailCount)
	}
	if d := time.Until(tab.nextAutoFetch); d < 14*time.Minute+50*time.Second || d > 15*time.Minute+5*time.Second {
		t.Fatalf("3rd backoff=%v, want ~15m", d)
	}

	// 恢复可用远程并成功后清零退避计数。
	if _, err := runGit(context.Background(), local, "remote", "set-url", "origin", origin); err != nil {
		t.Fatal(err)
	}
	tab.nextAutoFetch = time.Now().Add(-time.Second)
	a.maybeAutoFetch()
	waitUpdates(t, updates, func() bool { return !tab.fetching && !tab.checking && !tab.loading })
	if tab.fetchFailCount != 0 || !tab.nextAutoFetch.IsZero() {
		t.Fatalf("backoff did not reset after success: count=%d next=%v", tab.fetchFailCount, tab.nextAutoFetch)
	}
}

func TestQuickFetchOnOneRepoDoesNotBlockAnotherRepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell upload-pack helper")
	}
	originA, gitOriginA := initTestRepo(t)
	gitOriginA("commit", "--allow-empty", "-m", "base-a")
	repoA := cloneTestRepo(t, originA)

	originB, gitOriginB := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(originB, "b.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOriginB("add", "b.txt")
	gitOriginB("commit", "-m", "base-b")
	repoB := cloneTestRepo(t, originB)
	gitOriginB("checkout", "-b", "remote-only")
	gitOriginB("commit", "--allow-empty", "-m", "remote")
	gitOriginB("checkout", "main")
	if _, err := runGit(context.Background(), repoB, "config", "user.email", "test@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), repoB, "config", "user.name", "Test"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), repoB, "config", "commit.gpgsign", "false"); err != nil {
		t.Fatal(err)
	}

	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	defer os.WriteFile(release, nil, 0o600)
	script := writeBlockingUploadPackScript(t, started, release)
	if _, err := runGit(context.Background(), repoA, "config", "remote.origin.uploadpack", script); err != nil {
		t.Fatal(err)
	}

	tabA := newTab(1, repoA)
	tabB := newTab(2, repoB)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tabA, tabB}, active: 0, autoFetch: "2", dispatch: func(fn func()) { updates <- fn }}
	defer tabA.cancel()
	defer tabB.cancel()

	a.reload(tabA)
	waitUpdates(t, updates, func() bool { return !tabA.loading })
	a.active = 1
	a.reload(tabB)
	waitUpdates(t, updates, func() bool { return !tabB.loading })

	a.active = 0
	a.quickFetch(tabA)
	waitForFile(t, started)
	defer a.cancelManualFetch(tabA)

	// 切换到仓库 B：自动获取、本地刷新和本地提交都不受仓库 A 卡住的影响。
	a.active = 1
	a.maybeAutoFetch()
	waitUpdates(t, updates, func() bool { return !tabB.fetching && !tabB.checking && !tabB.loading })
	if !hasRefName(tabB.refs, "origin/remote-only") {
		t.Fatal("repo B auto fetch was blocked by repo A quickFetch")
	}

	if err := os.WriteFile(filepath.Join(repoB, "b.txt"), []byte("updated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.checkActiveRepo()
	waitUpdates(t, updates, func() bool { return !tabB.checking })
	if tabB.err != "" || tabB.worktreeCount != 1 {
		t.Fatalf("repo B checkActiveRepo failed: err=%q dirty=%d", tabB.err, tabB.worktreeCount)
	}

	a.stageWorktreeFile(tabB, "b.txt")
	waitUpdates(t, updates, func() bool {
		return !tabB.commitBusy && len(tabB.worktreeFiles) > 0 && tabB.worktreeFiles[0].hasStaged()
	})
	tabB.commitMsg = "repo b commit"
	a.runCommitWorktree(tabB)
	waitUpdates(t, updates, func() bool { return !tabB.commitBusy && !tabB.loading && tabB.worktreeCache != nil })
	if tabB.commitErr != "" {
		t.Fatalf("repo B commit failed: %s", tabB.commitErr)
	}
	if !tabA.manualFetchBusy {
		t.Fatal("repo A quickFetch should still be running")
	}
}
