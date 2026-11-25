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
	a.refsInline = true
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

// clickInDialogBody 点弹窗正文里的东西。正文是滚动区，能露出多少看平台字体：
// 窄窗口里靠后的设置项可能要先往下滚，点完又得往回滚才能再点上面的分页标签。
func clickInDialogBody(t *testing.T, tt *ui.Tester, width, height int, label string) {
	t.Helper()
	try := func() bool {
		tt.Frame()
		return tt.Click(label) == nil
	}
	if try() {
		return
	}
	x, y := float32(width)/2, float32(height)/2
	for _, dy := range []float32{120, -120} {
		for i := 0; i < 8; i++ {
			tt.Scroll(x, y, 0, dy)
			if try() {
				return
			}
		}
	}
	t.Fatalf("滚遍弹窗正文也点不到 %q", label)
}

func TestSettingsDialogLayoutAndUnsavedEdits(t *testing.T) {
	for _, width := range []int{420, 1100} {
		a := &app{settings: settingsState{open: true, repo: "/work/repo", global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))}}}
		tt := ui.NewTester(a.view, width, 720)
		defer retireTester(tt)
		tt.Frame()
		for _, label := range []string{"通用", "差异与视图", "Git", "当前项目", "数据与关于", "启动时恢复上次打开的仓库"} {
			if !tt.HasText(label) {
				t.Fatalf("missing %s", label)
			}
		}
		clickInDialogBody(t, tt, width, 720, "启动时恢复上次打开的仓库")
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
	if err := tt.Click("底部详情面板全宽"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.bottomFullWidth {
		t.Fatal("bottom panel preference not updated")
	}
	if err := tt.Click("差异与视图"); err != nil {
		t.Fatal(err)
	}
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
}

func TestSettingsGitTextRowInlineInputAndClear(t *testing.T) {
	a := &app{settings: settingsState{open: true, page: settingsPageGit, global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))}}}
	tt := ui.NewTester(a.view, 900, 760)
	defer retireTester(tt)
	tt.Frame()
	field, ok := tt.AccessByRole(ui.RoleTextField, "提交邮箱")
	if !ok {
		t.Fatalf("missing inline text field for 提交邮箱: %q", tt.Texts())
	}
	tt.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tt.Frame()
	tt.Type("   ")
	tt.Frame()
	if !a.settings.global.fields[1].enabled || !gitSettingsDirty(&a.settings.global) {
		t.Fatalf("whitespace edit should mark pending invalid override: %+v", a.settings.global.fields[1])
	}
	if apply, ok := tt.Access("应用"); !ok || !apply.Disabled {
		t.Fatalf("a whitespace-only override must not be appliable: %q", tt.Texts())
	}
	clickControl(t, tt, "清除")
	if a.settings.global.fields[1].enabled || a.settings.global.fields[1].value != "" || gitSettingsDirty(&a.settings.global) {
		t.Fatalf("clear button did not remove the override: %+v", a.settings.global.fields[1])
	}
	tt.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tt.Frame()
	tt.Type("alice@example.com")
	tt.Frame()
	if !a.settings.global.fields[1].enabled || a.settings.global.fields[1].value != "alice@example.com" || !gitSettingsDirty(&a.settings.global) {
		t.Fatalf("typing did not set inline override: %+v", a.settings.global.fields[1])
	}
	if apply, ok := tt.Access("应用"); !ok || apply.Disabled {
		t.Fatalf("valid override should enable Apply: %q", tt.Texts())
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

// 取值固定的 Git 键在项目页上只有菜单一个控件：不覆盖时菜单写“继承全局设置”，
// 生效值写成人能读的取值（变基），而不是 git 的原始值 true。
func TestGitSettingsProjectChoiceInheritsAndOverrides(t *testing.T) {
	s := gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))}
	s.fields[2] = gitSetting{effective: "true", origin: "/home/x/.gitconfig", scope: "global"}
	a := &app{}
	tt := ui.NewTester(func(c *ui.Context) { a.gitSettingRow(c, &s, 2, true) }, 720, 300)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("变基") || !tt.HasText("继承全局设置") {
		t.Fatalf("inherited value should read as its option label: %q", tt.Texts())
	}
	for _, text := range tt.Texts() {
		if text == "true" {
			t.Fatalf("raw git value shown: %q", tt.Texts())
		}
	}

	clickControl(t, tt, "拉取方式")
	if err := tt.ChooseMenuItem("合并"); err != nil {
		t.Fatal(err)
	}
	if !s.fields[2].enabled || s.fields[2].value != "false" {
		t.Fatalf("choosing a value did not override: %+v", s.fields[2])
	}

	clickControl(t, tt, "拉取方式")
	if err := tt.ChooseMenuItem("继承全局设置"); err != nil {
		t.Fatal(err)
	}
	if s.fields[2].enabled || s.fields[2].value != "" {
		t.Fatalf("choosing inheritance did not drop the override: %+v", s.fields[2])
	}
}

