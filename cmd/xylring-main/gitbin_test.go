package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// isolateGit pins the resolver to a candidate list and an empty PATH, so a test
// never picks up the Git installed on the machine running it.
func isolateGit(t *testing.T, candidates []string) {
	t.Helper()
	old := gitCommonPaths
	gitCommonPaths = func() []string { return candidates }
	gitResolve.Lock()
	gitResolve.override = ""
	gitResolve.checked = false
	gitResolve.failure = nil
	gitResolve.Unlock()
	t.Cleanup(func() {
		gitCommonPaths = old
		gitResolve.Lock()
		gitResolve.override = ""
		gitResolve.checked = false
		gitResolve.failure = nil
		gitResolve.Unlock()
	})
}

// fakeGit writes a stand-in binary: enough for `--version` probing without a
// real Git install.
func fakeGit(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake git is a shell script")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMissingGitReportsInstallHint(t *testing.T) {
	t.Setenv("PATH", "")
	isolateGit(t, nil)
	_, err := gitPath()
	if !isGitUnavailable(err) {
		t.Fatalf("missing Git is not reported as unavailable: %v", err)
	}
	if !strings.Contains(err.Error(), "未找到 Git") || !strings.Contains(err.Error(), "设置") {
		t.Fatalf("message does not say what to do: %q", err)
	}
}

func TestBrokenGitIsReportedAsUnrunnable(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	broken := fakeGit(t, dir, "git", `echo "xcode-select: error: no developer tools were found" >&2; exit 1`)
	isolateGit(t, []string{broken})
	_, err := gitPath()
	if !isGitUnavailable(err) {
		t.Fatalf("broken Git is not reported as unavailable: %v", err)
	}
	if strings.Contains(err.Error(), "未找到 Git") {
		t.Fatalf("installed but broken Git reported as missing: %q", err)
	}
	if !strings.Contains(err.Error(), "Git 无法运行") || !strings.Contains(err.Error(), "developer tools") {
		t.Fatalf("message hides why Git failed: %q", err)
	}
}

func TestProbeSkipsBrokenBinaryAndFindsWorkingOne(t *testing.T) {
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty"))
	dir := t.TempDir()
	broken := fakeGit(t, dir, "broken", "exit 1")
	good := fakeGit(t, dir, "good", `echo "git version 2.44.0"`)
	isolateGit(t, []string{broken, good})
	probe, err := resolveGit()
	if err != nil {
		t.Fatalf("working Git ignored after a broken one: %v", err)
	}
	if probe.path != good {
		t.Fatalf("resolved %q, want %q", probe.path, good)
	}
	if probe.version != "2.44.0" {
		t.Fatalf("version %q, want 2.44.0", probe.version)
	}
	if strings.Contains(os.Getenv("PATH"), dir) {
		t.Fatalf("resolver changed the application's PATH: %q", os.Getenv("PATH"))
	}
	env := gitBinaryEnv(probe.path, gitEnv())
	if !slices.Contains(env, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH")) {
		t.Fatalf("Git directory not exposed to child processes: %q", env)
	}
}

func TestConfiguredGitPathIsUsedAndValidated(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	good := fakeGit(t, dir, "git", `echo "git version 2.39.3"`)
	isolateGit(t, nil)
	setGitExecutablePath(good)
	probe, err := resolveGit()
	if err != nil || probe.path != good {
		t.Fatalf("configured Git ignored: %q %v", probe.path, err)
	}
	setGitExecutablePath(filepath.Join(dir, "missing"))
	_, err = gitPath()
	if !isGitUnavailable(err) || !strings.Contains(err.Error(), "设置的 Git 路径不可用") {
		t.Fatalf("unusable configured path not reported: %v", err)
	}
}

func TestClearingGitOverrideRestoresAutomaticDiscovery(t *testing.T) {
	auto := fakeGit(t, t.TempDir(), "git", `echo "git version 2.40.0"`)
	custom := fakeGit(t, t.TempDir(), "git", `echo "git version 2.44.0"`)
	t.Setenv("PATH", filepath.Dir(auto))
	isolateGit(t, nil)
	setGitExecutablePath(custom)
	if _, err := resolveGit(); err != nil {
		t.Fatal(err)
	}
	setGitExecutablePath("")
	probe, err := resolveGit()
	if err != nil || probe.path != auto {
		t.Fatalf("automatic Git = %q, want %q: %v", probe.path, auto, err)
	}
}

