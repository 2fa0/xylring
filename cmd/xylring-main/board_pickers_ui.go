package main

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

func boardStatusColor(c *ui.Context, s boardStatus) ui.Color {
	t := c.Theme()
	switch s {
	case boardTodo:
		return t.TextMuted
	case boardDoing:
		return t.Accent
	case boardReview:
		return t.Warning
	case boardDone:
		return t.Success
	default:
		return t.TextMuted
	}
}

func boardPriorityColor(c *ui.Context, p boardPriority) ui.Color {
	t := c.Theme()
	switch p {
	case boardPriorityUrgentImportant:
		return t.Danger
	case boardPriorityImportant:
		return t.Accent
	case boardPriorityUrgent:
		return t.Warning
	default:
		return t.TextMuted
	}
}

func boardPriorityPill(c *ui.Context, key string, p boardPriority, fontSize float32) ui.Element {
	col := boardPriorityColor(c, p)
	pill := ui.Row(c.Key(key)).Padding(2, 7).Gap(5).Radius(4).
		Background(col.Alpha(0.12)).AlignItems(ui.Center).Shrink(0)
	pill.Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(col).Shrink(0)
		lbl := ui.Text(c, tr(boardPriorityLabel(p))).FontSize(fontSize).TextColor(col).SingleLine()
		if p == boardPriorityUrgentImportant {
			lbl.FontWeight(600)
		} else {
			lbl.FontWeight(500)
		}
	})
	return pill
}

func sameBoardDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func boardCardDueAppearance(c *ui.Context, rawDue string, parsed time.Time, state boardDueState) (text, tip string, fg, bg ui.Color) {
	t := c.Theme()
	now := time.Now()
	if sameBoardDay(parsed, now) {
		text = tr("今天")
	} else if parsed.Year() == now.Year() {
		text = parsed.Format("01-02")
	} else {
		text = parsed.Format("2006-01-02")
	}
	tip = rawDue
	switch state {
	case boardDueOverdue:
		fg = t.Danger
		bg = t.Danger.Alpha(0.12)
		tip = rawDue + " · " + tr("已逾期")
	case boardDueToday:
		fg = t.Warning
		bg = t.Warning.Alpha(0.14)
		tip = rawDue + " · " + tr("今天截止")
	default:
		fg = t.TextMuted
		bg = t.TextMuted.Alpha(0.10)
	}
	return text, tip, fg, bg
}

func boardPriorityPicker(c *ui.Context, key string, current boardPriority, open *bool, fillWidth bool, onChange func(boardPriority)) {
	t := c.Theme()
	label := tr("请选择")
	if current != boardPriorityNone {
		label = tr(boardPriorityLabel(current))
	}
	btn := ui.ButtonBase(c.Key(key)).
		Label(label).
		Height(30).Padding(0, 10).Gap(6).Radius(t.Radius).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Transition(colorMotion())
	if fillWidth {
		btn.FillWidth()
	} else {
		btn.MinWidth(180)
	}
	switch {
	case *open:
		btn.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
	case btn.Pressed():
		btn.Background(t.SurfacePressed).Border(1, t.Border)
	case btn.Hovered():
		btn.Background(t.SurfaceHover).Border(1, t.Border)
	default:
		btn.Background(t.Surface.Alpha(0.6)).Border(1, t.Border.Alpha(0.7))
	}
	btn.Children(func() {
		if current != boardPriorityNone {
			col := boardPriorityColor(c, current)
			ui.Box(c).Size(7, 7).Radius(3.5).Background(col).Shrink(0)
			lbl := ui.Text(c, tr(boardPriorityLabel(current))).FontSize(13.5).TextColor(col).FontWeight(500).SingleLine()
			if current == boardPriorityUrgentImportant {
				lbl.FontWeight(600)
			}
		} else {
			ui.Icon(c, iconFlag).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, tr("请选择")).FontSize(13.5).TextColor(t.TextMuted).SingleLine()
		}
		ui.Box(c).Grow(1).MinWidth(4)
		ui.Icon(c, iconChevronDown).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
	})
	if btn.Clicked() {
		*open = !*open
	}
	ui.PopoverBase(c, btn, open, func(panel ui.Element) {
		panel.MinWidth(max(btn.Bounds().W, 180)).Margin(4, 0, 0, 0).Padding(4).Gap(2).
			Radius(t.Radius+2).Background(t.Background).Border(1, t.Border).
			Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
		for _, opt := range boardPriorities {
			p := opt
			selected := current == p
			col := boardPriorityColor(c, p)
			row := ui.ButtonBase(c.Key(key+"-opt-"+string(p))).
				Label(tr(boardPriorityLabel(p))).
				FillWidth().Padding(6, 8).Gap(8).Radius(t.Radius).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Transition(colorMotion())
			switch {
			case row.Pressed():
				row.Background(t.SurfacePressed)
			case row.Hovered() || selected:
				row.Background(t.SurfaceHover)
			}
			row.Children(func() {
				pill := boardPriorityPill(c, key+"-opt-pill-"+string(p), p, 13)
				if selected {
					pill.Background(col.Alpha(0.18))
				}
				ui.Box(c).Grow(1)
				if selected {
					ui.Icon(c, iconCheck).Size(13, 13).TextColor(t.Accent).Shrink(0)
				}
			})
			if row.Clicked() {
				*open = false
				if current != p {
					onChange(p)
				}
			}
		}
		if current != boardPriorityNone {
			ui.Box(c).FillWidth().Height(1).Margin(2, 0).Background(t.Border.Alpha(0.6))
			clearBtn := ui.ButtonBase(c.Key(key+"-clear")).
				Label(tr("清除")).
				FillWidth().Padding(6, 8).Gap(6).Radius(t.Radius).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Transition(colorMotion())
			if clearBtn.Hovered() {
				clearBtn.Background(t.SurfaceHover)
			}
			clearBtn.Children(func() {
				ui.Icon(c, iconClose).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, tr("清除")).FontSize(13).TextColor(t.TextMuted).SingleLine()
			})
			if clearBtn.Clicked() {
				*open = false
				onChange(boardPriorityNone)
			}
		}
	})
}

