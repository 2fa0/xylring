package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func isolateSettingsGit(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", path)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_COUNT", "0")
	return path
}
func TestGitSettingsScopesAndInheritance(t *testing.T) {
	global := isolateSettingsGit(t)
	repo, _ := initTestRepo(t)
	ctx := context.Background()
	if _, err := configCommand(ctx, repo, "--global", "user.email", "global@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := configCommand(ctx, repo, "--local", "--unset-all", "user.email"); err != nil {
		t.Fatal(err)
	}
	// Included identity values remain in their own files when an override is removed.
	include := filepath.Join(t.TempDir(), "work.gitconfig")
	if err := os.WriteFile(include, []byte("[user]\n email = work@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configCommand(ctx, repo, "--global", "includeIf.gitdir:"+filepath.ToSlash(realRepo)+"/.path", include); err != nil {
		t.Fatal(err)
	}
	fields, err := readGitSettings(ctx, repo, "--local")
	if err != nil {
		t.Fatal(err)
	}
	if fields[1].enabled || fields[1].effective != "work@example.com" || !strings.Contains(fields[1].origin, include) {
		t.Fatalf("inherited %+v", fields[1])
	}
	fields[1].enabled, fields[1].value = true, "project@example.com"
	if err := saveGitSettings(ctx, repo, "--local", fields); err != nil {
		t.Fatal(err)
	}
	fields, err = readGitSettings(ctx, repo, "--local")
	if err != nil {
		t.Fatal(err)
	}
	if fields[1].effective != "project@example.com" || fields[1].scope != "local" {
		t.Fatalf("override %+v", fields[1])
	}
	fields[1].enabled = false
	if err := saveGitSettings(ctx, repo, "--local", fields); err != nil {
		t.Fatal(err)
	}
	fields, err = readGitSettings(ctx, repo, "--local")
	if err != nil {
		t.Fatal(err)
	}
	if fields[1].enabled || fields[1].effective != "work@example.com" {
		t.Fatalf("restored %+v", fields[1])
	}
	b, _ := os.ReadFile(global)
	if !strings.Contains(string(b), "global@example.com") {
		t.Fatal("project edit changed global identity")
	}
	b, _ = os.ReadFile(include)
	if !strings.Contains(string(b), "work@example.com") {
		t.Fatal("project edit changed include")
	}
}
func TestGitSettingsNoWritesOnReadAndStaleEdits(t *testing.T) {
	path := isolateSettingsGit(t)
	ctx := context.Background()
	fields, err := readGitSettings(ctx, "", "--global")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reading settings created config")
	}
	fields[0].enabled, fields[0].value = true, "Draft"
	if _, err := configCommand(ctx, "", "--global", "user.name", "External"); err != nil {
		t.Fatal(err)
	}
	if err := saveGitSettings(ctx, "", "--global", fields); err == nil {
		t.Fatal("stale form overwrote external edit")
	}
	b, _ := configCommand(ctx, "", "--global", "--get", "user.name")
	if strings.TrimSpace(string(b)) != "External" {
		t.Fatal("external value lost")
	}
	fields, err = readGitSettings(ctx, "", "--global")
	if err != nil {
		t.Fatal(err)
	}
	fields[1].enabled, fields[1].value = true, "new@example.com"
	fields[5].enabled, fields[5].value = true, "bad..branch"
	if err := saveGitSettings(ctx, "", "--global", fields); err == nil {
		t.Fatal("invalid branch accepted")
	}
	if _, err := configCommand(ctx, "", "--global", "--get", "user.email"); !configExit(err, 1) {
		t.Fatal("validation wrote partial configuration")
	}
}
func TestSettingsPreferencesAndProjectFetchPersist(t *testing.T) {
	old := userConfigDir
	config := t.TempDir()
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()
	a := newApp()
	a.sessionReady = true
	a.restoreDisabled = true
	a.fileView, a.diffLayout = 1, 1
	a.diffIgnoreWhitespace = true
	a.autoFetch = "5"
	a.language = "en"
	a.repoAutoFetch = map[string]string{"/repo": "off"}
	a.queueSession()
	a.repoAutoFetch["/repo"] = "10"
	if a.lastSession.RepoAutoFetch["/repo"] != "off" {
		t.Fatal("queued settings alias mutable map")
	}
	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone
	b := newApp()
	if b.preferences() != a.preferences() || b.repoAutoFetch["/repo"] != "10" {
		t.Fatalf("preferences lost: %+v %+v", b.preferences(), b.repoAutoFetch)
	}
	if b.autoFetchDelay("/other") != 5*time.Minute || b.autoFetchDelay("/repo") != 10*time.Minute {
		t.Fatal("fetch inheritance failed")
	}
	b.repoAutoFetch["/repo"] = "off"
	if b.autoFetchDelay("/repo") != 0 {
		t.Fatal("project disable ignored")
	}
	delete(b.repoAutoFetch, "/repo")
	if b.autoFetchDelay("/repo") != 5*time.Minute {
		t.Fatal("restore fetch inheritance failed")
	}
}

