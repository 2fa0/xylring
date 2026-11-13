package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// 命令日志面板的状态。它不是某个仓库的东西：面板按打开时的仓库过滤，
// 横幅上的按钮会带上它自己那条失败所在的仓库，菜单则用活动仓库。
type gitLogState struct {
	open     bool
	dir      string // 空表示整个会话
	selected int
	state    ui.ListState
}

var gitLogView = &gitLogState{selected: -1}

// openGitLog 打开面板。dir 为空时取活动仓库；selectFailure 选中最近一条失败。
func (a *app) openGitLog(dir string, selectFailure bool) {
	if dir == "" {
		if t := a.activeTab(); t != nil {
			dir = t.path
		}
	}
	p := gitLogView
	p.open, p.dir, p.state = true, dir, ui.ListState{}
	p.selected = -1
	if !selectFailure {
		return
	}
	for i, call := range gitLog.entries(p.dir) {
		if call.status == gitCallFailed || call.status == gitCallCancelled {
			p.selected = i
			return
		}
	}
}

func (a *app) gitLogModal(c *ui.Context) {
	p := gitLogView
	if !p.open {
		return
	}
	entries := gitLog.entries(p.dir)
	if p.selected >= len(entries) {
		p.selected = len(entries) - 1
	}
	t := c.Theme()
	w, h := c.Size()
	visible := true
	ui.DialogBase(c.Key("git-log-modal"), &visible, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.4))
		panel.Width(min(680, max(0, w-32))).Height(min(560, max(0, h-32))).
			Padding(0).Gap(0).Radius(12).Background(t.Background).
			Border(1, t.Border).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Label(tr("Git 日志"))
		panel.Children(func() {
			ui.Row(c.Key("header")).Padding(12, 16).Gap(8).AlignItems(ui.Center).Shrink(0).
				BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, tr("Git 日志")).FontSize(14).Bold().Grow(1).MinWidth(0)
				if ui.Button(c.Key("git-log-clear"), tr("清空")).Disabled(len(entries) == 0).Clicked() {
					gitLog.clear()
					p.selected, p.state = -1, ui.ListState{}
				}
				if closeIconButton(c, "git-log-close", tr("关闭对话框")).Clicked() {
					p.open = false
				}
			})
			ui.Themed(c.Key("git-log-list-theme"), listTheme(c)).Grow(1).MinWidth(0).MinHeight(0).Padding(6, 8, 8, 8).Children(func() {
				if len(entries) == 0 {
					emptyHint(c, "git-log-empty", emptyHintProps{Title: tr("没有记录")})
					return
				}
				a.gitLogList(c, entries)
			})
		})
	})
	if !visible {
		p.open = false
	}
}

// gitLogList 列出这几次调用，最近的一条在上。展开的是选中行：
// 点击、上下键都只有一个落点，不会出现「选中一行、展开另一行」。
func (a *app) gitLogList(c *ui.Context, entries []gitCall) {
	p := gitLogView
	t := c.Theme()
	selected := p.selected
	p.state.Selected = &selected
	p.state.Key = func(i int) any { return entries[i].id }
	p.state.Label = func(i int) string { return entries[i].command }
	ui.List(c.Key("git-log-list"), &p.state, len(entries), func(i int) {
		call := entries[i]
		open := i == p.selected
		// Selected rows are painted by the list itself (listTheme); a stripe of our
		// own would look the same as the choice.
		row := ui.Row(c).FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Start)
		color := t.Text
		switch call.status {
		case gitCallFailed:
			color = t.Danger
		case gitCallCancelled:
			color = t.Warning
		}
		row.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				ui.Text(c, call.command).Font("monospace").FontSize(12).TextColor(color).
					SingleLine().MinWidth(0).Tooltip(call.command)
				if open {
					a.gitLogDetails(c, call)
				}
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Text(c, gitLogDuration(call)).FontSize(11).TextColor(t.TextMuted).Tooltip(tr("耗时"))
				if label := gitLogExit(call); label != "" {
					ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
					ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Tooltip(tr("退出码"))
				}
			})
		})
	}).Grow(1).MinHeight(0).Gap(2)
	p.selected = selected
}

func gitLogDuration(call gitCall) string {
	if call.status == gitCallRunning {
		return tr("进行中")
	}
	return fmt.Sprintf("%d ms", call.ms)
}

func gitLogExit(call gitCall) string {
	switch call.status {
	case gitCallRunning:
		return ""
	case gitCallCancelled:
		return tr("已取消")
	default:
		return fmt.Sprintf("%d", call.exit)
	}
}

func (a *app) gitLogDetails(c *ui.Context, call gitCall) {
	a.gitLogOutput(c, call, "out", tr("输出"), call.out, call.stream)
	a.gitLogOutput(c, call, "err", tr("错误"), call.err, false)
}

// gitLogOutput 是一段可复制的输出。二进制只说字节数，流式输出干脆不记内容。
func (a *app) gitLogOutput(c *ui.Context, call gitCall, part, title string, out gitCallOutput, stream bool) {
	t := c.Theme()
	text := out.text
	// 只有真有内容可拿的块才配「复制」：字节数和不记录都不值得复制。
	copyable := text != ""
	switch {
	case stream:
		text, copyable = tr("流式输出不记录"), false
	case out.binary:
		text, copyable = fmt.Sprintf(tr("二进制输出（%d 字节）"), out.bytes), false
	case text == "":
		return
	case out.truncated:
		text += "\n" + fmt.Sprintf(tr("已截断，共 %d 字节"), out.bytes)
	}
	key := fmt.Sprintf("git-log-%d-%s", call.id, part)
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0)
		if copyable {
			copyTextButton(c, key+"-copy", "复制", text)
		}
	})
	ui.Scroll(c.Key(key)).MaxHeight(160).FillWidth().Padding(6, 8).Radius(t.Radius).Background(t.Background).Children(func() {
		ui.Text(c, text).Font("monospace").FontSize(11).TextColor(t.TextMuted)
	})
}