const (
	boardCalModeDate = iota
	boardCalModeMonth
	boardCalModeYear
)

func boardDueDatePicker(c *ui.Context, key, current string, doneOrArchived bool, open *bool, pickerDate *time.Time, fillWidth bool, onChange func(string)) {
	t := c.Theme()
	now := time.Now()
	parsed, dueState, hasDue := boardTaskDueInfo(current, doneOrArchived, now)
	label := tr("请选择日期")
	fg := t.TextMuted
	iconFg := t.TextMuted
	if hasDue {
		label = current
		switch dueState {
		case boardDueOverdue:
			fg = t.Danger
			iconFg = t.Danger
		case boardDueToday:
			fg = t.Warning
			iconFg = t.Warning
		default:
			fg = t.Text
		}
	}

	wrap := ui.Row(c.Key(key + "-wrap")).AlignItems(ui.Center)
	if fillWidth {
		wrap.FillWidth()
	}
	wrap.Children(func() {
		btn := ui.ButtonBase(c.Key(key)).
			Label(label).
			Height(30).Padding(0, 10).Gap(6).Radius(t.Radius).
			AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Transition(colorMotion())
		if fillWidth {
			btn.FillWidth()
		} else {
			btn.MinWidth(180)
		}
		switch {
		case *open:
			btn.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
		case btn.Pressed():
			btn.Background(t.SurfacePressed).Border(1, t.Border)
		case btn.Hovered():
			btn.Background(t.SurfaceHover).Border(1, t.Border)
		case dueState == boardDueOverdue:
			btn.Background(t.Danger.Alpha(0.06)).Border(1, t.Danger.Alpha(0.36))
		case dueState == boardDueToday:
			btn.Background(t.Warning.Alpha(0.07)).Border(1, t.Warning.Alpha(0.38))
		default:
			btn.Background(t.Surface.Alpha(0.6)).Border(1, t.Border.Alpha(0.7))
		}
		btn.Children(func() {
			ui.Icon(c, iconCalendar).Size(13, 13).TextColor(iconFg).Shrink(0)
			lbl := ui.Text(c, label).FontSize(13.5).TextColor(fg).SingleLine()
			if hasDue {
				lbl.FontFeatures("tnum")
				if dueState == boardDueOverdue || dueState == boardDueToday {
					lbl.FontWeight(600)
				} else {
					lbl.FontWeight(500)
				}
			}
			if hasDue && dueState == boardDueToday {
				boardDueStatusChip(c, tr("今天"), t.Warning)
			} else if hasDue && dueState == boardDueOverdue {
				boardDueStatusChip(c, tr("已逾期"), t.Danger)
			}
			ui.Box(c).Grow(1).MinWidth(4)
			if hasDue {
				// Reserve room for the overlaid inline clear button so text never collides.
				ui.Box(c).Size(18, 18).Shrink(0)
			} else {
				ui.Icon(c, iconChevronDown).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
			}
		})
		if btn.Clicked() {
			*open = !*open
			if *open {
				if hasDue {
					*pickerDate = parsed
				} else {
					*pickerDate = now
				}
			}
		}
		if hasDue {
			clearBtn := ui.ButtonBase(c.Key(key+"-clear-inline")).
				Label(tr("清除截止日期")).Tooltip(tr("清除截止日期")).
				Absolute().Right(6).Top(5).Size(20, 20).Radius(10).Center().
				Cursor(ui.CursorPointer).Transition(colorMotion())
			clearFg := t.TextMuted
			switch {
			case clearBtn.Pressed():
				clearBtn.Background(t.SurfacePressed)
				clearFg = t.Text
			case clearBtn.Hovered():
				clearBtn.Background(t.SurfaceHover)
				clearFg = t.Text
			}
			clearBtn.Children(func() {
				ui.Icon(c, iconClose).Size(11, 11).TextColor(clearFg)
			})
			if clearBtn.Clicked() {
				*open = false
				onChange("")
			}
		}
		ui.PopoverBase(c, btn, open, func(panel ui.Element) {
			boardDueCalendarPopover(c, panel, key, current, parsed, hasDue, now, open, pickerDate, onChange)
		})
	})
}