func TestRelativeGitPathWorksFromRepositoryDirectory(t *testing.T) {
	dir := t.TempDir()
	bin := fakeGit(t, dir, "git", `echo "git version 2.44.0"`)
	t.Chdir(dir)
	t.Setenv("PATH", dir)
	isolateGit(t, nil)
	for _, input := range []string{"./git", "git"} {
		if path := setGitExecutablePath(input); path != bin {
			t.Fatalf("stored relative executable: %q", path)
		}
		if _, err := resolveGit(); err != nil {
			t.Fatal(err)
		}
		out, err := runGit(context.Background(), t.TempDir(), "--version")
		if err != nil || !strings.Contains(string(out), "2.44.0") {
			t.Fatalf("repository command failed after successful probe: %q %v", out, err)
		}
	}
}

func TestGitPathSubmissionDuringProbeDoesNotChangeConfiguration(t *testing.T) {
	a := &app{git: gitState{checking: true}, gitExecutable: "/original/git"}
	a.applyGitExecutablePath("/other/git")
	if a.gitExecutable != "/original/git" {
		t.Fatal("path submission bypassed the in-flight probe guard")
	}
	tt := ui.NewTester(a.gitBinarySettings, 720, 800)
	defer retireTester(tt)
	tt.Frame()
	field, ok := tt.AccessByRole(ui.RoleTextField, "Git 可执行文件路径")
	if !ok || !field.Disabled {
		t.Fatal("Git path input is editable during a probe")
	}
}

func TestGitProbeDiscardsSupersededResults(t *testing.T) {
	bin := fakeGit(t, t.TempDir(), "git", `echo "git version 2.44.0"`)
	t.Setenv("PATH", filepath.Dir(bin))
	isolateGit(t, nil)
	updates := make(chan func(), 1)
	a := &app{dispatch: func(fn func()) { updates <- fn }}
	a.checkGit(false)
	var finish func()
	select {
	case finish = <-updates:
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not complete")
	}
	a.noteGitUnavailable(&gitUnavailableError{message: "Git stopped working"})
	finish()
	if a.git.err != "Git stopped working" {
		t.Fatal("a superseded success overwrote a newer Git failure")
	}
}

func TestSwitchingGitReprobesTrailerCapability(t *testing.T) {
	old := fakeGit(t, t.TempDir(), "git", `if [ "$1" = "--version" ]; then echo "git version 2.20.0"; else echo '%(trailers:key=AI-Agent)'; fi`)
	newer := fakeGit(t, t.TempDir(), "git", `if [ "$1" = "--version" ]; then echo "git version 2.44.0"; else echo 'AI-Agent: Cindy'; fi`)
	t.Setenv("PATH", filepath.Dir(old))
	isolateGit(t, nil)
	ctx, dir := context.Background(), t.TempDir()
	for _, step := range []struct {
		path string
		want bool
	}{{old, false}, {newer, true}, {old, false}} {
		setGitExecutablePath(step.path)
		if _, supported := historyCommitFormat(ctx, dir); supported != step.want {
			t.Fatalf("trailer support for %s = %v, want %v", step.path, supported, step.want)
		}
	}
}

func TestFailedResolutionIsReusedUntilRetryWindow(t *testing.T) {
	t.Setenv("PATH", "")
	isolateGit(t, nil)
	if _, err := gitPath(); !isGitUnavailable(err) {
		t.Fatalf("missing Git not reported: %v", err)
	}
	dir := t.TempDir()
	good := fakeGit(t, dir, "git", `echo "git version 2.44.0"`)
	gitCommonPaths = func() []string { return []string{good} }
	if _, err := gitPath(); err == nil {
		t.Fatal("a cached failure must be reused inside the retry window")
	}
	gitResolve.Lock()
	gitResolve.failedAt = time.Now().Add(-2 * gitRetryFailedAfter)
	gitResolve.Unlock()
	if probe, err := resolveGit(); err != nil || probe.path != good {
		t.Fatalf("resolver did not pick up a newly installed Git: %q %v", probe.path, err)
	}
}

