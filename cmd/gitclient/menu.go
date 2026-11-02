package main

import (
	"runtime"

	"github.com/egoist/mygo"
)

func (a *app) installMenu() {
	file := []*mygo.MenuItem{
		{ID: "import", Label: "导入仓库…", Accelerator: "CmdOrCtrl+O", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.pickRepo()
		}},
		mygo.Separator(),
		{ID: "close-tab", Label: "关闭仓库", Accelerator: "CmdOrCtrl+W", Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
			if a.activeTab() != nil {
				a.closeTab(a.active)
			}
		}},
	}
	if runtime.GOOS != "darwin" {
		file = append(file, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit})
	}
	items := []*mygo.MenuItem{
		{Label: "文件", Submenu: file},
		{Role: mygo.RoleEditMenu},
		{Label: "显示", Submenu: []*mygo.MenuItem{
			{ID: "refresh", Label: "刷新", Accelerator: "CmdOrCtrl+R", Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
				if tab := a.activeTab(); tab != nil {
					a.refresh(tab)
				}
			}},
			{ID: "compare", Label: "比较…", Accelerator: "CmdOrCtrl+D", Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
				if tab := a.activeTab(); tab != nil {
					a.openCompareModal(tab)
				}
			}},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
	}
	if runtime.GOOS == "darwin" {
		items = append([]*mygo.MenuItem{{Role: mygo.RoleAppMenu}}, items...)
	}
	a.menu = mygo.NewMenu(items)
	mygo.App.SetMenu(a.menu)
}

func (a *app) syncMenu() {
	if a.menu == nil {
		return
	}
	tab := a.activeTab()
	has := tab != nil
	busy := has && tab.loading
	a.setMenuEnabled("refresh", has && !busy)
	a.setMenuEnabled("compare", has && !busy)
	a.setMenuEnabled("close-tab", has)
}

func (a *app) setMenuEnabled(id string, on bool) {
	item := a.menu.ItemByID(id)
	if item == nil || item.IsEnabled() == on {
		return
	}
	item.SetEnabled(on)
}
