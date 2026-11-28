package app

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/2fa0/xylring/internal/radar"
	"github.com/egoist/mygo/ui"
)

type radarState struct {
	loaded        bool
	polling       bool
	snapshot      radar.Snapshot
	filter        string // "" (all), "attention", "running"
	search        string
	searchInput   ui.Handle
	showHookModal bool
	hookTab       int // 0: Codex, 1: Claude Code, 2: Shell
	loadErr       string
	scanFn        func(context.Context, radar.ScanOptions) radar.Snapshot
}

func (a *app) openRadar() {
	a.leaveBoard()
	a.showHome = false
	a.showRadar = true
	a.pollRadar(true)
}

func (a *app) leaveRadar() {
	a.showRadar = false
	a.radar.showHookModal = false
}

func (a *app) radarModalOpen() bool {
	return a.radar.showHookModal
}

func (a *app) pollRadar(force bool) {
	if a.closed || a.radar.polling {
		return
	}
	if !force && !a.showRadar && a.win == nil && a.dispatch == nil && a.radar.scanFn == nil {
		return
	}
	a.radar.polling = true

	if a.radar.scanFn != nil {
		opts := radar.ScanOptions{
			Now:           time.Now().UTC(),
			KnownRepos:    a.radarCandidateRepos(),
			CanonicalPath: canonicalRepoDir,
			SkipTmux:      true,
			SkipCodex:     true,
		}
		snap := a.radar.scanFn(context.Background(), opts)
		a.radar.polling = false
		a.radar.loaded = true
		a.radar.loadErr = ""
		a.radar.snapshot = snap
		return
	}

	candidates := a.radarCandidateRepos()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		hints := collectRadarWorktreeHints(ctx, candidates)
		snap := radar.CollectSnapshot(ctx, radar.ScanOptions{
			Now:           time.Now().UTC(),
			KnownRepos:    candidates,
			Worktrees:     hints,
			CanonicalPath: canonicalRepoDir,
		})
		a.post(func() {
			a.radar.polling = false
			if a.closed {
				return
			}
			a.radar.loaded = true
			a.radar.loadErr = ""
			a.radar.snapshot = snap
		})
	}()
}

func (a *app) radarCandidateRepos() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || isDockerRepoPath(p) {
			return
		}
		norm := canonicalRepoDir(p)
		if norm == "" || seen[norm] {
			return
		}
		seen[norm] = true
		out = append(out, norm)
	}
	for _, p := range a.repoItems() {
		add(p)
	}
	for _, tab := range a.tabs {
		add(tab.path)
	}
	a.summaryMu.Lock()
	for repo, sum := range a.repoSummaries {
		add(repo)
		for _, wt := range sum.worktrees {
			add(wt.path)
		}
	}
	a.summaryMu.Unlock()
	for i, p := range a.recent {
		if i >= 12 {
			break
		}
		add(p)
	}
	return out
}

func collectRadarWorktreeHints(ctx context.Context, repos []string) []radar.WorktreeHint {
	if len(repos) == 0 {
		return nil
	}
	var hints []radar.WorktreeHint
	seen := map[string]bool{}
	for _, dir := range repos {
		if ctx.Err() != nil {
			break
		}
		norm := canonicalRepoDir(dir)
		if norm == "" || seen[norm] || isDockerRepoPath(norm) {
			continue
		}
		info, err := os.Stat(norm)
		if err != nil || !info.IsDir() || !isGitWorkTree(norm) {
			continue
		}
		seen[norm] = true

		hint := radar.WorktreeHint{
			Path:      norm,
			UpdatedAt: info.ModTime().UTC(),
		}
		if commonOut, cerr := runGit(ctx, norm, "rev-parse", "--path-format=absolute", "--git-common-dir"); cerr == nil {
			hint.CommonDir = canonicalRepoDir(strings.TrimSpace(string(commonOut)))
		}
		if branchOut, berr := runGit(ctx, norm, "symbolic-ref", "-q", "--short", "HEAD"); berr == nil {
			hint.Branch = strings.TrimSpace(string(branchOut))
		}
		if stOut, serr := runGit(ctx, norm, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=normal"); serr == nil && len(stOut) > 0 {
			changes := parsePorcelainZ(stOut)
			for _, ch := range changes {
				hint.DirtyFiles = append(hint.DirtyFiles, ch.path)
			}
		}
		hints = append(hints, hint)
	}
	return hints
}

