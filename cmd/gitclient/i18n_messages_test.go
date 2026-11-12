package main

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/egoist/mygo/ui"
)

func TestStoredMessagesFollowLanguageAndPreserveUserValues(t *testing.T) {
	old := englishUI.Load()
	t.Cleanup(func() { englishUI.Store(old) })
	cases := []struct{ source, want string }{
		{"已切换到分支 中文分支", "Switched to branch 中文分支"},
		{"已切换到分支 文件", "Switched to branch 文件"},
		{"提交失败: 人工提交不能填写 AI 模型", "Commit failed: Human commits cannot specify an AI model"},
		{"默认分支名无效：未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径", "Invalid default branch name: Git was not found. Install Git, or set the Git executable path in settings."},
		{"无法解析哈希 \"中文输入\"", "Unable to parse hash \"中文输入\""},
		{"提交姓名 不能为空或包含换行", "Commit author name cannot be empty or contain line breaks"},
		{"合并完成", "Merge completed"},
		{"文件过大（123 字节），未载入", "File too large (123 bytes); not loaded"},
	}
	for _, tc := range cases {
		englishUI.Store(true)
		got := trMessage(tc.source)
		if got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.source, got, tc.want)
		}
		englishUI.Store(false)
		if got := trMessage(tc.source); got != tc.source {
			t.Errorf("original %q -> %q, want %q", tc.source, got, tc.source)
		}
	}
}

func TestEnglishCatalogAndFormattedMessagesContainNoChinese(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	for source, translated := range englishText {
		for _, r := range translated {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("Chinese translation: %q -> %q", source, translated)
			}
		}
		spans := formatPlaceholder.FindAllStringIndex(source, -1)
		if len(spans) == 0 {
			continue
		}
		args := []any{}
		for _, span := range spans {
			switch source[span[1]-1] {
			case 'd':
				args = append(args, 3)
			case 'w', 'v':
				args = append(args, fmt.Errorf("请输入分支名"))
			default:
				args = append(args, "example")
			}
		}
		sample := fmt.Errorf(source, args...).Error()
		text := trMessage(sample)
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("untranslated formatted message: %q -> %q", sample, text)
				break
			}
		}
	}
}

func TestEnglishDialogsAndStoredErrorsContainNoChinese(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	cases := []struct {
		name  string
		setup func(*app)
		view  func(*app, *ui.Context)
	}{
		{"settings", func(a *app) { a.settings = settingsState{open: true} }, (*app).settingsModal},
		{"git-settings", func(a *app) {
			a.settings = settingsState{open: true, page: 1, global: gitSettingsScope{loaded: true, fields: make([]gitSetting, len(settingsGitKeys)), err: "提交姓名 不能为空或包含换行"}}
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