// clickControl clicks a control by its name instead of by text, so a setting
// whose name is drawn beside its control still reaches the control.
func clickControl(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	node, ok := tt.AccessByRole(ui.RoleButton, name)
	if !ok {
		node, ok = tt.AccessByRole(ui.RoleMenuButton, name)
	}
	if !ok {
		t.Fatalf("missing control %q", name)
	}
	tt.ClickAt(node.Bounds.X+node.Bounds.W/2, node.Bounds.Y+node.Bounds.H/2)
	tt.Frame()
}

func TestSettingsDialogLayoutAndUnsavedEdits(t *testing.T) {
	for _, width := range []int{420, 1100} {
		a := &app{settings: settingsState{open: true, repo: "/work/repo", global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))}}}
		tt := ui.NewTester(a.view, width, 720)
		defer retireTester(tt)
		tt.Frame()
		for _, label := range []string{"通用", "Git", "当前项目", "启动时恢复上次打开的仓库"} {
			if !tt.HasText(label) {
				t.Fatalf("missing %s", label)
			}
		}
		if err := tt.Click("启动时恢复上次打开的仓库"); err != nil {
			t.Fatal(err)
		}
		if !a.restoreDisabled {
			t.Fatal("restore preference not updated")
		}
		saveLobePreview(t, tt, "settings-general-"+strconv.Itoa(width))
		if err := tt.Click("Git"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		a.settings.global.fields[0].enabled = true
		a.settings.global.fields[0].value = "Draft"
		tt.Frame()
		saveLobePreview(t, tt, "settings-git-"+strconv.Itoa(width))
		for _, label := range []string{"应用", "放弃修改并关闭", "完成"} {
			node, ok := tt.AccessByRole(ui.RoleButton, label)
			if !ok || node.Bounds.X < 0 || node.Bounds.X+node.Bounds.W > float32(width) {
				t.Fatalf("clipped action %s: %+v", label, node.Bounds)
			}
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if !a.settings.open {
			t.Fatal("Escape discarded unsaved Git changes")
		}
		if err := tt.Click("放弃修改并关闭"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if a.settings.open {
			t.Fatal("explicit discard did not close settings")
		}
	}
}

func TestSettingsRowsToggleFromTheirLabels(t *testing.T) {
	a := &app{settings: settingsState{open: true}}
	tt := ui.NewTester(a.view, 900, 720)
	defer retireTester(tt)
	tt.Frame()
	if err := tt.Click("差异默认忽略空白"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.diffIgnoreWhitespace {
		t.Fatal("clicking the row did not turn the preference on")
	}
	if err := tt.Click("差异默认忽略空白"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.diffIgnoreWhitespace {
		t.Fatal("clicking the row did not turn the preference off")
	}
	if err := tt.Click("底部详情面板全宽"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.bottomFullWidth {
		t.Fatal("bottom panel preference not updated")
	}
}

func TestSettingsGitRowSwitchWritesAndRemovesOverride(t *testing.T) {
	a := &app{settings: settingsState{open: true, page: 1, global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))}}}
	tt := ui.NewTester(a.view, 900, 760)
	defer retireTester(tt)
	tt.Frame()
	if err := tt.Click("提交邮箱"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.global.fields[1].enabled {
		t.Fatal("row did not enable the override")
	}
	if !gitSettingsDirty(&a.settings.global) {
		t.Fatal("an empty override is not a pending change")
	}
	if apply, ok := tt.Access("应用"); !ok || !apply.Disabled {
		t.Fatalf("an empty override must not be appliable: %q", tt.Texts())
	}
	if err := tt.Click("提交邮箱"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.global.fields[1].enabled || gitSettingsDirty(&a.settings.global) {
		t.Fatal("row did not remove the override")
	}
}

func TestGitSettingsChoicesStayDisabledDuringSave(t *testing.T) {
	s := gitSettingsScope{busy: true, loaded: true, fields: make([]gitSetting, len(settingsGitKeys))}
	s.fields[2] = gitSetting{enabled: true, value: "false"}
	a := &app{}
	tt := ui.NewTester(func(c *ui.Context) { a.gitSettingRow(c, &s, 2, false) }, 720, 300)
	defer retireTester(tt)
	tt.Frame()
	choice, ok := tt.AccessByRole(ui.RoleMenuButton, "拉取方式")
	if !ok || !choice.Disabled {
		t.Fatal("pull strategy is editable during save")
	}
	tt.ClickAt(choice.Bounds.X+choice.Bounds.W/2, choice.Bounds.Y+choice.Bounds.H/2)
	tt.Frame()
	if len(tt.Menu()) != 0 || s.fields[2].value != "false" {
		t.Fatal("disabled choice opened its menu or changed the pending save")
	}
	s.busy = false
	tt.Frame()
	clickControl(t, tt, "拉取方式")
	if err := tt.ChooseMenuItem("变基"); err != nil {
		t.Fatal(err)
	}
	if s.fields[2].value != "true" {
		t.Fatal("choice stayed disabled after save completed")
	}
}

func TestSettingsProjectPageOwnsItsOverride(t *testing.T) {
	a := &app{settings: settingsState{open: true, page: 2, repo: "/work/repo",
		global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))},
		local:  gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))},
	}}
	tt := ui.NewTester(a.view, 1000, 800)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("/work/repo") {
		t.Fatal("missing project path")
	}
	clickControl(t, tt, "检查频率")
	if err := tt.ChooseMenuItem("每 5 分钟"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.repoAutoFetch["/work/repo"] != "5" {
		t.Fatalf("project override = %q", a.repoAutoFetch["/work/repo"])
	}
}