func (a *app) focusRadarSession(s radar.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := radar.FocusSession(ctx, s, a.openInEditor, openInTerminal)
	if err != nil {
		a.setBanner(trimMsg(err.Error()), s.Repo)
	}
}

func (a *app) dismissRadarSession(s radar.Session) {
	ts := s.UpdatedAt
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	if err := radar.DismissSession(s.ID, ts); err != nil {
		a.setBanner(trimMsg(err.Error()), s.Repo)
		return
	}
	a.radar.snapshot.Sessions = slices.DeleteFunc(slices.Clone(a.radar.snapshot.Sessions), func(item radar.Session) bool {
		return item.ID == s.ID
	})
	var att, run int
	for _, item := range a.radar.snapshot.Sessions {
		if item.State.NeedsAttention() {
			att++
		} else if item.State == radar.StateRunning {
			run++
		}
	}
	a.radar.snapshot.AttentionCount = att
	a.radar.snapshot.RunningCount = run
	a.pollRadar(true)
}

func (a *app) sidebarRadarRow(c *ui.Context) {
	t := c.Theme()
	active := a.showRadar
	row := ui.ButtonBase(c.Key("sidebar-radar-nav")).
		Label(tr("Agent 雷达")).
		FillWidth().
		Padding(9, 12).
		Gap(9).
		Radius(8).
		Justify(ui.Start).
		AlignItems(ui.Center).
		Cursor(ui.CursorPointer).
		Margin(0, 0, 6, 0).
		Transition(colorMotion())
	switch {
	case active:
		row.Background(t.Accent.Alpha(0.14)).Border(1, t.Accent.Alpha(0.4))
	case row.Pressed():
		row.Background(t.SurfacePressed).Border(1, t.Border.Alpha(0.7))
	case row.Hovered():
		row.Background(t.SurfaceHover).Border(1, t.Border.Alpha(0.7))
	default:
		if t.Dark {
			row.Background(ui.RGBA(255, 255, 255, 0.04)).Border(1, t.Border.Alpha(0.5)).
				InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.06))
		} else {
			row.Background(ui.RGBA(255, 255, 255, 0.78)).Border(1, t.Border.Alpha(0.55)).
				Shadow(0, 1, 2, 0, ui.RGBA(15, 23, 42, 0.05))
		}
	}
	row.Children(func() {
		fg := t.TextMuted
		if active {
			fg = t.Accent
		}
		ui.Icon(c, iconRadar).Size(16, 16).TextColor(fg).Shrink(0)
		lbl := ui.Text(c, tr("Agent 雷达")).FontSize(13.5).Grow(1).MinWidth(0).SingleLine()
		if active {
			lbl.FontWeight(600).TextColor(t.Accent)
		} else {
			lbl.FontWeight(500).TextColor(t.Text)
		}
		if !active {
			att := a.radar.snapshot.AttentionCount
			run := a.radar.snapshot.RunningCount
			if att > 0 {
				ui.Row(c).Padding(1, 7).Radius(10).Background(t.Warning.Alpha(0.16)).AlignItems(ui.Center).Shrink(0).Children(func() {
					ui.Text(c, fmt.Sprintf("%d", att)).FontSize(11.5).FontWeight(600).FontFeatures("tnum").TextColor(t.Warning).SingleLine()
				})
			} else if run > 0 {
				ui.Row(c).Padding(1, 7).Radius(10).Background(t.SurfaceHover).AlignItems(ui.Center).Shrink(0).Children(func() {
					ui.Text(c, fmt.Sprintf("%d", run)).FontSize(11.5).FontWeight(600).FontFeatures("tnum").TextColor(t.TextMuted).SingleLine()
				})
			}
		}
	})
	if row.Clicked() {
		a.openRadar()
	}
}

