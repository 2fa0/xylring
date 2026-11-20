package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

type editorPreset struct {
	id      string
	label   string
	macApps []string
	bins    []string
}

var externalEditorPresets = []editorPreset{
	{id: "vscode", label: "Visual Studio Code", macApps: []string{"Visual Studio Code", "Visual Studio Code - Insiders"}, bins: []string{"code"}},
	{id: "cursor", label: "Cursor", macApps: []string{"Cursor"}, bins: []string{"cursor"}},
	{id: "zed", label: "Zed", macApps: []string{"Zed"}, bins: []string{"zed"}},
	{id: "windsurf", label: "Windsurf", macApps: []string{"Windsurf"}, bins: []string{"windsurf"}},
	{id: "trae", label: "Trae", macApps: []string{"Trae"}, bins: []string{"trae"}},
	{id: "antigravity", label: "Antigravity", macApps: []string{"Antigravity"}, bins: []string{"agy"}},
	{id: "sublime", label: "Sublime Text", macApps: []string{"Sublime Text"}, bins: []string{"subl"}},
	{id: "goland", label: "GoLand", macApps: []string{"GoLand"}, bins: []string{"goland"}},
	{id: "idea", label: "IntelliJ IDEA", macApps: []string{"IntelliJ IDEA", "IntelliJ IDEA CE", "IntelliJ IDEA Ultimate"}, bins: []string{"idea"}},
}

const customEditorChoice = "__custom__"

func findEditorPreset(id string) (editorPreset, bool) {
	for _, p := range externalEditorPresets {
		if p.id == id {
			return p, true
		}
	}
	return editorPreset{}, false
}

func isCustomEditorValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	_, ok := findEditorPreset(value)
	return !ok
}

func editorDisplayName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return tr("系统默认")
	}
	if p, ok := findEditorPreset(value); ok {
		return p.label
	}
	base := filepath.Base(value)
	if base != "" && base != "." && base != string(filepath.Separator) {
		return strings.TrimSuffix(base, ".app")
	}
	return value
}

func editorMenuOptions() []configOption {
	opts := make([]configOption, 0, len(externalEditorPresets)+2)
	opts = append(opts, configOption{"", "系统默认"})
	for _, p := range externalEditorPresets {
		opts = append(opts, configOption{p.id, p.label})
	}
	opts = append(opts, configOption{customEditorChoice, "自定义…"})
	return opts
}

func resolveEditorCommand(
	goos, home, editor, targetPath string,
	statFn func(string) (os.FileInfo, error),
	lookPathFn func(string) (string, error),
) (string, []string, error) {
	return resolveEditorCommandWithEnv(goos, home, editor, targetPath, false, statFn, lookPathFn, nil)
}