func boardDueStatusChip(c *ui.Context, text string, col ui.Color) {
	ui.Row(c).Padding(1, 5).Radius(4).Background(col.Alpha(0.14)).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(11).FontWeight(600).TextColor(col).SingleLine()
	})
}

func boardDueCalendarPopover(c *ui.Context, panel ui.Element, key, current string, parsed time.Time, hasDue bool, now time.Time, open *bool, pickerDate *time.Time, onChange func(string)) {
	t := c.Theme()
	panel.Width(276).Margin(4, 0).Padding(0).Gap(0).
		Radius(10).Background(t.Background).Border(1, t.Border).Clip().
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.20))

	if pickerDate.IsZero() {
		*pickerDate = now
	}
	calMode := ui.Local(panel, "cal-mode", func() int { return boardCalModeDate })

	if *calMode == boardCalModeDate {
		if panel.Shortcut(0, ui.KeyPageUp) {
			*pickerDate = time.Date(pickerDate.Year(), pickerDate.Month()-1, 1, 0, 0, 0, 0, time.Local)
		}
		if panel.Shortcut(0, ui.KeyPageDown) {
			*pickerDate = time.Date(pickerDate.Year(), pickerDate.Month()+1, 1, 0, 0, 0, 0, time.Local)
		}
	}

	pickDate := func(target time.Time) {
		*pickerDate = target
		*open = false
		next := target.Format("2006-01-02")
		if next != current {
			onChange(next)
		}
	}

	// Quick preset bar
	ui.Row(c).FillWidth().Padding(10, 12, 8, 12).Gap(6).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border.Alpha(0.55)).Children(func() {
		quickChip := func(id, text string, target time.Time) {
			active := hasDue && current == target.Format("2006-01-02")
			b := ui.ButtonBase(c.Key(key+"-"+id)).
				Label(tr(text)).Height(24).Padding(0, 9).Radius(12).Center().
				Cursor(ui.CursorPointer).Transition(colorMotion())
			fg := t.Text
			switch {
			case active:
				b.Background(t.Accent.Alpha(0.14)).Border(1, t.Accent.Alpha(0.45))
				fg = t.Accent
			case b.Pressed():
				b.Background(t.SurfacePressed).Border(1, t.Border)
			case b.Hovered():
				b.Background(t.SurfaceHover).Border(1, t.Border)
			default:
				b.Background(t.Surface.Alpha(0.8)).Border(1, t.Border.Alpha(0.55))
			}
			b.Children(func() {
				lbl := ui.Text(c, tr(text)).FontSize(12).TextColor(fg).SingleLine()
				if active {
					lbl.FontWeight(600)
				} else {
					lbl.FontWeight(500)
				}
			})
			if b.Clicked() {
				pickDate(target)
			}
		}
		quickChip("today", "今天", now)
		quickChip("tomorrow", "明天", now.AddDate(0, 0, 1))
		quickChip("nextweek", "下周", now.AddDate(0, 0, 7))
		ui.Box(c).Grow(1)
		if hasDue {
			clr := ui.ButtonBase(c.Key(key+"-clear")).
				Label(tr("清除")).Height(24).Padding(0, 7).Radius(6).Center().
				Cursor(ui.CursorPointer).Transition(colorMotion())
			clrFg := t.TextMuted
			switch {
			case clr.Pressed():
				clr.Background(t.Danger.Alpha(0.14))
				clrFg = t.Danger
			case clr.Hovered():
				clr.Background(t.Danger.Alpha(0.08))
				clrFg = t.Danger
			}
			clr.Children(func() {
				ui.Text(c, tr("清除")).FontSize(12).FontWeight(500).TextColor(clrFg).SingleLine()
			})
			if clr.Clicked() {
				*open = false
				onChange("")
			}
		}
	})

	locale, firstWeekday := boardCalendarLocale(c)
	view := time.Date(pickerDate.Year(), pickerDate.Month(), 1, 0, 0, 0, 0, time.Local)

	boardCalendarHeader(c, key, locale, view, calMode, pickerDate)

	ui.Column(c).FillWidth().Padding(2, 12, 12, 12).Gap(2).Children(func() {
		switch *calMode {
		case boardCalModeMonth:
			boardCalendarMonthGrid(c, key, locale, view, parsed, hasDue, calMode, pickerDate)
		case boardCalModeYear:
			boardCalendarYearGrid(c, key, view, parsed, hasDue, calMode, pickerDate)
		default:
			boardCalendarDayGrid(c, key, locale, firstWeekday, view, parsed, hasDue, now, pickDate)
		}
	})
}