func (a *app) radarChromeToolbar(c *ui.Context, padR, rightPadL, rightW float32) {
	t := c.Theme()
	ui.Row(c.Key("radar-chrome-toolbar")).Fill().Padding(0, padR, 0, rightPadL).Gap(8).AlignItems(ui.Center).
		Transition(fadeEnter()).Children(func() {
		maxW := float32(320)
		if rightW < 620 {
			maxW = 200
		}
		searchBox := ui.Row(c.Key("radar-search-box")).Grow(1).MinWidth(130).MaxWidth(maxW).Height(30).
			Padding(0, 8).Gap(6).AlignItems(ui.Center).Radius(t.Radius).Background(t.Background).
			Transition(colorMotion())
		var input ui.Element
		searchBox.Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
			input = ui.TextInputBase(c.Key("radar-search"), &a.radar.search).
				Label(tr("搜索会话")).
				Placeholder(tr("搜索会话…")).
				Bind(&a.radar.searchInput).
				FontSize(13).
				Grow(1).MinWidth(0).FocusRing(false)
			if a.radar.search != "" {
				if closeIconButton(c, "radar-search-clear", tr("清空搜索输入")).Clicked() {
					a.radar.search = ""
					input.Focus()
				}
			} else if !input.Focused() && rightW >= 540 {
				shortcut := "Ctrl+F"
				if runtime.GOOS == "darwin" {
					shortcut = "⌘F"
				}
				kbdHint(c, "radar-search-shortcut", shortcut)
			}
		})
		border := t.Border
		if searchBox.Hovered() {
			border = t.TextMuted
		}
		if input.Focused() {
			border = t.Accent
			searchBox.Shadow(0, 0, 0, 2, t.Accent.Alpha(0.15))
		}
		searchBox.Border(1, border)

		ui.Box(c).Grow(1).MinWidth(4)

		hookBtn := ui.ButtonBase(c.Key("radar-hook-btn")).Height(30).Padding(0, 9).Gap(5).
			Radius(t.Radius).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Label(tr("接入 Hook")).Tooltip(tr("接入 Agent Hook")).Shrink(0).Transition(colorMotion())
		switch {
		case hookBtn.Pressed():
			hookBtn.Background(t.SurfacePressed)
		case hookBtn.Hovered():
			hookBtn.Background(t.SurfaceHover)
		}
		hookBtn.Children(func() {
			ui.Icon(c, iconBot).Size(15, 15).TextColor(t.TextMuted).Shrink(0)
			if rightW >= 580 {
				ui.Text(c, tr("接入 Hook")).FontSize(13).TextColor(t.TextMuted).SingleLine()
			}
		})
		if hookBtn.Clicked() {
			a.radar.showHookModal = true
		}

		refreshBtn := ui.ButtonBase(c.Key("radar-refresh-btn")).Label(tr("刷新会话")).Tooltip(tr("刷新会话"))
		styleActionIcon(c, refreshBtn, iconRefreshCw, 30, 15, t.TextMuted, a.radar.polling)
		if refreshBtn.Clicked() {
			a.pollRadar(true)
		}
	})
}

