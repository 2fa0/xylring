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
		root.Tooltip(tab.locateErr)
		input.Error(tab.locateErr)
	}
	root.Border(1, border)
	return input, !busy && (submit || input.Submitted())
}

// busyRow says work is in progress: the spinner, what it is doing, and an
// optional hint, so panes, footers and dialogs read the same way.
func busyRow(c *ui.Context, key, label, hint string) ui.Element {
	t := c.Theme()
	row := ui.Row(c.Key(key)).Gap(8).AlignItems(ui.Center).Shrink(0)
	row.Children(func() {
		ui.Spinner(c).Shrink(0)
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine()
		if hint != "" {
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted).SingleLine()
		}
	})
	return row
}

// busyBlock centers the spinner in the room the work is replacing.
func busyBlock(c *ui.Context, key, label, hint string) ui.Element {
	col := ui.Column(c.Key(key)).Grow(1).Center()
	col.Children(func() {
		busyRow(c, key+"-row", label, hint)
	})
	return col
}

type emptyHintProps struct {
	// Icon shows above the title in the faintest text color.
	Icon *ui.SVG
	// Title and Description say what is missing and what to do about it.
	Title, Description string
	// Action builds a button under the text; it is left out when nil.
	Action func()
}

// emptyHint holds the room of content there is none of yet, naming what is
// missing instead of leaving a bare sentence. Callers grow it into the pane.
func emptyHint(c *ui.Context, key string, p emptyHintProps) ui.Element {
	t := c.Theme()
	col := ui.Column(c.Key(key)).Center().Padding(16).Gap(2)
	col.Children(func() {
		if p.Icon != nil {
			ui.Icon(c, p.Icon).Size(28, 28).TextColor(t.TextMuted.Alpha(0.5))
		}
		if p.Title != "" {
			ui.Text(c, p.Title).FontSize(13).FontWeight(500).TextColor(t.Text).TextAlign(ui.Center).Margin(6, 0, 0, 0)
		}
		if p.Description != "" {
			ui.Text(c, p.Description).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center).MaxWidth(300)
		}
		if p.Action != nil {
			ui.Row(c).Gap(8).Justify(ui.Center).Margin(10, 0, 0, 0).Children(p.Action)
		}
	})
	return col
}

// chipStyle is the surface a chip is painted on. A zero color leaves that
// layer out, so a chip may be only text.
type chipStyle struct {
	Text, Background, Border ui.Color
	Bold                     bool
	FontSize                 float32
}

// chip is the pill the app puts a ref, a state or a count in: one height,
// padding and radius wherever it shows. Callers own its key, tooltip, click
// and menu, and may add children beside the label.
func chip(c *ui.Context, key, text string, s chipStyle) ui.Element {
	e := ui.Row(c.Key(key)).Gap(4).AlignItems(ui.Center).Shrink(0).Padding(1, 6).Radius(4)
	if s.Background != ui.Transparent {
		e.Background(s.Background)
	}
	if s.Border != ui.Transparent {
		e.Border(1, s.Border)
	}
	font := s.FontSize
	if font == 0 {
		font = 12
	}
	e.Children(func() {
		t := ui.Text(c, text).SingleLine().FontSize(font).TextColor(s.Text)
		if s.Bold {
			t.FontWeight(600)
		}
	})
	return e
}

// chipSoft tints the chip with the color itself, for refs and states.
func chipSoft(c *ui.Context, key, text string, color ui.Color, bold bool) ui.Element {
	return chip(c, key, text, chipStyle{Text: color, Background: color.Alpha(0.14), Border: color.Alpha(0.45), Bold: bold})
}

// copyTextButton confirms a labelled copy in place, as copyIconButton does for
// an icon, so the result stays with the button rather than a toast elsewhere.
func copyTextButton(c *ui.Context, key, name, content string) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Shrink(0)
	if b.Clicked() {
		c.Services().WriteClipboard(content)
		b.Jump("copied", 1)
	}
	copied := b.AnimateTo("copied", 0, ui.Animation{Duration: 2 * time.Second}) > 0
	label := name
	if copied {
		label = "已复制"
	}
	b.Label(label)
	b.Children(func() {
		ui.Text(c, label)
	})
	return b
}

// kbdHint is a key cap: the shortcut reads as a key the user presses, not as
// part of the label it sits beside.
func kbdHint(c *ui.Context, key, keys string) ui.Element {
	t := c.Theme()
	e := ui.Row(c.Key(key)).Padding(2, 5).Radius(4).Shrink(0).Background(t.Surface).Border(1, t.Border)
	e.Children(func() {
		ui.Text(c, keys).FontSize(11).TextColor(t.TextMuted).SingleLine()
	})
	return e
}

// pathBreadcrumb names a file in its directory: the directory muted and the
// file itself bold, in one line that keeps the full path as its tooltip. A
// path already folded by displayPath reads the same way.
func pathBreadcrumb(c *ui.Context, key, path string) ui.Element {
	t := c.Theme()
	short := displayPath(path)
	dir, name := "", short
	if i := strings.LastIndex(short, "/"); i >= 0 {
		dir, name = short[:i+1], short[i+1:]
	}
	spans := make([]ui.Span, 0, 2)
	if dir != "" {
		spans = append(spans, ui.Span{Text: dir, Color: t.TextMuted})
	}
	spans = append(spans, ui.Span{Text: name, Color: t.Text, Weight: 700})
	return ui.RichText(c.Key(key), spans...).Tooltip(path).SingleLine().Grow(1).MinWidth(0)
}
