package uikit

// Adapted from Lobe MyGo's pkg/action/{actionicon,copybutton}.go,
// pkg/field/searchbar.go, pkg/feedback/modal.go and pkg/nav/tabs.go.
// Copyright (c) 2026 LobeHub. MIT license: ../../third_party/lobe-mygo.LICENSE.
// https://github.com/lobehub/lobe-mygo

import (
	"strings"
	"time"

	"github.com/2fa0/xylring/internal/i18n"
	"github.com/egoist/mygo/ui"
)

// StyleActionIcon keeps MyGo's button semantics and the app's theme; only share the visual treatment.
func StyleActionIcon(c *ui.Context, b ui.Element, icon *ui.SVG, box, glyph float32, foreground ui.Color, active bool) ui.Element {
	t := c.Theme()
	b.Size(box, box).Radius(t.Radius).Center().Shrink(0).
		Cursor(ui.CursorPointer).Transition(ColorMotion())
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

// CopyIconButton keeps confirmation in MyGo's keyed element state, without timers or workers.
func CopyIconButton(c *ui.Context, key, name, content string) ui.Element {
	b := ui.ButtonBase(c.Key(key))
	if b.Clicked() {
		c.Services().WriteClipboard(content)
		b.Jump("copied", 1)
	}
	copied := b.AnimateTo("copied", 0, ui.Animation{Duration: 2 * time.Second}) > 0
	icon := IconCopy
	if copied {
		icon, name = IconCheck, "已复制"
	}
	b.Label(i18n.Tr(name)).Tooltip(i18n.Tr(name))
	return StyleActionIcon(c, b, icon, 24, 14, c.Theme().TextMuted, copied)
}

// AppModal keeps the native dialog's focus trap and dismissal, but pin actions outside
// the scrollable body. A running job must be cancelled through its own action.
func AppModal(c *ui.Context, open *bool, title string, width float32, busy bool, content, footer func()) {
	AppModalSized(c, open, title, width, 0, busy, content, footer)
}

// AppModalSized keeps the panel at one height when tall is above zero: it fills
// the window up to that height, so a dialog whose pages or loading states differ
// in length does not collapse and grow back while they change.
func AppModalSized(c *ui.Context, open *bool, title string, width, tall float32, busy bool, content, footer func()) {
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
			Border(1, t.Border).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Label(i18n.Tr(title))
		if tall > 0 {
			panel.Height(min(max(0, h-32), tall))
		}
		panel.Children(func() {
			ui.Row(c.Key("header")).Padding(12, 16).Gap(12).AlignItems(ui.Center).Shrink(0).
				BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, i18n.Tr(title)).FontSize(14).Bold().Grow(1).MinWidth(0)
				if CloseIconButton(c, "dialog-close", i18n.Tr("关闭对话框")).Disabled(busy).Clicked() {
					*open = false
				}
			})
			ui.Scroll(c.Key("body")).Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).FillWidth().Padding(16).Gap(10).Children(content)
			})
			ui.Row(c.Key("footer")).Padding(12, 16).Gap(8).AlignItems(ui.Center).Justify(ui.End).Shrink(0).
				BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(footer)
		})
	})
	if !visible && !busy {
		*open = false
	}
}

// BusyRow says work is in progress: the spinner, what it is doing, and an
// optional hint, so panes, footers and dialogs read the same way.
func BusyRow(c *ui.Context, key, label, hint string) ui.Element {
	t := c.Theme()
	row := ui.Row(c.Key(key)).Gap(8).AlignItems(ui.Center).Shrink(0)
	row.Children(func() {
		ui.Spinner(c).Shrink(0)
		ui.Text(c, i18n.TrMessage(label)).FontSize(12).TextColor(t.TextMuted).SingleLine()
		if hint != "" {
			ui.Text(c, i18n.TrMessage(hint)).FontSize(12).TextColor(t.TextMuted).SingleLine()
		}
	})
	return row
}

// BusyBlock centers the spinner in the room the work is replacing.
func BusyBlock(c *ui.Context, key, label, hint string) ui.Element {
	col := ui.Column(c.Key(key)).Grow(1).Center()
	col.Children(func() {
		BusyRow(c, key+"-row", label, hint)
	})
	return col
}

type EmptyHintProps struct {
	// Icon shows above the title in the faintest text color.
	Icon *ui.SVG
	// Title and Description say what is missing and what to do about it.
	Title, Description string
	// Action builds a button under the text; it is left out when nil.
	Action func()
}