func (a *app) radarScreen(c *ui.Context) {
	t := c.Theme()
	canvasBg := ui.Hex("#F4F3EF")
	if t.Dark {
		canvasBg = ui.Hex("#111215")
	}
	tint := t.Accent
	tintAlpha := float32(0.04)
	if a.radar.snapshot.AttentionCount > 0 {
		tint = t.Warning
		tintAlpha = 0.065
		if t.Dark {
			tintAlpha = 0.09
		}
	} else if t.Dark {
		tintAlpha = 0.055
	}
	dotCol := ui.RGBA(15, 23, 42, 0.045)
	if t.Dark {
		dotCol = ui.RGBA(255, 255, 255, 0.032)
	}

	body := func() {
		ui.Column(c.Key("radar-workspace")).Grow(1).MinWidth(0).MinHeight(0).
			Background(canvasBg).
			BorderWidth(1, 0, 0, 0).BorderColor(t.Border.Alpha(0.65)).
			Draw(func(p *ui.Painter, r ui.Rect) {
				if r.W <= 0 || r.H <= 0 {
					return
				}
				p.FillRadialGradient(r, ui.RadialGradient{
					Center: ui.Point{X: 0.35, Y: 0.0},
					Radius: max(r.W*0.55, 360),
					Oklab:  true,
					Stops: []ui.GradientStop{
						{Color: tint.Alpha(tintAlpha), At: 0},
						{Color: ui.Transparent, At: 1},
					},
				}, 0)
				const pitch = float32(24)
				for y := r.Y + 12; y < r.Y+r.H; y += pitch {
					for x := r.X + 12; x < r.X+r.W; x += pitch {
						p.Fill(ui.Rect{X: x, Y: y, W: 1.5, H: 1.5}, dotCol, 0.75)
					}
				}
			}).
			Transition(workspaceMainMotion()).Children(func() {
			a.globalAlertBanner(c, nil)
			a.radarFocusBar(c)
			a.radarSessionList(c)
		})
	}
	if !a.showRepoSidebar() {
		body()
		return
	}
	if a.repoSplit <= 0 {
		a.repoSplit = defaultSidebarWidth
	}
	ui.Split(c.Key("radar-repo-split"), &a.repoSplit, func() {
		a.repoList(c)
	}, body).Grow(1).MinHeight(0)
}

func (a *app) radarFilteredSessions() (filtered []radar.Session, total, attentionCount, runningCount int) {
	q := strings.ToLower(strings.TrimSpace(a.radar.search))
	for _, s := range a.radar.snapshot.Sessions {
		if q != "" && !radarSessionMatches(s, q) {
			continue
		}
		total++
		if s.State.NeedsAttention() {
			attentionCount++
		} else if s.State == radar.StateRunning {
			runningCount++
		}
		switch a.radar.filter {
		case "attention":
			if !s.State.NeedsAttention() {
				continue
			}
		case "running":
			if s.State != radar.StateRunning {
				continue
			}
		}
		filtered = append(filtered, s)
	}
	return filtered, total, attentionCount, runningCount
}

func radarSessionMatches(s radar.Session, q string) bool {
	if strings.Contains(strings.ToLower(s.Agent), q) ||
		strings.Contains(strings.ToLower(s.Model), q) ||
		strings.Contains(strings.ToLower(s.Branch), q) ||
		strings.Contains(strings.ToLower(s.Summary), q) ||
		strings.Contains(strings.ToLower(s.Repo), q) {
		return true
	}
	return false
}