func TestVanishedGitBinaryIsReportedAsUnavailable(t *testing.T) {
	dir := t.TempDir()
	bin := fakeGit(t, dir, "git", "exit 0")
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	gone := gitExecError(bin, &os.PathError{Op: "fork/exec", Path: bin, Err: fs.ErrNotExist})
	if !isGitUnavailable(gone) || !strings.Contains(gone.Error(), "已不存在") {
		t.Fatalf("vanished binary not reported: %v", gone)
	}
	// 工作目录消失时 Git 本身没问题，不能顺手报成缺 Git。
	other := gitExecError(bin, &os.PathError{Op: "chdir", Path: filepath.Join(dir, "gone"), Err: fs.ErrNotExist})
	if isGitUnavailable(other) {
		t.Fatalf("unrelated path failure reported as Git missing: %v", other)
	}
}

func TestRepoRootReportsGitProblemInsteadOfNotARepository(t *testing.T) {
	t.Setenv("PATH", "")
	isolateGit(t, nil)
	_, err := repoRoot(context.Background(), t.TempDir())
	if !isGitUnavailable(err) {
		t.Fatalf("missing Git reported as %v, want an unavailable-Git error", err)
	}
}

func TestScanGitReposReportsGitProblem(t *testing.T) {
	t.Setenv("PATH", "")
	isolateGit(t, nil)
	_, _, err := scanGitRepos(context.Background(), t.TempDir(), nil)
	if !isGitUnavailable(err) {
		t.Fatalf("scan hid the missing Git: %v", err)
	}
}

func TestRestoredRepoUsableKeepsReposWhenGitIsMissing(t *testing.T) {
	unavailable := &gitUnavailableError{message: "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径"}
	if !restoredRepoUsable("", "/repo", unavailable) {
		t.Fatal("a repository dropped because Git is missing loses its draft and tab")
	}
	if !restoredRepoUsable("/repo", "/repo", nil) {
		t.Fatal("a resolvable repository must be restored")
	}
	if restoredRepoUsable("/other", "/repo", nil) {
		t.Fatal("a nested path must not replace the saved repository")
	}
	if restoredRepoUsable("", "/repo", errors.New("不是 Git 仓库")) {
		t.Fatal("a directory that is not a repository must not be restored")
	}
}

func TestOperationsRefuseWithoutGit(t *testing.T) {
	a := &app{git: gitState{checked: true, err: "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径"}}
	a.openOperation("克隆仓库", "", "")
	if a.showOperation {
		t.Fatal("operation dialog opened without Git")
	}
	if a.banner == "" {
		t.Fatal("no explanation shown for the refused operation")
	}
}

