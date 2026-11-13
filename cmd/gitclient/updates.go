package main

import (
	_ "embed"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
)

// 更新窗口的图标：插件默认找 resources/icon.png，本项目的应用图标叫 logo.png 且是
// 800 KB 的原图，窗口用不上，所以单独放一张 256 px 的缩小版。
//
//go:embed resources/update-window.png
var updateIcon []byte

// updaterReady 记着更新插件注册过没有。plugins/updater 的入口在插件没注册时一律
// panic，而 go test、--version 这类路径根本不跑 main，所以本包只留下面这层包装，
// 别处不直接引用插件包。
var updaterReady atomic.Bool

// updatesInstallable 记着这个实例能不能换掉自己：插件注册过，而且安装位置可写。
// 从 DMG 里直接运行、或装在别的管理员名下时这里是 false——那种情况下界面不该装作
// 能更新。mygo.Updater.Enabled 要在安装目录里试建一个临时目录，所以只问一次，
// 第一次问通常发生在后台（见 watchUpdates）。
var (
	updatesInstallable     bool
	updatesInstallableOnce sync.Once
)

// useUpdater 注册 MyGo 的更新窗口，签名校验、下载和安装都交给它。必须在
// mygo.App.Run 之前调用：插件在启动 10 秒后自己查一次，之后每天一次，有新版本才
// 弹窗口。
//
// 检查、下载、验签都在插件自己的 goroutine 里：这里只是登记。断网、超时、被墙
// 都只会让那一次检查悄悄失败，界面不等网络，也不出应用横幅。
func useUpdater() {
	mygo.Use(native.New(updater.Options{Icon: updateIcon}))
	updaterReady.Store(true)
}

// updatesMenuItem 是菜单里的「检查更新…」。文案和语言由插件按 App.Locale 给，
// 不自造。没注册时返回 nil，调用方照 nil 不挂这一项。
func updatesMenuItem() *mygo.MenuItem {
	if !updaterReady.Load() {
		return nil
	}
	return updater.MenuItem()
}

// checkUpdates 把「现在查一次」交给插件就返回：请求、超时和失败都在它自己的
// goroutine 里，界面不等网络。
func checkUpdates() {
	if !updaterReady.Load() {
		return
	}
	updater.CheckForUpdates()
}

// updatesStatus 是设置页要说清的两个事实，都是插件的内存状态（上次检查、查到的
// 版本），读它不发请求。
type updatesStatus struct {
	version   string    // 插件查到的可用版本，空表示没有
	lastCheck time.Time // 上次检查成功的时间，零值表示还没查过
}

func currentUpdatesStatus() updatesStatus {
	if !updaterReady.Load() {
		return updatesStatus{}
	}
	return updatesStatus{version: updater.AvailableVersion(), lastCheck: updater.LastCheck()}
}

// automaticUpdates 是插件的后台检查开关：装好默认开，用户可以在设置页关掉。
func automaticUpdates() bool {
	if !updaterReady.Load() {
		return false
	}
	return updater.AutomaticChecks()
}

func setAutomaticUpdates(on bool) {
	if !updaterReady.Load() {
		return
	}
	updater.SetAutomaticChecks(on)
}

// updatesUsable 报告现在能不能真的更新：插件注册过，安装位置也可写。不可写时
// 设置页把这一组置灰并说清原因，而不是给一个按下去没反应的开关。
func updatesUsable() bool {
	if !updaterReady.Load() {
		return false
	}
	updatesInstallableOnce.Do(func() { updatesInstallable = mygo.Updater.Enabled() })
	return updatesInstallable
}

// watchUpdates 把插件的变化折成一次重绘：后台查完、用户在更新窗口里选了跳过或
// 稍后，设置页里的版本与时间要跟着改。回调本来就在主线程，只读内存状态。
func (a *app) watchUpdates() {
	if !updaterReady.Load() {
		return
	}
	updater.OnChange(func() {
		// 应用正在退出时窗口已经销毁，不再排队重绘。
		if a.closed {
			return
		}
		a.post(func() {})
	})
	// 插件的偏好存在 updater.json 里，第一次问号时会读一次盘；能否自动更新还要
	// 在安装目录里试建临时目录。都先在后台问一遍，打开设置页那一帧只读内存。
	go func() {
		automaticUpdates()
		currentUpdatesStatus()
		updatesUsable()
	}()
}
