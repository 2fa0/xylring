package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

type editorLaunchPlan struct {
	bin              string
	args             []string
	windowsShellOpen bool
	isLauncher       bool
}

func isWindowsBatchFile(p string) bool {
	lower := strings.ToLower(strings.TrimSpace(p))
	return strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")
}

func makeEditorLaunchPlan(goos, bin string, args []string) editorLaunchPlan {
	if goos == "windows" && isWindowsBatchFile(bin) {
		// Using "call" avoids cmd.exe /c stripping outer quotes when both the script path and an argument are quoted.
		cmdArgs := make([]string, 0, len(args)+4)
		cmdArgs = append(cmdArgs, "/d", "/c", "call", bin)
		cmdArgs = append(cmdArgs, args...)
		return editorLaunchPlan{bin: "cmd.exe", args: cmdArgs, isLauncher: true}
	}
	return editorLaunchPlan{bin: bin, args: args}
}

func resolveEditorLaunch(
	goos, home, editor, targetPath string,
	statFn func(string) (os.FileInfo, error),
	lookPathFn func(string) (string, error),
) (editorLaunchPlan, error) {
	editor = strings.TrimSpace(editor)
	if editor == "" {
		switch goos {
		case "darwin":
			return editorLaunchPlan{bin: "open", args: []string{targetPath}, isLauncher: true}, nil
		case "windows":
			return editorLaunchPlan{windowsShellOpen: true}, nil
		default:
			return editorLaunchPlan{bin: "xdg-open", args: []string{targetPath}, isLauncher: true}, nil
		}
	}
	if p, ok := findEditorPreset(editor); ok {
		if goos == "darwin" {
			roots := []string{"/Applications"}
			if home != "" {
				roots = append(roots, path.Join(home, "Applications"))
			}
			for _, appName := range p.macApps {
				for _, root := range roots {
					appPath := path.Join(root, appName+".app")
					if _, err := statFn(appPath); err == nil {
						return editorLaunchPlan{bin: "open", args: []string{"-a", appPath, targetPath}, isLauncher: true}, nil
					}
				}
			}
		}
		for _, bin := range p.bins {
			if binPath, err := lookPathFn(bin); err == nil {
				if goos == "windows" {
					if wslArgs, ok := resolveWindowsWSLEditorArgs(p.id, targetPath); ok {
						return makeEditorLaunchPlan(goos, binPath, wslArgs), nil
					}
				}
				return makeEditorLaunchPlan(goos, binPath, []string{targetPath}), nil
			}
		}
		return editorLaunchPlan{}, fmt.Errorf("未找到 %s，请在设置中检查默认编辑器", p.label)
	}
	if goos == "darwin" && strings.HasSuffix(strings.ToLower(editor), ".app") {
		if _, err := statFn(editor); err == nil {
			return editorLaunchPlan{bin: "open", args: []string{"-a", editor, targetPath}, isLauncher: true}, nil
		}
		return editorLaunchPlan{}, fmt.Errorf("未找到编辑器应用：%s", editor)
	}
	if strings.ContainsAny(editor, `/\`) {
		if info, err := statFn(editor); err == nil && !info.IsDir() {
			return makeEditorLaunchPlan(goos, editor, []string{targetPath}), nil
		}
		return editorLaunchPlan{}, fmt.Errorf("未找到编辑器：%s", editor)
	}
	if binPath, err := lookPathFn(editor); err == nil {
		return makeEditorLaunchPlan(goos, binPath, []string{targetPath}), nil
	}
	return editorLaunchPlan{}, fmt.Errorf("未找到编辑器命令：%s", editor)
}

func resolveEditorCommand(
	goos, home, editor, targetPath string,
	statFn func(string) (os.FileInfo, error),
	lookPathFn func(string) (string, error),
) (string, []string, error) {
	plan, err := resolveEditorLaunch(goos, home, editor, targetPath, statFn, lookPathFn)
	if err != nil {
		return "", nil, err
	}
	return plan.bin, plan.args, nil
}

const (
	editorEarlyExitWindow = 2 * time.Second
	editorStderrWaitDelay = 250 * time.Millisecond
)

func startAndReapEditor(cmd *exec.Cmd, isLauncher bool, onAsyncErr func(error)) error {
	stderr := &limitedBuffer{limit: 4096}
	cmd.Stderr = stderr
	// Avoid hanging cmd.Wait() if a spawned grandchild inherits stderr and stays open.
	cmd.WaitDelay = editorStderrWaitDelay
	hideConsoleWindow(cmd)
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		err := cmd.Wait()
		if err == nil || errors.Is(err, exec.ErrWaitDelay) || onAsyncErr == nil {
			return
		}
		// Launchers (open / xdg-open) exit quickly; any non-zero exit means launching failed.
		// Direct editor binaries may exit non-zero much later (e.g. terminated by user);
		// only surface immediate startup failures.
		if !isLauncher && time.Since(started) >= editorEarlyExitWindow {
			return
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			onAsyncErr(fmt.Errorf("%v: %s", err, msg))
		} else {
			onAsyncErr(err)
		}
	}()
	return nil
}

func openWithConfiguredEditor(targetPath, editor string, onAsyncErr func(error)) error {
	home, _ := os.UserHomeDir()
	plan, err := resolveEditorLaunch(runtime.GOOS, home, editor, targetPath, os.Stat, exec.LookPath)
	if err != nil {
		return err
	}
	if plan.windowsShellOpen {
		return openWithWindowsDefaultApp(targetPath)
	}
	cmd := exec.Command(plan.bin, plan.args...)
	return startAndReapEditor(cmd, plan.isLauncher, onAsyncErr)
}

func openWithDefaultEditor(path string) error {
	return openWithConfiguredEditor(path, "", nil)
}

func (a *app) openInEditor(targetPath string) error {
	if _, err := os.Stat(targetPath); err != nil {
		return err
	}
	return openWithConfiguredEditor(targetPath, a.externalEditor, func(err error) {
		a.post(func() {
			a.setBanner(trimMsg(err.Error()), "")
		})
	})
}

func (a *app) openEditorMenuLabel() string {
	return a.openEditorMenuLabelForContext(true)
}

func (a *app) openEditorMenuLabelForContext(inWorktree bool) string {
	if strings.TrimSpace(a.externalEditor) == "" {
		if !inWorktree {
			return tr("使用默认编辑器打开工作区文件")
		}
		return tr("使用默认编辑器打开")
	}
	if !inWorktree {
		return fmt.Sprintf(tr("在 %s 中打开工作区文件"), editorDisplayName(a.externalEditor))
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
	if !a.settingsHasUnsavedEdits() {
		a.settings.confirmClose = false
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

func (a *app) editorSettingsRows(c *ui.Context) []func() {
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
	return rows
}

func (a *app) editorSettings(c *ui.Context) {
	settingsSection(c, "settings-editor", "外部编辑器", a.editorSettingsRows(c)...)
}
