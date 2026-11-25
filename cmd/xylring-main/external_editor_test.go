package main

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type fakeFileInfo struct{ dir bool }

func (f fakeFileInfo) Name() string       { return "fake" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0755 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

func TestResolveEditorCommand(t *testing.T) {
	noStat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	noPath := func(string) (string, error) { return "", errors.New("not found") }

	// System default per OS.
	for _, tc := range []struct {
		goos, wantBin    string
		wantArgs         []string
		wantShellExecute bool
		wantLauncher     bool
	}{
		{"darwin", "open", []string{"/work/repo"}, false, true},
		{"windows", "", nil, true, false},
		{"linux", "xdg-open", []string{"/work/repo"}, false, true},
	} {
		plan, err := resolveEditorLaunch(tc.goos, "/home/u", "", "/work/repo", noStat, noPath)
		if err != nil || plan.bin != tc.wantBin || !slices.Equal(plan.args, tc.wantArgs) || plan.windowsShellOpen != tc.wantShellExecute || plan.isLauncher != tc.wantLauncher {
			t.Fatalf("%s default: got (%+v, %v)", tc.goos, plan, err)
		}
	}

	// Preset resolves macOS .app bundle first, then CLI fallback.
	bin, args, err := resolveEditorCommand("darwin", "/Users/u", "cursor", "/work/repo/main.go",
		func(p string) (os.FileInfo, error) {
			if p == "/Users/u/Applications/Cursor.app" {
				return fakeFileInfo{dir: true}, nil
			}
			return nil, os.ErrNotExist
		}, noPath)
	if err != nil || bin != "open" || !slices.Equal(args, []string{"-a", "/Users/u/Applications/Cursor.app", "/work/repo/main.go"}) {
		t.Fatalf("darwin app bundle: got (%q, %v, %v)", bin, args, err)
	}

	bin, args, err = resolveEditorCommand("linux", "/home/u", "vscode", "/work/repo", noStat, func(name string) (string, error) {
		if name == "code" {
			return "/usr/bin/code", nil
		}
		return "", errors.New("missing")
	})
	if err != nil || bin != "/usr/bin/code" || !slices.Equal(args, []string{"/work/repo"}) {
		t.Fatalf("linux preset cli: got (%q, %v, %v)", bin, args, err)
	}

	if _, _, err := resolveEditorCommand("darwin", "/Users/u", "zed", "/work/repo", noStat, noPath); err == nil || !strings.Contains(err.Error(), "Zed") {
		t.Fatalf("expected missing preset error, got %v", err)
	}

	// Custom .app and custom command.
	bin, args, err = resolveEditorCommand("darwin", "/Users/u", "/Applications/Nova.app", "/work/repo",
		func(p string) (os.FileInfo, error) {
			if p == "/Applications/Nova.app" {
				return fakeFileInfo{dir: true}, nil
			}
			return nil, os.ErrNotExist
		}, noPath)
	if err != nil || bin != "open" || !slices.Equal(args, []string{"-a", "/Applications/Nova.app", "/work/repo"}) {
		t.Fatalf("custom mac app: got (%q, %v, %v)", bin, args, err)
	}

	bin, args, err = resolveEditorCommand("linux", "/home/u", "hx", "/work/file.go", noStat, func(name string) (string, error) {
		if name == "hx" {
			return "/usr/local/bin/hx", nil
		}
		return "", errors.New("missing")
	})
	if err != nil || bin != "/usr/local/bin/hx" || !slices.Equal(args, []string{"/work/file.go"}) {
		t.Fatalf("custom command: got (%q, %v, %v)", bin, args, err)
	}

	// Windows .cmd/.bat preset wrapper must run through cmd.exe /d /c call.
	plan, err := resolveEditorLaunch("windows", `C:\Users\u`, "vscode", `C:\My Projects\repo`, noStat, func(name string) (string, error) {
		if name == "code" {
			return `C:\Program Files\Microsoft VS Code\bin\code.cmd`, nil
		}
		return "", errors.New("missing")
	})
	wantCmdArgs := []string{"/d", "/c", "call", `C:\Program Files\Microsoft VS Code\bin\code.cmd`, `C:\My Projects\repo`}
	if err != nil || plan.bin != "cmd.exe" || !plan.isLauncher || !slices.Equal(plan.args, wantCmdArgs) {
		t.Fatalf("windows code.cmd preset: got (%+v, %v)", plan, err)
	}

	// Historical diff context must label the editor entry as opening the working tree file.
	appDefault := &app{}
	if got := appDefault.openEditorMenuLabelForContext(false); got != "使用默认编辑器打开工作区文件" {
		t.Fatalf("unexpected history default label: %q", got)
	}
	appNamed := &app{externalEditor: "cursor"}
	if got := appNamed.openEditorMenuLabelForContext(false); got != "在 Cursor 中打开工作区文件" {
		t.Fatalf("unexpected history named label: %q", got)
	}
}

func TestExternalEditorSettingsAndPersistence(t *testing.T) {
	oldDir := userConfigDir
	cfg := t.TempDir()
	userConfigDir = func() (string, error) { return cfg, nil }
	defer func() { userConfigDir = oldDir }()

	a := newApp()
	a.settings = settingsState{open: true}
	tt := ui.NewTester(a.view, 900, 760)
	defer retireTester(tt)
	tt.Frame()

	if !tt.HasText("默认编辑器") || !tt.HasText("系统默认") {
		t.Fatalf("missing external editor setting: %q", tt.Texts())
	}

	scrollToEditor := func() {
		for i := 0; i < 8; i++ {
			if node, ok := tt.AccessByRole(ui.RoleMenuButton, "默认编辑器"); ok && node.Bounds.Y+node.Bounds.H < 680 {
				return
			}
			tt.Scroll(450, 380, 0, 120)
			tt.Frame()
		}
	}
	scrollToEditor()

	clickControl(t, tt, "默认编辑器")
	if err := tt.ChooseMenuItem("Cursor"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.externalEditor != "cursor" {
		t.Fatalf("expected cursor preset, got %q", a.externalEditor)
	}
	if a.openEditorMenuLabel() != "在 Cursor 中打开" {
		t.Fatalf("unexpected menu label: %q", a.openEditorMenuLabel())
	}

	clickControl(t, tt, "默认编辑器")
	if err := tt.ChooseMenuItem("自定义…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Scroll(450, 380, 0, 120)
	tt.Frame()
	field, ok := tt.AccessByRole(ui.RoleTextField, "自定义编辑器")
	if !ok {
		t.Fatalf("missing custom editor input: %q", tt.Texts())
	}
	tt.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tt.Frame()
	tt.Type("/Applications/Nova.app")
	tt.Frame()
	if err := tt.Click("应用"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.externalEditor != "/Applications/Nova.app" {
		t.Fatalf("expected custom editor saved, got %q", a.externalEditor)
	}
	if a.openEditorMenuLabel() != "在 Nova 中打开" {
		t.Fatalf("unexpected custom menu label: %q", a.openEditorMenuLabel())
	}

	// Persistence roundtrip via session.json.
	p, err := configFile("session.json")
	if err != nil {
		t.Fatal(err)
	}
	prefs := a.preferences()
	if err := writeJSON(p, savedSession{Preferences: &prefs}); err != nil {
		t.Fatal(err)
	}
	restored := newApp()
	if restored.externalEditor != "/Applications/Nova.app" {
		t.Fatalf("restored externalEditor = %q", restored.externalEditor)
	}
}

func TestSidebarRepoMenuIncludesDefaultEditor(t *testing.T) {
	dir, _ := initTestRepo(t)
	tab := newTab(1, dir)
	tab.loaded = true
	tab.branch = "main"
	a := &app{tabs: []*repoTab{tab}, externalEditor: "zed"}
	tt := ui.NewTester(a.view, 1100, 720)
	defer retireTester(tt)
	tt.Frame()

	if err := tt.RightClick(repoTitle(dir)); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	if !slices.Contains(menu, "在 Zed 中打开") {
		t.Fatalf("expected editor item in repo context menu, got %v", menu)
	}
}

func TestStartAndReapEditorLauncherFailureAndNormalExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sh test")
	}

	errCh := make(chan error, 1)
	failCmd := exec.Command("sh", "-c", "echo 'boom launcher' >&2; exit 2")
	if err := startAndReapEditor(failCmd, true, func(err error) { errCh <- err }); err != nil {
		t.Fatalf("start failCmd: %v", err)
	}
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "boom launcher") {
			t.Fatalf("expected launcher stderr error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for launcher failure callback")
	}

	okErrCh := make(chan error, 1)
	okCmd := exec.Command("sh", "-c", "exit 0")
	if err := startAndReapEditor(okCmd, false, func(err error) { okErrCh <- err }); err != nil {
		t.Fatalf("start okCmd: %v", err)
	}
	select {
	case err := <-okErrCh:
		t.Fatalf("unexpected error for normal editor exit: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	// A grandchild keeping stderr open must not stall reporting an immediate launcher failure.
	grandchildErrCh := make(chan error, 1)
	grandchildCmd := exec.Command("sh", "-c", "(sleep 5 >&2 &); echo 'fast fail' >&2; exit 2")
	if err := startAndReapEditor(grandchildCmd, true, func(err error) { grandchildErrCh <- err }); err != nil {
		t.Fatalf("start grandchildCmd: %v", err)
	}
	select {
	case err := <-grandchildErrCh:
		if err == nil || !strings.Contains(err.Error(), "fast fail") {
			t.Fatalf("expected fast fail stderr error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for launcher failure when grandchild holds stderr")
	}
}