// 全局页没有可继承的范围：不覆盖那一项就是“未设置”，选它把这个键删掉。
func TestGitSettingsGlobalChoiceUnsets(t *testing.T) {
	s := gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))}
	s.fields[3] = gitSetting{enabled: true, value: "only", original: []string{"only"}}
	a := &app{}
	tt := ui.NewTester(func(c *ui.Context) { a.gitSettingRow(c, &s, 3, false) }, 720, 300)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("仅快进") {
		t.Fatalf("chosen value should show its label: %q", tt.Texts())
	}
	clickControl(t, tt, "快进策略")
	if err := tt.ChooseMenuItem("未设置"); err != nil {
		t.Fatal(err)
	}
	if s.fields[3].enabled || s.fields[3].value != "" || !gitSettingsDirty(&s) {
		t.Fatalf("unsetting did not clear the key: %+v", s.fields[3])
	}
}

func TestSettingsProjectPageOwnsItsOverride(t *testing.T) {
	a := &app{settings: settingsState{open: true, page: settingsPageProject, repo: "/work/repo",
		global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))},
		local:  gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))},
	}}
	tt := ui.NewTester(a.view, 1000, 800)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("/work/repo") {
		t.Fatal("missing project path")
	}
	clickControl(t, tt, "自动获取")
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

// 切页签时对话框高度不变：读取中的 Git 页只剩一行提示，若让面板跟着内容收缩，
// 内容回来时就会闪一下。
func TestSettingsDialogKeepsHeightAcrossPages(t *testing.T) {
	a := &app{settings: settingsState{open: true, repo: "/work/repo", global: gitSettingsScope{busy: true}, local: gitSettingsScope{busy: true}}}
	tt := ui.NewTester(a.view, 1100, 720)
	defer retireTester(tt)
	tt.Frame()
	height := func() float32 {
		d, ok := tt.AccessByRole(ui.RoleDialog, "设置")
		if !ok {
			t.Fatal("missing settings dialog")
		}
		return d.Bounds.H
	}
	want := height()
	for _, label := range []string{"差异与视图", "Git", "当前项目", "数据与关于", "通用", "Git"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if got := height(); got != want {
			t.Fatalf("%s changed dialog height: got %v want %v", label, got, want)
		}
	}
	a.settings.global = gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))}
	tt.Frame()
	if got := height(); got != want {
		t.Fatalf("loaded page changed dialog height: got %v want %v", got, want)
	}
	if !tt.HasText("提交姓名") || !tt.HasText("Git 程序") {
		t.Fatalf("loaded Git page did not render config and binary sections: %q", tt.Texts())
	}
}

func TestSettingsDataAboutPageGroupsBoardUpdateAndAbout(t *testing.T) {
	a := &app{settings: settingsState{open: true, page: settingsPageDataAbout}}
	tt := ui.NewTester(a.view, 1000, 760)
	defer retireTester(tt)
	tt.Frame()
	for _, want := range []string{"看板", "数据目录", "备份与导入", "更新", "关于", "项目主页"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q on Data & About page: %q", want, tt.Texts())
		}
	}
}

