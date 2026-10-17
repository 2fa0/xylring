package main

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"os"
	"runtime"
)

type savedTab struct {
	Path, Scope, Query, HistoryPath, SearchText, SearchScope string
	Refs                                                     []string
	Hash                                                     [20]byte
	Dirty                                                    bool
	Count, Row                                               int
}

type savedSession struct {
	Tabs   []savedTab
	Active int
}

func (a *app) saveSession() {
	if !a.persist {
		return
	}
	s := savedSession{Active: a.active}
	for _, t := range a.tabs {
		t.captureSelection()
		s.Tabs = append(s.Tabs, savedTab{Path: t.path, Scope: t.scope, Query: t.query, HistoryPath: t.historyPath, SearchText: t.searchText, SearchScope: t.searchScope, Refs: t.historyRefs, Hash: t.wantHash, Dirty: t.wantDirty, Count: t.resumeCount, Row: t.resumeRow})
	}
	path, err := configFile("session.json")
	if err == nil {
		err = writeJSON(path, s)
	}
	if err != nil {
		a.banner = "保存标签失败：" + err.Error()
	}
	// TableLayout holds only column preferences, never row data.
	for _, t := range a.tabs {
		if len(t.tableLayout.Widths) > 0 {
			if a.prefs.Layouts == nil {
				a.prefs.Layouts = map[string]ui.TableLayout{}
			}
			a.prefs.Layouts[t.path] = t.tableLayout
		}
	}
	if path, err := configFile("settings.json"); err == nil {
		if err = writeJSON(path, a.prefs); err != nil {
			a.banner = err.Error()
		}
	}
}

func (a *app) restoreSession() {
	path, err := configFile("session.json")
	if err != nil {
		a.banner = err.Error()
		return
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	var s savedSession
	if err != nil {
		a.banner = err.Error()
		return
	}
	if err = json.Unmarshal(data, &s); err != nil {
		a.banner = "恢复标签失败：" + err.Error()
		return
	}
	for _, old := range s.Tabs {
		if _, err := os.Stat(old.Path); err != nil {
			a.banner = "上次仓库不可用：" + old.Path
			continue
		}
		a.nextID++
		t := newTab(a.nextID, old.Path)
		t.scope, t.scopeChoice, t.query = old.Scope, old.Scope, old.Query
		t.historyRefs, t.historyPath, t.searchText = old.Refs, old.HistoryPath, old.SearchText
		t.searchScope = old.SearchScope
		t.wantHash, t.wantDirty, t.resumeCount, t.resumeRow = old.Hash, old.Dirty, old.Count, old.Row
		t.released = true
		a.tabs = append(a.tabs, t)
	}
	if len(a.tabs) > 0 {
		a.active = min(max(0, s.Active), len(a.tabs)-1)
		a.reload(a.tabs[a.active])
	}
}

func (a *app) moveTab(id, to int) {
	from := -1
	for i, t := range a.tabs {
		if t.id == id {
			from = i
			break
		}
	}
	if from < 0 || to < 0 || to >= len(a.tabs) || from == to {
		return
	}
	active := a.activeTab()
	t := a.tabs[from]
	a.tabs = append(a.tabs[:from], a.tabs[from+1:]...)
	a.tabs = append(a.tabs, nil)
	copy(a.tabs[to+1:], a.tabs[to:])
	a.tabs[to] = t
	for i, t := range a.tabs {
		if t == active {
			a.active = i
			break
		}
	}
	a.saveSession()
}

func (a *app) tabShortcuts(c *ui.Context) {
	modifier := ui.Cmd
	if runtime.GOOS != "darwin" {
		modifier = ui.Ctrl
	}
	if c.Shortcut(modifier, ui.KeyT) {
		a.pickRepo()
	}
	if c.Shortcut(modifier, ui.KeyW) {
		a.closeTab(a.active)
	}
	for i := 0; i < min(9, len(a.tabs)); i++ {
		if c.Shortcut(modifier, ui.Key1+ui.Key(i)) {
			a.activate(i)
		}
	}
	if len(a.tabs) > 0 {
		if c.Shortcut(ui.Ctrl, ui.KeyTab) {
			a.activate((a.active + 1) % len(a.tabs))
		}
		if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
			a.activate((a.active + len(a.tabs) - 1) % len(a.tabs))
		}
	}
}
