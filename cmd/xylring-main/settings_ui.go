package main

import "github.com/egoist/mygo/ui"

// 设置页读起来是一摞卡片：卡片自己带着标题、边框和底色，行与行用一条往底色里
// 褪过的细线分开。一行的形状固定——名字和说明在左，改它的控件在右。卡片的分组、
// 行距和字号跟着另一个应用 snapover 的设置页走，两边的设置页因此是同一种手感。

// settingsControlWidth is how much room a row leaves for its control: enough
// for a menu's chosen value, less on a narrow window that has less to give.
func settingsControlWidth(c *ui.Context) float32 {
	if w, _ := c.Size(); w < 620 {
		return 142
	}
	return 176
}

func settingsName(c *ui.Context, label string) {
	ui.Text(c, tr(label)).FontSize(14).FontWeight(500).TextColor(c.Theme().Text)
}

func settingsDetail(c *ui.Context, text string) {
	ui.Text(c, trMessage(text)).FontSize(12).LineHeight(1.4).TextColor(c.Theme().TextMuted)
}

// settingsCardDivider separates the rows of a card. The card's own border
// already outlines the group, so the lines between rows sit most of the way
// toward the card's background and leave the row names the heaviest thing in it.
func settingsCardDivider(c *ui.Context) {
	t := c.Theme()
	ui.Box(c).Height(1).Shrink(0).FillWidth().Background(t.Border.Mix(t.Surface, 0.7))
}

// settingsSection groups settings that belong together on one card, titled
// inside it: the title is the card's own first row, so a section reads as one
// thing instead of a small caption over a box.
func settingsSection(c *ui.Context, key, title string, rows ...func()) {
	t := c.Theme()
	ui.Column(c.Key(key)).FillWidth().Radius(12).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Text(c, tr(title)).FontSize(16).FontWeight(600).TextColor(t.Text).Padding(12, 16)
		settingsCardDivider(c)
		for i, row := range rows {
			if i > 0 {
				settingsCardDivider(c)
			}
			row()
		}
	})
}

// settingsDisclosure is settingsSection whose rows fold away: the title row is
// the card's header and the chevron at its end says the rows can be put away,
// so a page of long groups stays short until one of them is wanted. A pointer
// over the header lights it, as a row of the same card would.
func settingsDisclosure(c *ui.Context, key, title string, open *bool, rows ...func()) {
	t := c.Theme()
	ui.Column(c.Key(key)).FillWidth().Radius(12).Background(t.Surface).Border(1, t.Border).Children(func() {
		p := ui.CollapsibleBase(c.Key(key+"-fold"), open)
		head := p.Trigger.FillWidth().Padding(12, 16).Gap(12).AlignItems(ui.Center).
			Cursor(ui.CursorPointer).Radius(12).Transition(colorMotion())
		if head.Hovered() {
			head.Background(t.SurfaceHover)
		}
		head.Children(func() {
			ui.Text(c, tr(title)).FontSize(16).FontWeight(600).TextColor(t.Text).
				SingleLine().Grow(1).MinWidth(0)
			ui.Icon(c, iconChevronDown).Size(16, 16).TextColor(t.TextMuted).Shrink(0).
				Rotate(180 * p.Progress())
		})
		p.Panel(func() {
			settingsCardDivider(c)
			for i, row := range rows {
				if i > 0 {
					settingsCardDivider(c)
				}
				row()
			}
		})
	})
}

// settingsRow lays out one setting, the shape every row of a card has: its
// name and details on the left, its control on the right.
func settingsRow(c *ui.Context, key string, left, right func()) {
	ui.Row(c.Key(key)).FillWidth().Padding(11, 16).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(left)
		ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(8).Children(right)
	})
}

// settingsSwitch paints a switch on the row that is one, at the row's right
// edge, in the theme's colors and at the place the row reserved for it.
func settingsSwitch(p *ui.Painter, r ui.Rect, pos float32, t *ui.Theme, disabled bool) {
	const trackW, trackH, inset float32 = 34, 20, 2
	track := ui.Rect{X: r.X + r.W - 16 - trackW, Y: r.Y + (r.H-trackH)/2, W: trackW, H: trackH}
	angle := float32(0.15)
	if t.Dark {
		angle = 0.25
	}
	trackColor := t.Border.Mix(t.Text, angle).Mix(t.Accent, pos)
	knobD := trackH - 2*inset
	knob := ui.Rect{X: track.X + inset + pos*(trackW-knobD-2*inset), Y: track.Y + inset, W: knobD, H: knobD}
	shadow := ui.RGBA(0, 0, 0, 0.25)
	if disabled {
		trackColor, shadow = trackColor.Alpha(0.5), ui.RGBA(0, 0, 0, 0.1)
	}
	p.Fill(track, trackColor, trackH/2)
	p.Shadow(knob, knobD/2, 0, 1, 2, 0, shadow)
	p.Fill(knob, ui.RGB(255, 255, 255), knobD/2)
}

// settingsSwitchRow is a setting that is on or off: the whole row is the
// switch, so a click or Space anywhere on it changes the value.
func settingsSwitchRow(c *ui.Context, key, label, detail string, value *bool, disabled bool, onChange func()) {
	t := c.Theme()
	row := ui.SwitchBase(c.Key(key), value).Label(tr(label)).FillWidth().
		Padding(11, 16).Gap(12).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Disabled(disabled).Transition(colorMotion())
	if row.Hovered() && !disabled {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Padding(0, 46, 0, 0).Children(func() {
			settingsName(c, label)
			if detail != "" {
				settingsDetail(c, detail)
			}
		})
	})
	pos := row.AnimateTo("switch", boolFloat(*value), ui.Animation{Duration: motionColor, Ease: ui.EaseOut})
	row.DrawOver(func(p *ui.Painter, r ui.Rect) { settingsSwitch(p, r, pos, t, disabled) })
	if onChange != nil {
		row.OnClick(onChange)
	}
}

// settingsChoice is a setting's menu: the row names the setting, and the menu
// shows the value chosen for it.
func settingsChoice(c *ui.Context, key, label, value string, options []configOption, choose func(string)) ui.Element {
	optionLabel := func(label string) string {
		if label == "关闭" && englishUI.Load() {
			return "Off"
		}
		return tr(label)
	}
	selected := value
	for _, o := range options {
		if o.value == value {
			selected = optionLabel(o.label)
		}
	}
	if selected == "" {
		selected = tr("未设置")
	}
	btn := ui.MenuButton(c.Key(key), selected, func(m *ui.Menu) {
		for _, o := range options {
			if m.Item(optionLabel(o.label)).Checked(value == o.value).Chosen() {
				choose(o.value)
			}
		}
	}).Label(tr(label)).Shrink(0).Width(settingsControlWidth(c))
	return btn
}

// settingsChoiceRow is a setting chosen from a menu rather than typed.
func settingsChoiceRow(c *ui.Context, key, label, detail, value string, options []configOption, choose func(string)) {
	settingsRow(c, key, func() {
		settingsName(c, label)
		if detail != "" {
			settingsDetail(c, detail)
		}
	}, func() {
		settingsChoice(c, key+"-menu", label, value, options, choose)
	})
}

func boolFloat(v bool) float32 {
	if v {
		return 1
	}
	return 0
}
