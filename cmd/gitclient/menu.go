package main

import (
	"runtime"

	"github.com/egoist/mygo"
)

func (a *app) installMenu() {
	settingsItem := &mygo.MenuItem{ID: "settings", Label: tr("设置…"), Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }}
	file := []*mygo.MenuItem{
		{ID: "import", Label: tr("导入仓库…"), Accelerator: "CmdOrCtrl+O", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.pickRepo()
		}},
		mygo.Separator(),
		{ID: "install-cli", Label: tr("在 PATH 中安装 'xylring' 命令行工具…"), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.installCLIMenu()
		}},
		{ID: "uninstall-cli", Label: tr("从 PATH 中卸载 'xylring' 命令行工具"), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.uninstallCLIMenu()
		}},
		mygo.Separator(),
		{ID: "close-tab", Label: tr("关闭当前标签页或仓库"), Accelerator: "CmdOrCtrl+W", Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
			a.closeCurrentTab()
		}},
	}
	if runtime.GOOS != "darwin" {
		file = append(file, settingsItem, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit})
	}
	operations := []*mygo.MenuItem{
		{Label: tr("新建仓库"), Submenu: []*mygo.MenuItem{
			{Label: tr("克隆仓库…"), Click: func(*mygo.MenuItem, *mygo.Window) { a.openOperation("克隆仓库", "", "") }},
			{Label: tr("初始化仓库…"), Click: func(*mygo.MenuItem, *mygo.Window) { a.openOperation("初始化仓库", "", "") }},
		}},
		{Label: "Stash…", Click: func(*mygo.MenuItem, *mygo.Window) { a.openOperation("Stash", "", "") }},
		{Label: tr("远程…"), Click: func(*mygo.MenuItem, *mygo.Window) { a.openOperation("远程", "", "") }},
		{Label: tr("设置身份…"), Click: func(*mygo.MenuItem, *mygo.Window) { a.openOperation("设置身份", "", "") }},
	}
	items := []*mygo.MenuItem{
		{Label: tr("仓库"), Submenu: operations},
		{Label: tr("文件"), Submenu: file},
		{Role: mygo.RoleEditMenu},
		{Label: tr("显示"), Submenu: []*mygo.MenuItem{
			{ID: "toggle-sidebar", Label: tr("切换侧边栏"), Accelerator: "CmdOrCtrl+B", Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
				a.toggleSidebar()
			}},
			{ID: "toggle-bottom-full-width", Label: tr("切换底栏全宽展示"), Click: func(*mygo.MenuItem, *mygo.Window) {
				a.toggleBottomFullWidth()
			}},
			mygo.Separator(),
			{ID: "fetch", Label: tr("获取远程…"), Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
				if tab := a.activeTab(); tab != nil {
					a.openFetch(tab)
				}
			}},
			{ID: "refresh", Label: tr("刷新"), Accelerator: "CmdOrCtrl+R", Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
				if tab := a.activeTab(); tab != nil {
					a.refresh(tab)
				}
			}},
			{ID: "compare", Label: tr("比较…"), Accelerator: "CmdOrCtrl+D", Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
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
		items = append([]*mygo.MenuItem{{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout}, mygo.Separator(), settingsItem, mygo.Separator(),
			{Role: mygo.RoleServices}, mygo.Separator(), {Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(), {Role: mygo.RoleQuit},
		}}}, items...)
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
	empty := tab == nil && len(a.found) <= 1
	a.setMenuEnabled("toggle-sidebar", !empty)
	a.setMenuEnabled("toggle-bottom-full-width", has)
	a.setMenuEnabled("fetch", has && !tab.unavailable && !a.fetch.busy && !a.write.busy)
	a.setMenuEnabled("refresh", has && !busy)
	a.setMenuEnabled("compare", has && !tab.unavailable && !busy)
	a.setMenuEnabled("close-tab", has)
}

func (a *app) setMenuEnabled(id string, on bool) {
	item := a.menu.ItemByID(id)
	if item == nil || item.IsEnabled() == on {
		return
	}
	item.SetEnabled(on)
}