func boardCalendarLocale(c *ui.Context) (locale string, firstWeekday time.Weekday) {
	lang := c.Locale()
	if lang == "" {
		if englishUI.Load() {
			lang = "en"
		} else {
			lang = "zh-Hans"
		}
	}
	region := c.Region()
	if region.HourCycle != 12 && region.HourCycle != 24 {
		region = i18n.RegionFor(lang)
	}
	return region.FormatLocale(lang), region.FirstWeekday
}

func boardCalendarNavButton(c *ui.Context, key, label string, icon *ui.SVG, onClick func()) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label).
		Size(26, 26).Radius(13).Center().Shrink(0).
		Cursor(ui.CursorPointer).Transition(colorMotion())
	fg := t.TextMuted
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
		fg = t.Text
	case b.Hovered():
		b.Background(t.SurfaceHover)
		fg = t.Text
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(fg)
	})
	if b.Clicked() {
		onClick()
	}
}

func boardCalendarTitleButton(c *ui.Context, key, text, tip string, onClick func()) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Label(text).Tooltip(tip).
		Padding(3, 6).Radius(6).Center().
		Cursor(ui.CursorPointer).Transition(colorMotion())
	fg := t.Text
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
		fg = t.Accent
	case b.Hovered():
		b.Background(t.SurfaceHover)
		fg = t.Accent
	}
	b.Children(func() {
		ui.Text(c, text).FontSize(13.5).FontWeight(600).FontFeatures("tnum").TextColor(fg).SingleLine()
	})
	if b.Clicked() {
		onClick()
	}
}

