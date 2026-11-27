package app

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

type fileTabIndicatorMatch struct{ tab *repoTab }

func fileTabIndicator(c *ui.Context, tab *repoTab) {
	ui.Box(c.Key("selected-indicator")).Absolute().Left(6).Right(6).Bottom(0).Height(2).
		Radius(1).Background(c.Theme().Accent).IgnoreInput(true).
		Transition(ui.ElementTransition{Duration: motionPane, Ease: ui.EaseOut, Match: fileTabIndicatorMatch{tab}})
}

func omniSearchField(c *ui.Context, tab *repoTab) (input ui.Element, submit bool) {
	t := c.Theme()
	busy := tab.loading && strings.TrimSpace(tab.searchText) == tab.query.term && tab.searchKind == tab.query.kind
	root := ui.Row(c.Key("search-field")).Grow(1).MinWidth(0).Height(30).Padding(0, 6).Gap(4).
		AlignItems(ui.Center).Radius(15).Background(t.Background).Transition(colorMotion())
	root.Children(func() {
		if busy {
			ui.Spinner(c.Key("search-loading")).Size(16, 16).Margin(4).Shrink(0).Label(tr("正在搜索"))
		} else {
			submit = paneIconButton(c, "search-history", tr("搜索"), iconSearch).Tooltip(tr("搜索或跳转 (Enter)")).Clicked()
		}
		input = ui.TextInputBase(c.Key(fmt.Sprintf("search-%d", tab.id)), &tab.searchText).
			Label(tr("搜索提交、代码、分支、作者或路径")).Placeholder(tr("搜索提交、代码、分支、作者或路径…")).
			Bind(&tab.searchInput).Grow(1).MinWidth(0).FocusRing(false).
			KeyShortcuts(commandModifier(), ui.KeyP)
		if tab.searchText != "" {
			if closeIconButton(c, "search-reset", tr("清空搜索输入")).Clicked() {
				tab.searchText, tab.locate, tab.locateErr = "", "", ""
				tab.locateGen++
				tab.searchSuggestOpen = false
				input.Focus()
			}
		} else if !input.Focused() {
			shortcut := "Ctrl+P"
			if runtime.GOOS == "darwin" {
				shortcut = "⌘P"
			}
			kbdHint(c, "search-shortcut", shortcut)
		}
	})
	border := t.Border
	if root.Hovered() {
		border = t.TextMuted
	}
	if input.Focused() {
		border = t.Accent
		root.Shadow(0, 0, 0, 2, t.Accent.Alpha(0.15))
	}
	if tab.locateErr != "" {
		border = t.Danger
		root.Tooltip(trMessage(tab.locateErr))
		input.Error(trMessage(tab.locateErr))
	}
	root.Border(1, border)
	return input, !busy && (submit || input.Submitted())
}