func TestGitNoticeStatesTheProblemAndOffersRetry(t *testing.T) {
	a := &app{git: gitState{checked: true, err: "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径"}}
	tt := ui.NewTester(func(c *ui.Context) { a.gitNoticeCard(c) }, 460, 320)
	defer retireTester(tt)
	tt.Frame()
	for _, want := range []string{"Git 未就绪", "重新检测", "指定 Git 路径…", "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
}

func TestGitNoticeIsTranslated(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	a := &app{git: gitState{checked: true, err: "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径"}}
	tt := ui.NewTester(func(c *ui.Context) { a.gitNoticeCard(c) }, 460, 320)
	defer retireTester(tt)
	tt.Frame()
	assertEnglishUITexts(t, tt)
	if !tt.HasText("Git is not ready") || !tt.HasText("Check again") {
		t.Fatalf("missing English notice: %q", tt.Texts())
	}
}

func TestGitSettingsShowStatusAndPath(t *testing.T) {
	a := &app{git: gitState{checked: true, path: "/usr/local/bin/git", version: "2.44.0"}}
	tt := ui.NewTester(func(c *ui.Context) { a.gitBinarySettings(c) }, 720, 400)
	defer retireTester(tt)
	tt.Frame()
	for _, want := range []string{"Git 程序", "Git 状态", "2.44.0 · /usr/local/bin/git", "Git 可执行文件路径"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}

	broken := &app{git: gitState{checked: true, err: "Git 无法运行：/usr/bin/git（no developer tools）。请重新安装 Git，或在设置中指定可用的 Git 可执行文件路径"}}
	bad := ui.NewTester(func(c *ui.Context) { broken.gitBinarySettings(c) }, 720, 400)
	defer retireTester(bad)
	bad.Frame()
	if !bad.HasText("Git 无法运行：/usr/bin/git（no developer tools）。请重新安装 Git，或在设置中指定可用的 Git 可执行文件路径") {
		t.Fatalf("Git failure not shown in settings: %q", bad.Texts())
	}
}

func TestRepositoryFailureSurfacesUnavailableGit(t *testing.T) {
	a := &app{}
	a.noteGitUnavailable(errors.New("不是 Git 仓库"))
	if !a.gitReady() {
		t.Fatal("an unrelated repository error must not be reported as missing Git")
	}
	a.noteGitUnavailable(&gitUnavailableError{message: "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径"})
	if a.gitReady() {
		t.Fatal("a Git failure found by a repository was not surfaced")
	}
	if a.git.err != "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径" {
		t.Fatalf("unexpected notice: %q", a.git.err)
	}
}

func TestAutoRefreshStopsWhenGitIsUnavailable(t *testing.T) {
	tab := newTab(1, "/tmp/example")
	tab.loaded = true
	a := &app{tabs: []*repoTab{tab}, git: gitState{checked: true, err: "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径"}}
	a.checkActiveRepo()
	if tab.checking {
		t.Fatal("refresh keeps polling Git that cannot run")
	}
}

func TestResolveGitFindsTheSystemGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no Git on PATH")
	}
	resetGitResolution()
	probe, err := resolveGit()
	if err != nil {
		t.Fatalf("system Git not resolved: %v", err)
	}
	if info, err := os.Stat(probe.path); err != nil || info.IsDir() {
		t.Fatalf("resolved path is not a binary: %q %v", probe.path, err)
	}
	if major, _, ok := strings.Cut(probe.version, "."); !ok || major == "" || strings.Trim(major, "0123456789") != "" {
		t.Fatalf("unexpected version string: %q", probe.version)
	}
}

func TestUnavailableGitOffersRetryInsideRepository(t *testing.T) {
	tab := newTab(1, "/tmp/example")
	tab.loaded, tab.err = true, "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径"
	a := &app{tabs: []*repoTab{tab}, git: gitState{checked: true, err: tab.err}}
	tt := ui.NewTester(func(c *ui.Context) { a.commitPane(c, tab, false) }, 700, 400)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径") {
		t.Fatalf("repository failure not shown: %q", tt.Texts())
	}
	if !tt.HasText("重新检测") {
		t.Fatalf("no retry action for a missing Git: %q", tt.Texts())
	}
}

func TestRecoveringGitRetriesEveryFailedRepository(t *testing.T) {
	first, second := newTab(1, "/tmp/first"), newTab(2, "/tmp/second")
	first.loaded, second.loaded = true, true
	first.err = "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径"
	second.err = first.err
	healthy := newTab(3, "/tmp/healthy")
	healthy.loaded = true
	a := &app{tabs: []*repoTab{first, second, healthy}, dispatch: func(func()) {}}
	a.setBanner("未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径", "")
	a.resumeAfterGit()
	if a.banner != "" {
		t.Fatalf("Git notice kept after recovery: %q", a.banner)
	}
	for _, tab := range []*repoTab{first, second} {
		if tab.err != "" || !tab.loading || tab.loaded {
			t.Fatalf("%s not reloaded after recovery: err=%q loading=%v loaded=%v", tab.path, tab.err, tab.loading, tab.loaded)
		}
	}
	if healthy.loading || !healthy.loaded {
		t.Fatal("a healthy repository was reloaded")
	}
}