func boardCalendarHeader(c *ui.Context, key, locale string, view time.Time, calMode *int, pickerDate *time.Time) {
	t := c.Theme()
	prevTip, nextTip := tr("上个月"), tr("下个月")
	step := func(delta int) {
		switch *calMode {
		case boardCalModeMonth:
			*pickerDate = view.AddDate(delta, 0, 0)
		case boardCalModeYear:
			*pickerDate = view.AddDate(delta*10, 0, 0)
		default:
			*pickerDate = view.AddDate(0, delta, 0)
		}
	}
	switch *calMode {
	case boardCalModeMonth:
		prevTip, nextTip = tr("上一年"), tr("下一年")
	case boardCalModeYear:
		prevTip, nextTip = tr("上一年代"), tr("下一年代")
	}

	ui.Row(c).FillWidth().Padding(10, 12, 4, 12).Gap(4).AlignItems(ui.Center).Children(func() {
		boardCalendarNavButton(c, key+"-prev", prevTip, iconChevronLeft, func() { step(-1) })

		ui.Row(c).Grow(1).MinWidth(0).Gap(2).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			yearLabel := strconv.Itoa(view.Year())
			if !englishUI.Load() {
				yearLabel = fmt.Sprintf("%d年", view.Year())
			}
			monthWidth := i18n.Abbreviated
			if englishUI.Load() {
				monthWidth = i18n.Wide
			}
			monthNames := i18n.Months(locale, monthWidth, true)
			monthLabel := monthNames[view.Month()-1]

			switch *calMode {
			case boardCalModeDate:
				if englishUI.Load() {
					boardCalendarTitleButton(c, key+"-mode-month", monthLabel, tr("选择月份"), func() { *calMode = boardCalModeMonth })
					boardCalendarTitleButton(c, key+"-mode-year", yearLabel, tr("选择年份"), func() { *calMode = boardCalModeYear })
				} else {
					boardCalendarTitleButton(c, key+"-mode-year", yearLabel, tr("选择年份"), func() { *calMode = boardCalModeYear })
					boardCalendarTitleButton(c, key+"-mode-month", monthLabel, tr("选择月份"), func() { *calMode = boardCalModeMonth })
				}
			case boardCalModeMonth:
				boardCalendarTitleButton(c, key+"-mode-year", yearLabel, tr("选择年份"), func() { *calMode = boardCalModeYear })
			case boardCalModeYear:
				decadeStart := (view.Year() / 10) * 10
				ui.Text(c, fmt.Sprintf("%d - %d", decadeStart, decadeStart+9)).
					FontSize(13.5).FontWeight(600).FontFeatures("tnum").TextColor(t.Text).Padding(3, 6).SingleLine()
			}
		})

		boardCalendarNavButton(c, key+"-next", nextTip, iconChevronRight, func() { step(1) })
	})
}

func boardCalendarDayGrid(c *ui.Context, key, locale string, firstWeekday time.Weekday, view, parsed time.Time, hasDue bool, now time.Time, pickDate func(time.Time)) {
	t := c.Theme()
	weekdays := i18n.Weekdays(locale, i18n.Short, true)
	ui.Row(c).FillWidth().Children(func() {
		for i := range 7 {
			name := weekdays[(int(firstWeekday)+i)%7]
			ui.Text(c, name).Width(36).Height(24).TextAlign(ui.Center).
				FontSize(11.5).FontWeight(500).TextColor(t.TextMuted.Alpha(0.78)).
				LineHeight(24.0 / 11.5).SingleLine()
		}
	})

	startOffset := (int(view.Weekday()) - int(firstWeekday) + 7) % 7
	gridStart := view.AddDate(0, 0, -startOffset)
	for w := range 6 {
		ui.Row(c.Key(fmt.Sprintf("%s-w-%d", key, w))).FillWidth().Children(func() {
			for d := range 7 {
				day := gridStart.AddDate(0, 0, w*7+d)
				inMonth := day.Month() == view.Month() && day.Year() == view.Year()
				isToday := sameBoardDay(day, now)
				isSelected := hasDue && sameBoardDay(day, parsed)

				dayLabel := i18n.FormatDate(locale, day, i18n.DateOptions{Skeleton: "yMMMMd"})
				ui.Box(c).Size(36, 32).Center().Children(func() {
					cell := ui.ButtonBase(c.Key(fmt.Sprintf("%s-d-%d-%d", key, w, d))).
						Label(dayLabel).
						Size(30, 30).Radius(15).Center().
						Cursor(ui.CursorPointer).Transition(colorMotion())

					fg := t.Text
					weight := 400
					switch {
					case isSelected:
						fg = t.AccentText
						weight = 600
						if cell.Pressed() && t.AccentPressed != ui.Transparent {
							cell.Background(t.AccentPressed)
						} else if cell.Hovered() && t.AccentHover != ui.Transparent {
							cell.Background(t.AccentHover)
						} else {
							cell.Background(t.Accent)
						}
					case isToday:
						fg = t.Accent
						weight = 600
						switch {
						case cell.Pressed():
							cell.Background(t.Accent.Alpha(0.22))
						case cell.Hovered():
							cell.Background(t.Accent.Alpha(0.16))
						default:
							cell.Background(t.Accent.Alpha(0.10))
						}
					case inMonth:
						switch {
						case cell.Pressed():
							cell.Background(t.SurfacePressed)
						case cell.Hovered():
							cell.Background(t.SurfaceHover)
						}
					default:
						fg = t.TextMuted.Alpha(0.38)
						switch {
						case cell.Pressed():
							cell.Background(t.SurfacePressed.Alpha(0.6))
							fg = t.TextMuted
						case cell.Hovered():
							cell.Background(t.SurfaceHover.Alpha(0.6))
							fg = t.TextMuted
						}
					}

					cell.Children(func() {
						ui.Text(c, strconv.Itoa(day.Day())).
							FontSize(12.5).FontWeight(weight).FontFeatures("tnum").
							TextColor(fg).SingleLine()
						if isToday && !isSelected {
							ui.Box(c).Absolute().Bottom(3).Size(3, 3).Radius(1.5).Background(t.Accent)
						}
					})
					if cell.Clicked() {
						pickDate(day)
					}
				})
			}
		})
	}
}