func resolveEditorCommandWithEnv(
	goos, home, editor, targetPath string,
	inWSL bool,
	statFn func(string) (os.FileInfo, error),
	lookPathFn func(string) (string, error),
	wslPathFn func(string) (string, error),
) (string, []string, error) {
	editor = strings.TrimSpace(editor)
	if editor == "" {
		bin, args := resolveDefaultOpenCommand(goos, targetPath, inWSL, lookPathFn, wslPathFn)
		return bin, args, nil
	}
	if p, ok := findEditorPreset(editor); ok {
		if goos == "darwin" {
			roots := []string{"/Applications"}
			if home != "" {
				roots = append(roots, filepath.Join(home, "Applications"))
			}
			for _, appName := range p.macApps {
				for _, root := range roots {
					appPath := filepath.Join(root, appName+".app")
					if _, err := statFn(appPath); err == nil {
						return "open", []string{"-a", appPath, targetPath}, nil
					}
				}
			}
		}
		for _, bin := range p.bins {
			if binPath, err := lookPathFn(bin); err == nil {
				return binPath, []string{targetPath}, nil
			}
		}
		return "", nil, fmt.Errorf("未找到 %s，请在设置中检查默认编辑器", p.label)
	}
	if goos == "darwin" && strings.HasSuffix(strings.ToLower(editor), ".app") {
		if _, err := statFn(editor); err == nil {
			return "open", []string{"-a", editor, targetPath}, nil
		}
		return "", nil, fmt.Errorf("未找到编辑器应用：%s", editor)
	}
	if strings.ContainsAny(editor, `/\`) {
		if info, err := statFn(editor); err == nil && !info.IsDir() {
			return editor, []string{targetPath}, nil
		}
		return "", nil, fmt.Errorf("未找到编辑器：%s", editor)
	}
	if binPath, err := lookPathFn(editor); err == nil {
		return binPath, []string{targetPath}, nil
	}
	return "", nil, fmt.Errorf("未找到编辑器命令：%s", editor)
}

func openWithConfiguredEditor(targetPath, editor string) error {
	home, _ := os.UserHomeDir()
	bin, args, err := resolveEditorCommandWithEnv(runtime.GOOS, home, editor, targetPath, isWSL(), os.Stat, exec.LookPath, wslPathToWindows)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	hideConsoleWindow(cmd)
	return cmd.Start()
}

func openWithDefaultEditor(path string) error {
	return openWithConfiguredEditor(path, "")
}

func (a *app) openInEditor(targetPath string) error {
	if _, err := os.Stat(targetPath); err != nil {
		return err
	}
	return openWithConfiguredEditor(targetPath, a.externalEditor)
}

func (a *app) openEditorMenuLabel() string {
	if strings.TrimSpace(a.externalEditor) == "" {
		return tr("使用默认编辑器打开")
	}
	return fmt.Sprintf(tr("在 %s 中打开"), editorDisplayName(a.externalEditor))
}

func (a *app) applyCustomEditorPath(raw string) {
	trimmed := strings.TrimSpace(raw)
	a.editorPathDraft = trimmed
	a.externalEditor = trimmed
	if trimmed == "" {
		a.settings.customEditor = false
	} else {
		a.settings.customEditor = isCustomEditorValue(trimmed)
	}
}

func (a *app) pickEditorApp() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	go func() {
		paths, err := openEditorDialog(win)
		a.post(func() {
			a.picking = false
			if err != nil {
				a.setBanner(trimMsg(err.Error()), "")
				return
			}
			if len(paths) == 0 {
				return
			}
			a.applyCustomEditorPath(paths[0])
		})
	}()
}

func (a *app) editorSettings(c *ui.Context) {
	customActive := a.settings.customEditor || isCustomEditorValue(a.externalEditor)
	selected := a.externalEditor
	if customActive {
		selected = customEditorChoice
	}
	rows := []func(){
		func() {
			settingsChoiceRow(c, "settings-default-editor", "默认编辑器", "", selected, editorMenuOptions(), func(v string) {
				if v == customEditorChoice {
					a.settings.customEditor = true
					if isCustomEditorValue(a.externalEditor) {
						a.editorPathDraft = a.externalEditor
					}
					return
				}
				a.settings.customEditor = false
				a.externalEditor = v
				a.editorPathDraft = ""
			})
		},
	}
	if customActive {
		rows = append(rows, func() {
			ui.Column(c.Key("settings-editor-custom")).FillWidth().Children(func() {
				settingsRow(c, "settings-editor-custom-row", func() {
					settingsName(c, "自定义编辑器")
				}, func() {
					if ui.Button(c.Key("settings-editor-browse"), tr("选择…")).Disabled(a.picking).Clicked() {
						a.pickEditorApp()
					}
					if ui.Button(c.Key("settings-editor-apply"), tr("应用")).
						Disabled(strings.TrimSpace(a.editorPathDraft) == a.externalEditor).Clicked() {
						a.applyCustomEditorPath(a.editorPathDraft)
					}
				})
				ui.Row(c.Key("settings-editor-custom-input")).FillWidth().Padding(0, 16, 11, 16).Children(func() {
					input := ui.TextInput(c.Key("value"), &a.editorPathDraft).
						Label(tr("自定义编辑器")).
						Placeholder(tr("输入应用路径或命令，例如 /Applications/Zed.app 或 code")).
						FillWidth()
					if input.Submitted() {
						a.applyCustomEditorPath(a.editorPathDraft)
					}
				})
			})
		})
	}
	settingsSection(c, "settings-editor", "外部编辑器", rows...)
}
