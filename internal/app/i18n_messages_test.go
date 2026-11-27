package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEnglishDialogsAndStoredErrorsContainNoChinese(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	cases := []struct {
		name  string
		setup func(*app)
		view  func(*app, *ui.Context)
	}{
		{"settings", func(a *app) { a.settings = settingsState{open: true, confirmClose: true} }, (*app).settingsModal},
		{"project-settings", func(a *app) {
			a.settings = settingsState{open: true, page: settingsPageProject, repo: "/work/repo", local: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys))}}
		}, (*app).settingsModal},
		{"git-settings", func(a *app) {
			a.settings = settingsState{open: true, page: settingsPageGit, global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys)), err: "提交姓名 不能为空或包含换行"}}
		}, (*app).settingsModal},
		{"write", func(a *app) {
			a.showWrite = true
			a.write.kind = writeBranch
			a.write.err = "请等待正在执行的操作"
		}, (*app).commitWriteModal},
		{"fetch", func(a *app) {
			a.showFetch = true
			a.fetch.remotes = []string{"origin"}
			a.fetch.err = "没有配置远程"
		}, (*app).fetchModal},
		{"compare", func(a *app) { a.showCompare = true; a.compareErr = "请输入分支、标签或哈希" }, (*app).comparisonModal},
		{"operation", func(a *app) {
			a.showOperation = true
			a.op.action = "硬重置"
			a.op.repo = "/work/repo"
			a.op.err = "请先确认再执行"
		}, (*app).operationModal},
		{"ring", func(a *app) {
			a.tabs[0].ring.open = true
			a.tabs[0].ring.status = "版本过旧"
			a.tabs[0].ring.err = "现有 Hook 不属于 Xylring，未修改"
		}, (*app).ringModal},
		{"discard", func(a *app) {
			a.showDiscard = true
			a.discard.file = fileChange{path: "example.go", extra: "未跟踪"}
		}, (*app).discardModal},
		{"close-guard", func(a *app) {
			a.startGitWrite("正在提交")
			a.quitGuard = closeGuardState{open: true}
		}, (*app).closeGuardModal},
		{"folder-import-init", func(a *app) {
			a.openFolderImportInit("/work/empty-dir")
		}, (*app).folderImportModal},
		{"folder-import-repos", func(a *app) {
			a.openFolderImportRepos("/work/projects", []string{"/work/projects/alpha", "/work/projects/beta"})
		}, (*app).folderImportModal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{tabs: []*repoTab{graphTabForWrite()}}
			tc.setup(a)
			tt := ui.NewTester(func(c *ui.Context) { tc.view(a, c) }, 900, 800)
			defer retireTester(tt)
			tt.SetLocale("en")
			tt.Frame()
			assertEnglishUITexts(t, tt)
			for _, text := range tt.Texts() {
				if strings.Contains(text, "%!s") {
					t.Errorf("invalid format %q", text)
				}
			}
		})
	}
}
