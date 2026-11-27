package i18n

import (
	"fmt"
	"testing"
	"unicode"
)

func TestStoredMessagesFollowLanguageAndPreserveUserValues(t *testing.T) {
	old := EnglishUI.Load()
	t.Cleanup(func() { EnglishUI.Store(old) })
	cases := []struct{ source, want string }{
		{"已切换到分支 中文分支", "Switched to branch 中文分支"},
		{"已切换到分支 文件", "Switched to branch 文件"},
		{"提交失败: 人工提交不能填写 AI 模型", "Commit failed: Human commits cannot specify an AI model"},
		{"默认分支名无效：未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径", "Invalid default branch name: Git was not found. Install Git, or set the Git executable path in settings."},
		{"无法解析哈希 \"中文输入\"", "Unable to parse hash \"中文输入\""},
		{"提交姓名 不能为空或包含换行", "Commit author name cannot be empty or contain line breaks"},
		{"合并完成", "Merge completed"},
		{"文件过大（123 字节），未载入", "File too large (123 bytes); not loaded"},
		{"找不到这个仓库：/tmp/gone", "Repository not found: /tmp/gone"},
		{"不是 Git 仓库：/tmp/plain", "Not a Git repository: /tmp/plain"},
	}
	for _, tc := range cases {
		EnglishUI.Store(true)
		got := TrMessage(tc.source)
		if got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.source, got, tc.want)
		}
		EnglishUI.Store(false)
		if got := TrMessage(tc.source); got != tc.source {
			t.Errorf("original %q -> %q, want %q", tc.source, got, tc.source)
		}
	}
}

func TestEnglishCatalogAndFormattedMessagesContainNoChinese(t *testing.T) {
	old := EnglishUI.Load()
	EnglishUI.Store(true)
	t.Cleanup(func() { EnglishUI.Store(old) })
	for source, translated := range EnglishText {
		for _, r := range translated {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("Chinese translation: %q -> %q", source, translated)
			}
		}
		spans := FormatPlaceholder.FindAllStringIndex(source, -1)
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
		text := TrMessage(sample)
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				t.Errorf("untranslated formatted message: %q -> %q", sample, text)
				break
			}
		}
	}
}