func (a *app) radarFocusBar(c *ui.Context) {
	t := c.Theme()
	_, total, attentionCount, runningCount := a.radarFilteredSessions()

	trackBg := ui.RGBA(15, 23, 42, 0.045)
	pillActiveBg := ui.Hex("#FFFFFF")
	if t.Dark {
		trackBg = ui.RGBA(255, 255, 255, 0.04)
		pillActiveBg = ui.Hex("#232630")
	}

	pill := func(id, label, filterVal string, count int, accent ui.Color) {
		active := a.radar.filter == filterVal
		btn := ui.ButtonBase(c.Key("radar-filter-"+id)).
			Label(tr(label)).
			Role(ui.RoleTab).
			AccessSelected(active).
			Height(26).Padding(0, 11).Radius(13).
			AlignItems(ui.Center).Cursor(ui.CursorPointer).Shrink(0).
			Transition(colorMotion())

		fg := t.TextMuted
		countFg := t.TextMuted.Alpha(0.75)
		switch {
		case active:
			if filterVal == "" {
				fg = t.Text
				countFg = t.TextMuted
			} else {
				fg = accent
				countFg = accent
			}
		case btn.Pressed():
			btn.Background(t.SurfacePressed.Alpha(0.6))
			fg = t.Text
		case btn.Hovered():
			btn.Background(t.SurfaceHover.Alpha(0.7))
			fg = t.Text
		}

		btn.Children(func() {
			if active {
				box := ui.Box(c.Key("radar-filter-active-pill")).Absolute().Fill().
					Radius(13).Background(pillActiveBg).
					IgnoreInput(true).ZIndex(0)
				if filterVal != "" {
					box.Border(1, accent.Alpha(0.45))
				} else {
					box.Border(1, t.Border.Alpha(0.55))
				}
			}
			ui.Row(c).AlignItems(ui.Center).Gap(6).ZIndex(1).Children(func() {
				if filterVal != "" {
					ui.Box(c).Size(7, 7).Radius(3.5).Background(accent).Shrink(0)
				}
				lbl := ui.Text(c, tr(label)).FontSize(12.5).TextColor(fg).SingleLine()
				if active {
					lbl.FontWeight(600)
				} else {
					lbl.FontWeight(500)
				}
				ui.Text(c, fmt.Sprintf("%d", count)).FontSize(11.5).FontFeatures("tnum").TextColor(countFg).SingleLine()
			})
		})
		if btn.Clicked() {
			a.radar.filter = filterVal
		}
	}

	ui.Row(c.Key("radar-focus-bar")).FillWidth().Padding(14, 20, 6, 20).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Row(c.Key("radar-filter-track")).Padding(3).Gap(2).Radius(16).
			Role(ui.RoleTabList).Label(tr("会话筛选")).
			Background(trackBg).Border(1, t.Border.Alpha(0.5)).AlignItems(ui.Center).Children(func() {
			pill("all", "全部", "", total, t.Accent)
			pill("attention", "需处理", "attention", attentionCount, t.Warning)
			pill("running", "运行中", "running", runningCount, ui.Hex("#10B981"))
		})
	})
}

func (a *app) radarSessionList(c *ui.Context) {
	sessions, _, _, _ := a.radarFilteredSessions()
	if len(sessions) == 0 {
		msg := tr("暂无活跃的 Agent 会话")
		if strings.TrimSpace(a.radar.search) != "" || a.radar.filter != "" {
			msg = tr("没有匹配的会话")
		}
		ui.Column(c.Key("radar-empty-wrap")).Grow(1).Center().Children(func() {
			emptyHint(c, "radar-empty", emptyHintProps{
				Icon:  iconRadar,
				Title: msg,
				Action: func() {
					if strings.TrimSpace(a.radar.search) != "" || a.radar.filter != "" {
						if ui.Button(c.Key("radar-clear-filter"), tr("显示全部")).Clicked() {
							a.radar.search = ""
							a.radar.filter = ""
						}
						return
					}
					if ui.PrimaryButton(c.Key("radar-empty-hook"), tr("接入 Hook…")).Clicked() {
						a.radar.showHookModal = true
					}
				},
			})
		})
		return
	}

	var attention, rest []radar.Session
	for _, s := range sessions {
		if s.State.NeedsAttention() {
			attention = append(attention, s)
		} else {
			rest = append(rest, s)
		}
	}
	splitSections := a.radar.filter == "" && len(attention) > 0 && len(rest) > 0

	ui.Scroll(c.Key("radar-scroll")).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(8, 20, 24, 20).Gap(10).Children(func() {
			if splitSections {
				ui.Text(c, tr("需处理")).FontSize(12).FontWeight(600).TextColor(c.Theme().Warning).Padding(2, 4)
				for _, s := range attention {
					a.radarSessionCard(c, s)
				}
				ui.Text(c, tr("运行中与空闲")).FontSize(12).FontWeight(600).TextColor(c.Theme().TextMuted).Padding(8, 4, 2, 4)
				for _, s := range rest {
					a.radarSessionCard(c, s)
				}
				return
			}
			for _, s := range sessions {
				a.radarSessionCard(c, s)
			}
		})
	})
}