func boardCalendarSubGrid(c *ui.Context, key string, count int, cellAt func(i int) (label string, inScope, selected bool, onPick func())) {
	t := c.Theme()
	for r := range (count + 2) / 3 {
		ui.Row(c.Key(fmt.Sprintf("%s-r-%d", key, r))).FillWidth().Children(func() {
			for col := range 3 {
				i := r*3 + col
				if i >= count {
					break
				}
				label, inScope, selected, onPick := cellAt(i)
				ui.Box(c).Size(84, 42).Center().Children(func() {
					b := ui.ButtonBase(c.Key(fmt.Sprintf("%s-c-%d", key, i))).
						Label(label).Size(76, 32).Radius(16).Center().
						Cursor(ui.CursorPointer).Transition(colorMotion())
					fg := t.Text
					weight := 400
					if !inScope {
						fg = t.TextMuted.Alpha(0.45)
					}
					switch {
					case selected:
						fg = t.AccentText
						weight = 600
						if b.Hovered() && t.AccentHover != ui.Transparent {
							b.Background(t.AccentHover)
						} else {
							b.Background(t.Accent)
						}
					case b.Pressed():
						b.Background(t.SurfacePressed)
					case b.Hovered():
						b.Background(t.SurfaceHover)
						if !inScope {
							fg = t.TextMuted
						}
					}
					b.Children(func() {
						ui.Text(c, label).FontSize(13).FontWeight(weight).FontFeatures("tnum").TextColor(fg).SingleLine()
					})
					if b.Clicked() {
						onPick()
					}
				})
			}
		})
	}
}

func boardCalendarMonthGrid(c *ui.Context, key, locale string, view, parsed time.Time, hasDue bool, calMode *int, pickerDate *time.Time) {
	months := i18n.Months(locale, i18n.Abbreviated, true)
	boardCalendarSubGrid(c, key+"-months", 12, func(i int) (string, bool, bool, func()) {
		m := time.Month(i + 1)
		selected := (hasDue && parsed.Year() == view.Year() && parsed.Month() == m) ||
			(!hasDue && view.Month() == m)
		return months[i], true, selected, func() {
			*pickerDate = time.Date(view.Year(), m, 1, 0, 0, 0, 0, time.Local)
			*calMode = boardCalModeDate
		}
	})
}

func boardCalendarYearGrid(c *ui.Context, key string, view, parsed time.Time, hasDue bool, calMode *int, pickerDate *time.Time) {
	start := (view.Year()/10)*10 - 1
	boardCalendarSubGrid(c, key+"-years", 12, func(i int) (string, bool, bool, func()) {
		y := start + i
		inDecade := i > 0 && i < 11
		selected := (hasDue && parsed.Year() == y) || (!hasDue && view.Year() == y)
		return strconv.Itoa(y), inDecade, selected, func() {
			*pickerDate = time.Date(y, view.Month(), 1, 0, 0, 0, 0, time.Local)
			*calMode = boardCalModeDate
		}
	})
}

func boardRepoColor(c *ui.Context, repo string) ui.Color {
	t := c.Theme()
	palette := []ui.Color{
		t.Accent,
		t.Success,
		t.Warning,
		t.Danger,
		ui.RGB(147, 51, 234),
		ui.RGB(13, 148, 136),
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(repoTitle(repo))))
	return palette[h.Sum32()%uint32(len(palette))]
}