func TestSettingsRefsPositionSwitchesCommitColumns(t *testing.T) {
	h := hashN(1)
	tab := newTab(1, "/work/repo")
	tab.loaded = true
	tab.head = h
	tab.commits = []commit{{hash: h, subject: "feat: demo"}}
	tab.refs = map[[20]byte][]refInfo{h: {{kind: refBranch, name: "main"}}}
	a := &app{
		tabs:     []*repoTab{tab},
		active:   0,
		settings: settingsState{open: true, page: settingsPageDiff},
		dispatch: func(func()) {},
	}
	tt := ui.NewTester(a.view, 1100, 760)
	defer retireTester(tt)
	tt.Frame()

	// 默认图谱左侧独立列
	if a.refsInline {
		t.Fatal("refsInline should default to false")
	}
	if !tt.HasText("引用") || tt.HasText("描述 / 引用") {
		t.Fatalf("expected separate refs column by default: %q", tt.Texts())
	}

	clickControl(t, tt, "引用位置")
	if err := tt.ChooseMenuItem("描述行内"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.refsInline {
		t.Fatal("choosing 描述行内 did not enable refsInline")
	}
	if !tt.HasText("描述 / 引用") {
		t.Fatalf("expected inline refs column header 描述 / 引用: %q", tt.Texts())
	}
	if !tt.HasText("HEAD") || !tt.HasText("main") || !tt.HasText("feat: demo") {
		t.Fatalf("inline refs mode should keep badges and subject visible: %q", tt.Texts())
	}
}

func TestInlineRefsKeepSubjectVisibleInsideSubjectColumn(t *testing.T) {
	h := hashN(1)
	tab := newTab(1, "/work/repo")
	tab.loaded = true
	tab.head = h
	tab.commits = []commit{{hash: h, subject: "feat: keep subject visible with many long refs", author: "Ada", when: 1700000000}}
	tab.refs = map[[20]byte][]refInfo{
		h: {
			{kind: refBranch, name: "feature/very-long-branch-name-alpha"},
			{kind: refBranch, name: "feature/very-long-branch-name-beta"},
			{kind: refTag, name: "v0.11.0-rc.1-release-candidate"},
		},
	}
	a := &app{
		tabs:       []*repoTab{tab},
		active:     0,
		refsInline: true,
		repoSplit:  300,
		dispatch:   func(func()) {},
	}
	tt := ui.NewTester(a.view, 920, 600)
	defer retireTester(tt)
	tt.Frame()

	subjRect, ok := tt.Find("feat: keep subject visible with many long refs")
	if !ok {
		t.Fatalf("subject text not found: %q", tt.Texts())
	}
	dateHeader, ok := tt.Find("日期")
	if !ok {
		t.Fatalf("date column header not found: %q", tt.Texts())
	}
	// The subject must start well before the date column so it isn't clipped away by inline badges.
	visibleW := dateHeader.X - subjRect.X
	if visibleW < 80 {
		t.Fatalf("subject clipped by inline refs: subjRect=%+v dateHeader=%+v visibleW=%.1f", subjRect, dateHeader, visibleW)
	}
}

func TestSettingsNavSupportsArrowKeys(t *testing.T) {
	a := &app{settings: settingsState{open: true, page: settingsPageGeneral}}
	tt := ui.NewTester(a.view, 1000, 760)
	defer retireTester(tt)
	tt.Frame()

	if err := tt.Click("通用"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.settings.page != settingsPageDiff {
		t.Fatalf("expected KeyDown to move to settingsPageDiff, got %d", a.settings.page)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if a.settings.page != settingsPageDataAbout {
		t.Fatalf("expected KeyEnd to move to settingsPageDataAbout, got %d", a.settings.page)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if a.settings.page != settingsPageProject {
		t.Fatalf("expected KeyUp to move to settingsPageProject, got %d", a.settings.page)
	}
}

func TestGitSettingsClearDoesNotPreviewStaleAndShowsHigherWorktreeScope(t *testing.T) {
	fields := make([]gitSetting, len(settingsGitKeys))
	// user.name: local override cleared before save, no global/system inherited value known.
	fields[0] = gitSetting{
		enabled:   false,
		value:     "",
		original:  []string{"Stale Local Name"},
		effective: "Stale Local Name",
		scope:     "local",
		origin:    "file:.git/config",
	}
	// user.email: local value edited, but higher-precedence worktree config overrides it.
	fields[1] = gitSetting{
		enabled:   true,
		value:     "local@example.com",
		original:  []string{"local@example.com"},
		effective: "wt@example.com",
		scope:     "worktree",
		origin:    "file:.git/worktrees/wt/config.worktree",
	}
	a := &app{
		settings: settingsState{
			open:  true,
			page:  settingsPageProject,
			repo:  "/work/repo",
			local: gitSettingsScope{loaded: true, fields: fields},
		},
	}
	tt := ui.NewTester(a.view, 1000, 760)
	defer retireTester(tt)
	tt.Frame()

	if tt.HasText("Stale Local Name") {
		t.Fatalf("cleared project override must not preview stale local value: %q", tt.Texts())
	}
	if !tt.HasText("工作树") || !tt.HasText("wt@example.com") {
		t.Fatalf("higher-precedence worktree effective value must remain visible: %q", tt.Texts())
	}
}

func TestSettingsSidebarContentTopAlignedAndClipped(t *testing.T) {
	a := &app{settings: settingsState{
		open:   true,
		page:   settingsPageGeneral,
		repo:   "/work/repo",
		global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))},
	}}
	tt := ui.NewTester(a.view, 1100, 760)
	defer retireTester(tt)
	tt.Frame()

	navBtn, ok1 := tt.Find("通用")
	sectionTitle, ok2 := tt.Find("界面")
	if !ok1 || !ok2 {
		t.Fatalf("missing nav or section: ok1=%v ok2=%v", ok1, ok2)
	}
	// 右侧首张卡片应紧贴内容区顶部，和左侧首项基本齐平，不应被垂直居中推到中间。
	if diff := sectionTitle.Y - navBtn.Y; diff > 40 || diff < -20 {
		t.Fatalf("right content not top-aligned: nav.Y=%v section.Y=%v", navBtn.Y, sectionTitle.Y)
	}

	// 切到内容更长的 Git 页，顶部卡片不能盖到关闭按钮或标题栏上。
	if err := tt.Click("Git"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	closeBtn, okClose := tt.AccessByRole(ui.RoleButton, "关闭对话框")
	gitTitle, okGit := tt.Find("提交身份")
	if !okClose || !okGit {
		t.Fatalf("missing close or git section: okClose=%v okGit=%v", okClose, okGit)
	}
	if gitTitle.Y < closeBtn.Bounds.Y+closeBtn.Bounds.H {
		t.Fatalf("git section overlapped dialog header: close=%+v git=%+v", closeBtn.Bounds, gitTitle)
	}
}

func TestGitSettingsEmptyUnchangedFieldDoesNotBlockOtherEdits(t *testing.T) {
	fields := make([]gitSetting, len(settingsGitKeys))
	// Existing empty user.name (unchanged).
	fields[0] = gitSetting{enabled: true, value: "", original: []string{""}}
	// User modifies fetch.prune (index 4).
	fields[4] = gitSetting{enabled: true, value: "true", original: nil}

	s := &gitSettingsScope{loaded: true, fields: fields}
	if !gitSettingsDirty(s) {
		t.Fatal("expected fetch.prune edit to mark scope dirty")
	}
	if gitSettingsHasEmptyOverride(s) {
		t.Fatal("existing unchanged empty user.name must not block applying other changed settings")
	}
	// If the user explicitly edits user.name to whitespace, it MUST block Apply.
	fields[0].value = "   "
	if !gitSettingsHasEmptyOverride(s) {
		t.Fatal("modified empty/whitespace field must block Apply")
	}
}

func TestSettingsGitRepoDirIsolatesWSLAndDockerForGlobalScope(t *testing.T) {
	wslPath := `\\wsl$\Ubuntu\home\user\repo`
	if got := settingsGitRepoDir(wslPath, false); got != "" {
		t.Fatalf("global settings with WSL repo must target host, got %q", got)
	}
	if got := settingsGitRepoDir(wslPath, true); got != wslPath {
		t.Fatalf("local settings with WSL repo must target WSL repo, got %q", got)
	}
	dockerPath := dockerRepoKey(repoTarget{
		context:   "default",
		endpoint:  "unix:///var/run/docker.sock",
		container: strings.Repeat("a", 64),
		path:      "/workspace/repo",
	})
	if got := settingsGitRepoDir(dockerPath, false); got != "" {
		t.Fatalf("global settings with Docker repo must target host, got %q", got)
	}
}

func TestSettingsNavAccessibilityAndFixedConfirmCloseBanner(t *testing.T) {
	fields := make([]gitSetting, len(settingsGitKeys))
	fields[0] = gitSetting{enabled: true, value: "Modified Name", original: nil}
	a := &app{settings: settingsState{
		open:         true,
		page:         settingsPageGit,
		confirmClose: true,
		repo:         "/work/repo",
		global:       gitSettingsScope{loaded: true, fields: fields},
	}}
	tt := ui.NewTester(a.view, 900, 620)
	defer retireTester(tt)
	tt.Frame()

	// Nav tabs expose RoleTab and selected state.
	gitTab, ok := tt.AccessByRole(ui.RoleTab, "Git")
	if !ok || !gitTab.Selected {
		t.Fatalf("expected Git nav item to be a selected RoleTab, got ok=%v node=%+v", ok, gitTab)
	}
	generalTab, ok := tt.AccessByRole(ui.RoleTab, "通用")
	if !ok || generalTab.Selected {
		t.Fatalf("expected 通用 nav item to be unselected RoleTab, got ok=%v node=%+v", ok, generalTab)
	}

	// Scroll to bottom of Git page; confirmClose banner must stay fixed and visible.
	for i := 0; i < 10; i++ {
		tt.Scroll(550, 350, 0, 120)
		tt.Frame()
	}
	bannerRect, okBanner := tt.Find("有未应用的 Git 修改。")
	if !okBanner || bannerRect.Y < 0 || bannerRect.Y > 300 {
		t.Fatalf("confirmClose banner scrolled off screen: ok=%v rect=%+v", okBanner, bannerRect)
	}
}

func TestSettingsUnsavedPathDraftsBlockDoneAndClearConfirmOnApply(t *testing.T) {
	// Unsaved Git path draft blocks Done and triggers confirmClose instead of silently discarding.
	a := &app{
		gitPathDraft: "/custom/git",
		settings: settingsState{
			open:   true,
			page:   settingsPageGit,
			global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))},
		},
	}
	if !a.gitPathDraftDirty() || !a.settingsHasUnsavedEdits() {
		t.Fatal("expected unsaved gitPathDraft to be marked dirty")
	}
	tt := ui.NewTester(a.view, 900, 720)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("放弃修改并关闭") {
		t.Fatalf("expected discard button for unsaved gitPathDraft: %q", tt.Texts())
	}
	// Clicking "放弃修改并关闭" discards the draft and closes settings cleanly.
	if err := tt.Click("放弃修改并关闭"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.open || a.gitPathDraft != "" {
		t.Fatalf("expected discard to close settings and reset gitPathDraft, open=%v draft=%q", a.settings.open, a.gitPathDraft)
	}

	// Unsaved custom editor path draft blocks Done and clears confirmClose once applied.
	b := &app{
		editorPathDraft: "/Applications/Nova.app",
		settings: settingsState{
			open:         true,
			page:         settingsPageGeneral,
			customEditor: true,
			confirmClose: true,
		},
	}
	if !b.editorPathDraftDirty() || !b.settingsHasUnsavedEdits() {
		t.Fatal("expected unsaved editorPathDraft to be marked dirty")
	}
	b.applyCustomEditorPath(b.editorPathDraft)
	if b.settings.confirmClose {
		t.Fatal("expected confirmClose warning to clear after applying custom editor path")
	}
}