func radarStateColor(c *ui.Context, st radar.SessionState) ui.Color {
	t := c.Theme()
	switch st {
	case radar.StateWaiting:
		return t.Warning
	case radar.StateError:
		return t.Danger
	case radar.StateReview:
		return t.Accent
	case radar.StateRunning:
		return ui.Hex("#10B981")
	default:
		return t.TextMuted
	}
}

func radarFocusTooltip(target radar.FocusTarget) string {
	switch target.Kind {
	case "tmux":
		if target.TmuxTarget != "" {
			return fmt.Sprintf(tr("跳回 tmux 窗格 %s"), target.TmuxTarget)
		}
		return tr("跳回 tmux 窗格")
	case "app":
		if target.AppName != "" {
			return fmt.Sprintf(tr("切换到 %s"), target.AppName)
		}
		return tr("切回原窗口")
	default:
		return tr("在终端中打开")
	}
}

func (a *app) radarSessionCard(c *ui.Context, s radar.Session) {
	t := c.Theme()
	stCol := radarStateColor(c, s.State)
	cardBg := ui.Hex("#FFFFFF")
	if t.Dark {
		cardBg = ui.Hex("#181A20")
	}
	borderCol := t.Border.Alpha(0.7)
	if s.State == radar.StateWaiting || s.State == radar.StateError {
		borderCol = stCol.Alpha(0.45)
	}

	title := s.Agent
	if s.Repo != "" {
		title = repoTitle(s.Repo)
	}

	metaParts := make([]string, 0, 2)
	if s.Branch != "" {
		metaParts = append(metaParts, s.Branch)
	}
	if s.ChangedFiles > 0 {
		metaParts = append(metaParts, fmt.Sprintf(tr("%d 文件"), s.ChangedFiles))
	}
	metaText := strings.Join(metaParts, " · ")

	card := ui.Column(c.Key("radar-card-"+s.ID)).FillWidth().Padding(12, 14).Gap(6).
		Radius(10).Background(cardBg).Border(1, borderCol).Transition(colorMotion())
	if t.Dark {
		card.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.04))
	} else {
		card.Shadow(0, 1, 3, 0, ui.RGBA(15, 23, 42, 0.05))
	}
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(stCol).Shrink(0)
			titleEl := ui.Text(c, title).FontSize(13.5).FontWeight(600).TextColor(t.Text).SingleLine().MinWidth(0)
			if s.Repo != "" {
				titleEl.Tooltip(homePath(s.Repo))
			}

			badge := chipSoft(c, "radar-agent-"+s.ID, s.Agent, t.Accent, true)
			if s.Model != "" {
				badge.Tooltip(s.Model)
			}

			if metaText != "" {
				ui.Text(c, metaText).FontSize(12).TextColor(t.TextMuted).SingleLine().MinWidth(0).Shrink(1)
			}

			ui.Box(c).Grow(1).MinWidth(4)

			if s.Repo != "" && !isDockerRepoPath(s.Repo) {
				if ui.Button(c.Key("radar-diff-"+s.ID), tr("查看改动")).
					Height(26).Padding(0, 10).FontSize(12).Shrink(0).Clicked() {
					a.openRepo(s.Repo)
				}
			}
			focusTip := radarFocusTooltip(s.Target)
			focusBtn := ui.ButtonBase(c.Key("radar-focus-"+s.ID)).Label(focusTip).Tooltip(focusTip)
			styleActionIcon(c, focusBtn, iconTerminal, 26, 14, t.TextMuted, false)
			if focusBtn.Clicked() {
				a.focusRadarSession(s)
			}

			ackBtn := ui.ButtonBase(c.Key("radar-ack-"+s.ID)).Label(tr("标记已处理")).Tooltip(tr("标记已处理"))
			styleActionIcon(c, ackBtn, iconCheck, 26, 14, t.TextMuted, false)
			if ackBtn.Clicked() {
				a.dismissRadarSession(s)
			}
		})

		if s.Summary != "" || !s.UpdatedAt.IsZero() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 16).Children(func() {
				if s.Summary != "" {
					ui.Text(c, s.Summary).FontSize(12.5).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0).Tooltip(s.Summary)
				} else {
					ui.Box(c).Grow(1)
				}
				if !s.UpdatedAt.IsZero() {
					ui.Text(c, formatBoardRelativeTime(s.UpdatedAt)).FontSize(11.5).TextColor(t.TextMuted.Alpha(0.85)).SingleLine().Shrink(0)
				}
			})
		}

		if len(s.OverlapFiles) > 0 {
			overlapText := fmt.Sprintf(tr("与 %s 重叠 %d 文件"), strings.Join(s.OverlapWith, "、"), len(s.OverlapFiles))
			ui.Row(c).FillWidth().Padding(0, 0, 0, 16).Children(func() {
				chipSoft(c, "radar-overlap-"+s.ID, overlapText, t.Warning, false).
					Tooltip(strings.Join(s.OverlapFiles, "\n"))
			})
		}
	})
}