// EmptyHint holds the room of content there is none of yet, naming what is
// missing instead of leaving a bare sentence. Callers grow it into the pane.
func EmptyHint(c *ui.Context, key string, p EmptyHintProps) ui.Element {
	t := c.Theme()
	col := ui.Column(c.Key(key)).Center().Padding(16).Gap(2)
	col.Children(func() {
		if p.Icon != nil {
			ui.Icon(c, p.Icon).Size(28, 28).TextColor(t.TextMuted.Alpha(0.5))
		}
		if p.Title != "" {
			ui.Text(c, i18n.TrMessage(p.Title)).FontSize(13).FontWeight(500).TextColor(t.Text).TextAlign(ui.Center).Margin(6, 0, 0, 0)
		}
		if p.Description != "" {
			ui.Text(c, i18n.TrMessage(p.Description)).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center).MaxWidth(300)
		}
		if p.Action != nil {
			ui.Row(c).Gap(8).Justify(ui.Center).Margin(10, 0, 0, 0).Children(p.Action)
		}
	})
	return col
}

// ChipStyle is the surface a chip is painted on. A zero color leaves that
// layer out, so a chip may be only text.
type ChipStyle struct {
	Text, Background, Border ui.Color
	Bold                     bool
	FontSize                 float32
	MaxWidth                 float32
}

// Chip is the pill the app puts a ref, a state or a count in: one height,
// padding and radius wherever it shows. Callers own its key, tooltip, click
// and menu, and may add children beside the label.
func Chip(c *ui.Context, key, text string, s ChipStyle) ui.Element {
	e := ui.Row(c.Key(key)).Gap(4).AlignItems(ui.Center).Shrink(0).Padding(1, 6).Radius(4)
	if s.MaxWidth > 0 {
		e.MaxWidth(s.MaxWidth).Clip()
	}
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
		t := ui.Text(c, text).SingleLine().MinWidth(0).FontSize(font).TextColor(s.Text)
		if s.MaxWidth > 0 {
			t.Shrink(1)
		}
		if s.Bold {
			t.FontWeight(600)
		}
	})
	return e
}

// ChipSoft tints the chip with the color itself, for refs and states.
func ChipSoft(c *ui.Context, key, text string, color ui.Color, bold bool) ui.Element {
	return Chip(c, key, text, ChipStyle{Text: color, Background: color.Alpha(0.14), Border: color.Alpha(0.45), Bold: bold})
}

// DangerButton matches MyGo's Button/PrimaryButton geometry while emphasizing
// destructive confirmations with the theme's Danger color.
func DangerButton(c *ui.Context, key, label string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).
		Label(label).
		Padding(t.Space(1.5), t.Space(3.5)).
		Gap(t.Space(1.5)).
		Radius(t.Radius).
		Cursor(ui.CursorPointer).
		Transition(ColorMotion())
	bg := t.Danger
	switch {
	case b.Pressed():
		bg = t.Danger.Alpha(0.82)
	case b.Hovered():
		bg = t.Danger.Alpha(0.9)
	}
	b.Background(bg).Border(1, bg).TextColor(t.AccentText)
	b.Children(func() {
		ui.Text(c, label).SingleLine().TextColor(t.AccentText)
	})
	return b
}

// CopyTextButton confirms a labelled copy in place, as CopyIconButton does for
// an icon, so the result stays with the button rather than a toast elsewhere.
func CopyTextButton(c *ui.Context, key, name, content string) ui.Element {
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
	b.Label(i18n.Tr(label))
	b.Children(func() {
		ui.Text(c, i18n.Tr(label))
	})
	return b
}

// KbdHint is a key cap: the shortcut reads as a key the user presses, not as
// part of the label it sits beside.
func KbdHint(c *ui.Context, key, keys string) ui.Element {
	t := c.Theme()
	e := ui.Row(c.Key(key)).Padding(2, 5).Radius(4).Shrink(0).Background(t.Surface).Border(1, t.Border)
	e.Children(func() {
		ui.Text(c, keys).FontSize(11).TextColor(t.TextMuted).SingleLine()
	})
	return e
}

// DisplayPath shortens a path for a title row: the file and the directory it
// is in, with what comes before them folded into an ellipsis. Keeping the
// nearest directory says more than keeping the topmost one. Paths of three
// segments or fewer are shown whole.
func DisplayPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	if len(parts) <= 3 {
		return p
	}
	return "…/" + parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

// PathBreadcrumb names a file in its directory: the directory muted and the
// file itself bold, in one line that keeps the full path as its tooltip. A
// path already folded by DisplayPath reads the same way.
func PathBreadcrumb(c *ui.Context, key, path string) ui.Element {
	t := c.Theme()
	short := DisplayPath(path)
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
