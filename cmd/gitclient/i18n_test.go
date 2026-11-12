package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLanguageSettingsSwitchWithoutClosing(t *testing.T) {
	old := englishUI.Load()
	t.Cleanup(func() { englishUI.Store(old) })
	for _, width := range []int{420, 1100} {
		englishUI.Store(false)
		a := &app{settings: settingsState{open: true}}
		tt := ui.NewTester(a.view, width, 800)
		tt.Frame()
		if !tt.HasText("跟随系统") {
			t.Fatal("language must default to system")
		}
		if err := tt.Click("跟随系统"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		labels := []string{}
		for _, item := range tt.Menu() {
			labels = append(labels, item)
		}
		for _, label := range []string{"跟随系统", "简体中文", "English"} {
			if !slices.Contains(labels, label) {
				t.Fatalf("missing language choice %s", label)
			}
		}
		if err := tt.ChooseMenuItem("English"); err != nil {
			t.Fatal(err)
		}
		if a.language != "en" {
			t.Fatal("language preference not updated")
		}
		tt.Frame()
		for _, label := range []string{"Settings", "General", "Interface language", "English", "Done"} {
			if !tt.HasText(label) {
				t.Fatalf("missing English label %s", label)
			}
		}
		if !a.settings.open {
			t.Fatal("language switch closed settings")
		}
		saveLobePreview(t, tt, "settings-english-"+strconv.Itoa(width))
		if err := tt.Click("English"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("Simplified Chinese"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if a.language != "zh-Hans" {
			t.Fatal("Chinese choice was not saved")
		}
		if !tt.HasText("通用") || !tt.HasText("完成") {
			t.Fatal("cannot switch back to Chinese")
		}
		retireTester(tt)
	}
}

func TestTranslationCoverageAndFormats(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	verbs := regexp.MustCompile(`%\+?(?:\[\d+\])?\d*(?:\.\d+)?[a-zA-Z%]`)
	for source, translated := range englishText {
		if translated == "" {
			t.Fatalf("empty translation: %s", source)
		}
		if !slices.Equal(verbs.FindAllString(source, -1), verbs.FindAllString(translated, -1)) {
			t.Fatalf("format arguments differ: %q -> %q", source, translated)
		}
	}
	for _, file := range files {
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || name.Name != "tr" || len(call.Args) != 1 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			source, _ := strconv.Unquote(lit.Value)
			if _, ok := englishText[source]; !ok {
				t.Errorf("%s: missing translation for %q", file, source)
			}
			return true
		})
	}
}

// Only operations whose outcome is not obvious from their name carry a hint;
// the rest would restate git's own semantics to someone who already chose them.
func TestOperationHintsOnlyExplainConsequences(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	for _, action := range []string{"硬重置", "混合重置", "恢复历史文件", "删除 Stash", "中止操作", "继续操作"} {
		hint := operationHint(action)
		if hint == "" {
			t.Fatalf("missing English hint for action %s", action)
		}
		for _, r := range hint {
			if r >= '\u4e00' && r <= '\u9fff' {
				t.Fatalf("untranslated hint: %s", hint)
			}
		}
	}
	for _, action := range []string{"软重置", "推送分支", "设置身份"} {
		if hint := operationHint(action); hint != "" {
			t.Fatalf("%s should not explain git semantics: %q", action, hint)
		}
	}
}