func (a *app) radarModals(c *ui.Context) {
	a.radarHookModal(c)
}

func (a *app) radarHookModal(c *ui.Context) {
	t := c.Theme()
	appModal(c.Key("radar-hook-modal"), &a.radar.showHookModal, tr("接入 Agent Hook"), 580, false, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			seg := ui.SegmentedBase(c.Key("radar-hook-tabs"), &a.radar.hookTab, 3)
			seg.Track.FillWidth().Padding(2).Gap(2).Radius(t.Radius).
				Background(t.Surface).Border(1, t.Border.Alpha(0.7)).Label(tr("Hook 类型"))
			labels := []string{"Codex", "Claude Code", "Shell"}
			seg.Track.Children(func() {
				for i, label := range labels {
					active := a.radar.hookTab == i
					item := seg.Segment(i).Label(label).Grow(1).Center().Padding(5, 8).
						Radius(max(t.Radius-2, 0)).Cursor(ui.CursorPointer).Transition(colorMotion())
					if active {
						item.Background(t.Background).Border(1, t.Border.Alpha(0.6))
					} else if item.Hovered() {
						item.Background(t.SurfaceHover)
					}
					item.Children(func() {
						txt := ui.Text(c, label).FontSize(12.5).SingleLine()
						if active {
							txt.FontWeight(600).TextColor(t.Text)
						} else {
							txt.TextColor(t.TextMuted)
						}
					})
				}
			})

			snippet := radar.CodexNotifyConfig()
			switch a.radar.hookTab {
			case 1:
				snippet = radar.ClaudeHooksConfig()
			case 2:
				snippet = radar.GenericShellConfig()
			}
			ui.TextArea(c.Key("radar-hook-snippet"), &snippet).Font("monospace").FontSize(12).ReadOnly(true).Height(210).FillWidth()
		})
	}, func() {
		snippet := radar.CodexNotifyConfig()
		switch a.radar.hookTab {
		case 1:
			snippet = radar.ClaudeHooksConfig()
		case 2:
			snippet = radar.GenericShellConfig()
		}
		copyTextButton(c, "radar-hook-copy", "复制配置", snippet)
		if ui.PrimaryButton(c.Key("radar-hook-close"), tr("完成")).Clicked() {
			a.radar.showHookModal = false
		}
	})
}