func TestInlineRefsNarrowWindowCapsBadgeAndKeepsSubjectVisible(t *testing.T) {
	h := hashN(1)
	tab := newTab(1, "/work/repo")
	tab.loaded = true
	tab.head = h
	tab.commits = []commit{{hash: h, subject: "feat: keep this description readable"}}
	tab.refs = map[[20]byte][]refInfo{
		h: {
			{kind: refBranch, name: "feature/very-long-branch-name-that-should-not-squeeze-out-the-commit-subject"},
			{kind: refBranch, name: "release/v0.12.0-candidate"},
			{kind: refTag, name: "v0.12.0-rc.1"},
		},
	}
	a := &app{
		tabs:       []*repoTab{tab},
		active:     0,
		refsInline: true,
		dispatch:   func(func()) {},
	}
	tt := ui.NewTester(a.view, 860, 620)
	defer retireTester(tt)
	tt.Frame()

	subjRect, ok := tt.Find("feat: keep this description readable")
	if !ok {
		t.Fatalf("commit subject missing in narrow inline refs mode: %q", tt.Texts())
	}
	if subjRect.W < 80 {
		t.Fatalf("commit subject squeezed too narrow: rect=%+v", subjRect)
	}
	if dateHeader, ok := tt.Find("日期"); ok && dateHeader.X-subjRect.X < 80 {
		t.Fatalf("commit subject clipped before date column: subjRect=%+v dateHeader=%+v", subjRect, dateHeader)
	}
	if !tt.HasText("+3") && !tt.HasText("+2") && !tt.HasText("+1") {
		t.Fatalf("expected overflow refs collapsed into +N badge: %q", tt.Texts())
	}
}
