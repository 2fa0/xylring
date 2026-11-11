package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAppearanceSwitchesFromSettings(t *testing.T) {
	for _, graph := range []bool{false, true} {
		a := &app{}
		if graph {
			a.tabs = []*repoTab{graphTabForWrite()}
		}
		var dark bool
		tt := ui.NewTester(func(c *ui.Context) {
			a.view(c)
			dark = c.Theme().Dark
		}, 1100, 720)
		defer retireTester(tt)
		tt.SetDark(false)
		tt.Frame()
		if err := tt.Click("设置"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		for _, step := range []struct {
			label, mode string
			dark        bool
		}{
			{"深色", "dark", true},
			{"浅色", "light", false},
			{"跟随系统", "", false},
		} {
			if err := tt.Click("外观"); err != nil {
				t.Fatal(err)
			}
			if err := tt.ChooseMenuItem(step.label); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			saveLobePreview(t, tt, "appearance-"+step.mode)
			if a.themeMode != step.mode || dark != step.dark {
				t.Fatalf("%s: mode=%q dark=%t", step.label, a.themeMode, dark)
			}
		}
		tt.SetDark(true)
		tt.Frame()
		if !dark {
			t.Fatal("system appearance changes must apply in automatic mode")
		}
		if err := tt.Click("外观"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("浅色"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if dark {
			t.Fatal("manual light mode must override a dark system")
		}
	}
}

func TestAppearancePersistsAcrossLaunches(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()
	a := newApp()
	if a.themeMode != "" {
		t.Fatal("new installs should follow the system")
	}
	a.sessionReady = true
	for _, mode := range []string{"dark", "light", ""} {
		a.themeMode = mode
		a.queueSession()
		if a.lastSession.ThemeMode != mode {
			t.Fatal("theme-only change was not queued")
		}
	}
	close(a.sessionWrites)
	<-a.sessionDone
	if got := newApp().themeMode; got != "" {
		t.Fatalf("restored mode=%q", got)
	}
	for _, mode := range []string{"dark", "light", "unknown"} {
		p, err := configFile("session.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(p, savedSession{ThemeMode: mode}); err != nil {
			t.Fatal(err)
		}
		want := mode
		if mode == "unknown" {
			want = ""
		}
		if got := newApp().themeMode; got != want {
			t.Fatalf("restored mode=%q want=%q", got, want)
		}
	}
}
