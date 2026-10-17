package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

type viewSettings struct {
	First            int
	More             int
	IncludeUntracked bool
	OpenDetails      bool
	GraphStyle       string
	Colors           string
	HideSubject      bool
	HideDate         bool
	HideAuthor       bool
	HideHash         bool
	HideGraph        bool
	Layout           ui.TableLayout
}

type settings struct {
	GitPath    string
	RecentSort string
	View       viewSettings
	Repos      map[string]viewSettings
	Layouts    map[string]ui.TableLayout
}

func defaultViewSettings() viewSettings {
	return viewSettings{First: pageFirst, More: pageMore, OpenDetails: true, GraphStyle: "圆角", Colors: "#1f78b4,#33a02c,#e31a1c,#ff7f00,#6a3d9a,#b15928,#17becf,#bc80bd"}
}

var userConfigDir = os.UserConfigDir

func configFile(name string) (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "GitClientPrototype")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".gitclient-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func loadSettings() (settings, error) {
	p := settings{View: defaultViewSettings(), RecentSort: "最近使用", Repos: map[string]viewSettings{}}
	path, err := configFile("settings.json")
	if err != nil {
		return p, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("设置文件无法读取：%w", err)
	}
	return p, nil
}

func (a *app) options(path string) viewSettings {
	v := a.prefs.View
	if local, ok := a.prefs.Repos[path]; ok {
		v = local
	}
	if v.First <= 0 || v.More <= 0 {
		return defaultViewSettings()
	}
	if layout, ok := a.prefs.Layouts[path]; ok {
		v.Layout = layout
	}
	return v
}

func parsePalette(s string) ([]ui.Color, error) {
	var colors []ui.Color
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "#")
		b, err := hex.DecodeString(part)
		if err != nil || len(b) != 3 {
			return nil, fmt.Errorf("图谱颜色须为逗号分隔的 #RRGGBB")
		}
		colors = append(colors, ui.RGBA(b[0], b[1], b[2], 1))
	}
	if len(colors) > 32 {
		return nil, fmt.Errorf("最多设置 32 种颜色")
	}
	return colors, nil
}

func (a *app) beginSettings() {
	a.settingsRepo = ""
	if tab := a.activeTab(); tab != nil {
		a.settingsRepo = tab.path
	}
	a.settingsScope = "用户设置"
	a.settingsView = a.options("")
	a.settingsFirst, a.settingsMore = strconv.Itoa(a.settingsView.First), strconv.Itoa(a.settingsView.More)
	a.settingsGit = a.prefs.GitPath
	a.settingsErr = ""
	a.showSettings = true
}

func (a *app) settingsModal(c *ui.Context) {
	ui.Modal(c.Key("settings"), &a.showSettings, func() {
		ui.Column(c).Width(560).Gap(8).Children(func() {
			ui.Text(c, "设置").Bold()
			scopes := []string{"用户设置"}
			if a.settingsRepo != "" {
				scopes = append(scopes, "当前仓库")
			}
			if ui.Select(c.Key("settings-scope"), &a.settingsScope, scopes).Changed() {
				path := ""
				if a.settingsScope == "当前仓库" {
					path = a.settingsRepo
				}
				a.settingsView = a.options(path)
				a.settingsFirst, a.settingsMore = strconv.Itoa(a.settingsView.First), strconv.Itoa(a.settingsView.More)
			}
			ui.Scroll(c).MaxHeight(440).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					ui.Text(c, "Git 可执行文件（用户级，留空使用 PATH）")
					ui.TextInput(c.Key("git-path"), &a.settingsGit).Width(520)
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, "首屏条数")
						ui.TextInput(c.Key("first-page"), &a.settingsFirst).Width(90)
						ui.Text(c, "每页条数")
						ui.TextInput(c.Key("more-page"), &a.settingsMore).Width(90)
					})
					ui.Checkbox(c.Key("include-untracked"), &a.settingsView.IncludeUntracked, "显示未跟踪文件")
					ui.Checkbox(c.Key("open-search-detail"), &a.settingsView.OpenDetails, "搜索跳转时自动打开详情")
					ui.Select(c.Key("graph-style"), &a.settingsView.GraphStyle, []string{"圆角", "折线"})
					ui.Text(c, "图谱颜色（#RRGGBB，以逗号分隔）")
					ui.TextInput(c.Key("graph-colors"), &a.settingsView.Colors).Width(520)
					ui.Checkbox(c.Key("hide-subject"), &a.settingsView.HideSubject, "隐藏描述 / 引用列")
					ui.Checkbox(c.Key("hide-graph"), &a.settingsView.HideGraph, "隐藏图谱列")
					ui.Checkbox(c.Key("hide-date"), &a.settingsView.HideDate, "隐藏日期列")
					ui.Checkbox(c.Key("hide-author"), &a.settingsView.HideAuthor, "隐藏作者列")
					ui.Checkbox(c.Key("hide-hash"), &a.settingsView.HideHash, "隐藏哈希列")
					ui.Text(c, "列宽可在提交表头拖动调整。")
				})
			})
			if a.settingsErr != "" {
				ui.Text(c, a.settingsErr).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("settings-cancel"), "取消").Clicked() {
					a.showSettings = false
				}
				if ui.PrimaryButton(c.Key("settings-save"), "保存设置").Clicked() {
					a.saveSettings()
				}
			})
		})
	})
}

func (a *app) saveSettings() {
	v := a.settingsView
	if v.HideSubject && v.HideGraph && v.HideAuthor && v.HideDate && v.HideHash {
		a.settingsErr = "至少显示一列"
		return
	}
	first, e1 := strconv.Atoi(a.settingsFirst)
	more, e2 := strconv.Atoi(a.settingsMore)
	if e1 != nil || e2 != nil || first < 1 || more < 1 || first > 5000 || more > 5000 {
		a.settingsErr = "每页条数须在 1 到 5000 之间"
		return
	}
	if _, err := parsePalette(a.settingsView.Colors); err != nil {
		a.settingsErr = err.Error()
		return
	}
	git := strings.TrimSpace(a.settingsGit)
	if git != "" {
		if _, err := exec.LookPath(git); err != nil {
			a.settingsErr = "找不到 Git 可执行文件：" + err.Error()
			return
		}
	}
	updated := a.prefs
	updated.Repos = make(map[string]viewSettings, len(a.prefs.Repos))
	for path, prefs := range a.prefs.Repos {
		updated.Repos[path] = prefs
	}
	a.settingsView.First, a.settingsView.More = first, more
	updated.GitPath = git
	if a.settingsScope == "当前仓库" {
		updated.Repos[a.settingsRepo] = a.settingsView
	} else {
		updated.View = a.settingsView
	}
	path, err := configFile("settings.json")
	if err == nil {
		err = writeJSON(path, updated)
	}
	if err != nil {
		a.settingsErr = err.Error()
		return
	}
	a.prefs = updated
	gitMu.Lock()
	gitOverride = git
	gitMu.Unlock()
	a.showSettings = false
	if tab := a.activeTab(); tab != nil {
		a.reload(tab)
	}
}

func (a *app) sortedRecent() []string {
	paths := append([]string(nil), a.recent...)
	switch a.prefs.RecentSort {
	case "名称":
		sort.SliceStable(paths, func(i, j int) bool { return filepath.Base(paths[i]) < filepath.Base(paths[j]) })
	case "完整路径":
		sort.Strings(paths)
	}
	return paths
}