func TestSettingsRestoreDisabledKeepsStartupEmpty(t *testing.T) {
	repo, _ := initTestRepo(t)
	old := userConfigDir
	config := t.TempDir()
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()
	p, err := configFile("session.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(p, savedSession{Preferences: &appPreferences{RestoreDisabled: true}, Repos: []savedRepo{{Path: repo}}, Found: []string{repo}, RingSkipNotice: true}); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	updates := make(chan func(), 64)
	a.dispatch = func(fn func()) { updates <- fn }
	a.restoreSession()
	waitUpdates(t, updates, func() bool { return a.sessionReady })
	if len(a.tabs) != 0 || len(a.found) != 0 {
		t.Fatal("disabled startup restored repositories")
	}
	if !a.ringSkipNotice {
		t.Fatal("startup skipped non-workspace preferences")
	}
}

func TestSettingsGitApplyRetainsFailedDraft(t *testing.T) {
	isolateSettingsGit(t)
	ctx := context.Background()
	fields, err := readGitSettings(ctx, "", "--global")
	if err != nil {
		t.Fatal(err)
	}
	fields[1].enabled, fields[1].value = true, "bad\nemail"
	updates := make(chan func(), 64)
	a := &app{dispatch: func(fn func()) { updates <- fn }, settings: settingsState{open: true, global: gitSettingsScope{loaded: true, fields: fields}}}
	a.loadSettingsGit(false, true)
	waitUpdates(t, updates, func() bool { return !a.settings.global.busy })
	if a.settings.global.err == "" || a.settings.global.fields[1].value != "bad\nemail" {
		t.Fatal("failed save lost draft or error")
	}
	a.settings.global.fields[1].value = "fixed@example.com"
	a.loadSettingsGit(false, true)
	waitUpdates(t, updates, func() bool { return !a.settings.global.busy })
	if a.settings.global.err != "" {
		t.Fatal(a.settings.global.err)
	}
	if a.settings.global.fields[1].effective != "fixed@example.com" {
		t.Fatal("retry did not save correction")
	}
}

func TestGlobalSettingsIncludesSystemAndExcludesProject(t *testing.T) {
	isolateSettingsGit(t)
	system := filepath.Join(t.TempDir(), "systemconfig")
	if err := os.WriteFile(system, []byte("[user]\n name = System Identity\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_SYSTEM", system)
	repo, _ := initTestRepo(t)
	fields, err := readGitSettings(context.Background(), repo, "--global")
	if err != nil {
		t.Fatal(err)
	}
	if fields[0].enabled || fields[0].effective != "System Identity" || fields[0].scope != "system" {
		t.Fatalf("wrong global inheritance: %+v", fields[0])
	}
}

func TestSettingsGlobalSaveRefreshesProjectWithoutLosingDraft(t *testing.T) {
	isolateSettingsGit(t)
	repo, _ := initTestRepo(t)
	ctx := context.Background()
	if _, err := configCommand(ctx, repo, "--local", "--unset-all", "user.email"); err != nil {
		t.Fatal(err)
	}
	if _, err := configCommand(ctx, repo, "--global", "user.email", "old@example.com"); err != nil {
		t.Fatal(err)
	}
	global, err := readGitSettings(ctx, repo, "--global")
	if err != nil {
		t.Fatal(err)
	}
	project, err := readGitSettings(ctx, repo, "--local")
	if err != nil {
		t.Fatal(err)
	}
	global[1].value = "new@example.com"
	project[0].enabled, project[0].value = true, "Unsaved Project Name"
	updates := make(chan func(), 64)
	a := &app{dispatch: func(fn func()) { updates <- fn }, settings: settingsState{open: true, repo: repo,
		global: gitSettingsScope{loaded: true, fields: global},
		local:  gitSettingsScope{loaded: true, fields: project},
	}}
	a.loadSettingsGit(false, true)
	waitUpdates(t, updates, func() bool { return !a.settings.global.busy })
	if a.settings.global.err != "" || a.settings.local.err != "" {
		t.Fatalf("global=%s local=%s", a.settings.global.err, a.settings.local.err)
	}
	if a.settings.local.fields[1].effective != "new@example.com" {
		t.Fatal("project inherited value stayed stale after global save")
	}
	if a.settings.local.fields[0].value != "Unsaved Project Name" || !gitSettingsDirty(&a.settings.local) {
		t.Fatal("global save discarded project draft")
	}
}

func TestGitSettingsRejectsCheckoutShorthandAsDefaultBranch(t *testing.T) {
	isolateSettingsGit(t)
	repo, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	git("checkout", "-b", "feature")
	fields, err := readGitSettings(context.Background(), repo, "--local")
	if err != nil {
		t.Fatal(err)
	}
	fields[5].enabled, fields[5].value = true, "@{-1}"
	if err := saveGitSettings(context.Background(), repo, "--local", fields); err == nil {
		t.Fatal("checkout shorthand accepted as a persistent default branch name")
	}
	if _, err := configCommand(context.Background(), repo, "--local", "--get", "init.defaultBranch"); !configExit(err, 1) {
		t.Fatal("invalid default branch was written")
	}
}
