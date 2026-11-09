package main

// Adapted from Lobe MyGo's pkg/action/{actionicon,copybutton}.go,
// pkg/field/searchbar.go, pkg/feedback/modal.go and pkg/nav/tabs.go.
// Copyright (c) 2026 LobeHub. MIT license: ../../third_party/lobe-mygo.LICENSE.
// https://github.com/lobehub/lobe-mygo

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// Keep MyGo's button semantics and the app's theme; only share the visual treatment.
func styleActionIcon(c *ui.Context, b ui.Element, icon *ui.SVG, box, glyph float32, foreground ui.Color, active bool) ui.Element {
	t := c.Theme()
	b.Size(box, box).Radius(t.Radius).Center().Shrink(0).
		Cursor(ui.CursorPointer).Transition(colorMotion())
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	case active:
		b.Background(t.Accent.Alpha(0.12))
	}
	if active {
		foreground = t.Accent
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(glyph, glyph).TextColor(foreground)
	})
	return b
}

// The confirmation lives in MyGo's keyed element state, without timers or workers.
func copyIconButton(c *ui.Context, key, name, content string) ui.Element {
	b := ui.ButtonBase(c.Key(key))
	if b.Clicked() {
		c.Services().WriteClipboard(content)
		b.Jump("copied", 1)
	}
	copied := b.AnimateTo("copied", 0, ui.Animation{Duration: 2 * time.Second}) > 0
	icon := iconCopy
	if copied {
		icon, name = iconCheck, "已复制"
	}
	b.Label(name).Tooltip(name)
	return styleActionIcon(c, b, icon, 24, 14, c.Theme().TextMuted, copied)
}

// Keep the native dialog's focus trap and dismissal, but pin actions outside
// the scrollable body. A running job must be cancelled through its own action.
func appModal(c *ui.Context, open *bool, title string, width float32, busy bool, content, footer func()) {
	if !*open {
		return
	}
	w, h := c.Size()
	t := c.Theme()
	visible := true
	ui.DialogBase(c, &visible, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.4))
		panel.Width(min(width, max(0, w-32))).MaxHeight(max(0, h-32)).
			Padding(0).Gap(0).Radius(12).Background(t.Background).
			Border(1, t.Border).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Label(title)
		panel.Children(func() {
			ui.Row(c.Key("header")).Padding(12, 16).Gap(12).AlignItems(ui.Center).Shrink(0).
				BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, title).FontSize(14).Bold().Grow(1).MinWidth(0)
				if closeIconButton(c, "dialog-close", "关闭对话框").Disabled(busy).Clicked() {
					*open = false
				}
			})
			ui.Scroll(c.Key("body")).Shrink(1).MinHeight(0).Children(func() {
				ui.Column(c).FillWidth().Padding(16).Gap(10).Children(content)
			})
			ui.Row(c.Key("footer")).Padding(12, 16).Gap(8).Justify(ui.End).Shrink(0).
				BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(footer)
		})
	})
	if !visible && !busy {
		*open = false
	}
}

type fileTabIndicatorMatch struct{ tab *repoTab }

func fileTabIndicator(c *ui.Context, tab *repoTab) {
	ui.Box(c.Key("selected-indicator")).Absolute().Left(6).Right(6).Bottom(0).Height(2).
		Radius(1).Background(c.Theme().Accent).IgnoreInput(true).
		Transition(ui.ElementTransition{Duration: motionPane, Ease: ui.EaseOut, Match: fileTabIndicatorMatch{tab}})
}

func omniSearchField(c *ui.Context, tab *repoTab) (input ui.Element, submit bool) {
	t := c.Theme()
	busy := tab.loading && strings.TrimSpace(tab.searchText) == tab.query.term && tab.searchKind == tab.query.kind
	root := ui.Row(c.Key("search-field")).Grow(1).MinWidth(0).Height(32).Padding(0, 4).Gap(4).
		AlignItems(ui.Center).Radius(t.Radius).Background(t.Background).Transition(colorMotion())
	root.Children(func() {
		if busy {
			ui.Spinner(c.Key("search-loading")).Size(16, 16).Margin(4).Shrink(0).Label("正在搜索")
		} else {
			submit = paneIconButton(c, "search-history", "搜索", iconSearch).Tooltip("搜索或跳转 (Enter)").Clicked()
		}
		input = ui.TextInputBase(c.Key(fmt.Sprintf("search-%d", tab.id)), &tab.searchText).
			Label("搜索提交、代码、分支、作者或路径").Placeholder("搜索提交、代码、分支、作者或路径…").
			Bind(&tab.searchInput).Grow(1).MinWidth(0).FocusRing(false).
			KeyShortcuts(commandModifier(), ui.KeyP)
		if tab.searchText != "" {
			if closeIconButton(c, "search-reset", "清空搜索输入").Clicked() {
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
			ui.Text(c, shortcut).FontSize(11).TextColor(t.TextMuted).Shrink(0).Padding(2, 4)
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
		root.Tooltip(tab.locateErr)
		input.Error(tab.locateErr)
	}
	root.Border(1, border)
	return input, !busy && (submit || input.Submitted())
}
