package main

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// closeGuardState 是退出前唯一一次确认。allow 一旦置上，窗口就按用户的意愿关掉：
// 这条路径只通向退出，所以不需要复位。
type closeGuardState struct {
	open  bool
	allow bool
}

// 正在跑的写操作记在 app 上，而不是仓库标签上：关掉仓库不会停下已经发起的 Git，
// 那个仓库也不在 a.tabs 里了。键是开始顺序，退出确认只说最近开始的那一个。
// 只在主线程读写，和其他界面状态一样。
func (a *app) startGitWrite(label string) int {
	if a.writes == nil {
		a.writes = map[int]string{}
	}
	a.writeSeq++
	a.writes[a.writeSeq] = label
	return a.writeSeq
}

func (a *app) finishGitWrite(id int) { delete(a.writes, id) }

// quitBusy 说明这一刻退出会放过哪个写操作。只认真正改仓库或远端状态的 Git 调用：
// fetch、读取历史和 diff 都能干净取消，看板写入在 OnClosed 里有 commitBoardDrafts
// 冲刷并在退出前等队列跑完；准备对话框里的读操作、打开弹窗时的查状态，都不会写东西。
func (a *app) quitBusy() string {
	label, newest := "", 0
	for id, name := range a.writes {
		if id > newest {
			label, newest = name, id
		}
	}
	return label
}

// closeGuardHook 挂在窗口关上之前：有写操作在跑就先拦住，问一句再退。
// 关窗口和 ⌘Q 都会走到这里，退出也是关窗口；系统注销、关机时 mygo 会忽略拦截，
// 那已经不是用户还能决定的事。
func (a *app) closeGuardHook(e *mygo.CloseEvent) {
	if a.quitGuard.allow || a.quitBusy() == "" {
		return
	}
	e.PreventDefault()
	a.quitGuard.open = true
	// OnClose 跑在原生关闭回调里，得自己把这一帧要回来，窗口才不会停在关了一半的样子。
	if a.win != nil {
		a.win.Invalidate()
	}
}

// closeGuardText 是确认框里那一句：说清正在做什么，以及退出的真实后果。退出只是关窗口，
// 已经交给 Git 的子进程不会被取消，所以不能说「退出会中断」。操作在确认期间结束时这句话
// 跟着变，不放着一个已经不成立的旧操作名。
func closeGuardText(busy string) string {
	if busy == "" {
		return tr("操作已完成。")
	}
	return fmt.Sprintf(tr("%s，退出不会取消这次操作。"), trMessage(busy))
}

func (a *app) closeGuardModal(c *ui.Context) {
	d := &a.quitGuard
	if !d.open {
		return
	}
	appModal(c.Key("close-guard-modal"), &d.open, tr("退出 Xylring？"), 420, false, func() {
		ui.Text(c, closeGuardText(a.quitBusy())).FontSize(13).TextColor(c.Theme().Text)
	}, func() {
		if ui.Button(c.Key("close-guard-stay"), tr("先不退出")).Clicked() {
			d.open = false
		}
		if dangerButton(c, "close-guard-quit", tr("仍要退出")).Clicked() {
			d.open = false
			d.allow = true
			if a.win != nil {
				a.win.Close()
			}
		}
	})
}
