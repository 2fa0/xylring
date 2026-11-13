package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 构建配置是版本号与更新源的唯一来源：cli.go 的兜底版本和它差一位，
// `xylring --version` 就会和清单对不上，更新链从这里开始断。
func TestBuildConfigMatchesVersionAndUpdates(t *testing.T) {
	data, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Version string `json:"version"`
		Updates *struct {
			PublicKey string `json:"publicKey"`
			GitHub    string `json:"github"`
			Changelog string `json:"changelog"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != defaultVersion {
		t.Fatalf("mygo.json version %q, cli.go defaultVersion %q", cfg.Version, defaultVersion)
	}
	if cfg.Updates == nil {
		t.Fatal("mygo.json has no updates: builds cannot sign updates")
	}
	if cfg.Updates.PublicKey == "" || cfg.Updates.GitHub != "2fa0/xylring-release" || cfg.Updates.Changelog == "" {
		t.Fatalf("updates config is incomplete: %+v", *cfg.Updates)
	}
}

// 测试和命令行路径不跑 main，更新插件从未注册；这层包装必须全程可调用，
// 不能让 plugins/updater 的 used() 把它变成 panic。
func TestUpdateHelpersWorkWithoutRegisteredPlugin(t *testing.T) {
	if updaterReady.Load() {
		t.Fatal("a test registered the updater plugin; helpers must stay unregistered here")
	}
	if item := updatesMenuItem(); item != nil {
		t.Fatal("update menu item without the plugin")
	}
	if automaticUpdates() {
		t.Fatal("automatic checks reported on without the plugin")
	}
	if got := currentUpdatesStatus(); got.version != "" || !got.lastCheck.IsZero() {
		t.Fatalf("update status without the plugin: %+v", got)
	}
	setAutomaticUpdates(true)
	checkUpdates()
}

// 更新不了的时候（测试与命令行构建、直接在 DMG 里打开、装在别的管理员名下），
// 这一组要置灰并说清原因，而不是给一个按下去没反应的开关。
func TestSettingsUpdateSectionSaysWhyItCannotUpdate(t *testing.T) {
	if updaterReady.Load() {
		t.Fatal("a test registered the updater plugin; this one covers the unregistered path")
	}
	if updatesUsable() {
		t.Fatal("an unregistered updater reported itself usable")
	}
	a := &app{}
	tt := ui.NewTester(func(c *ui.Context) { a.updateSettings(c) }, 760, 260)
	defer retireTester(tt)
	tt.Frame()
	for _, label := range []string{"更新", "自动检查更新", "当前版本 " + defaultVersion, "这个构建不能自更新", "检查更新"} {
		if !tt.HasText(label) {
			t.Fatalf("missing %q: %q", label, tt.Texts())
		}
	}
	switchInfo, ok := tt.AccessByRole(ui.RoleSwitch, "自动检查更新")
	if !ok || !switchInfo.Disabled {
		t.Fatalf("reported the auto-check switch as usable: %+v", switchInfo)
	}
	button, ok := tt.AccessByRole(ui.RoleButton, "检查更新")
	if !ok || !button.Disabled {
		t.Fatalf("reported the check button as usable: %+v", button)
	}
	if err := tt.Click("检查更新"); err != nil {
		t.Fatalf("check button is not reachable: %v", err)
	}
	tt.Frame()
}
