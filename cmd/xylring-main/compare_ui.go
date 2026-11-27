package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type fileViewPillMatch struct{ key string }

type diffLayoutPillMatch struct{}

func fileViewToggleWidget(c *ui.Context, key, label string, view *int) {
	theme := c.Theme()
	trackBg := ui.RGBA(15, 23, 42, 0.045)
	pillActiveBg := ui.Hex("#FFFFFF")
	if theme.Dark {
		trackBg = ui.RGBA(255, 255, 255, 0.05)
		pillActiveBg = ui.Hex("#232630")
	}
	seg := ui.SegmentedBase(c.Key(key), view, 2)
	seg.Track.Shrink(0).AlignItems(ui.Stretch).Padding(2).Gap(2).Radius(8).
		Background(trackBg).Border(1, theme.Border.Alpha(0.55)).Label(tr(label))
	seg.Track.Children(func() {
		icons := [2]*ui.SVG{iconList, iconListTree}
		names := [2]string{"列表", "树"}
		for i, ic := range icons {
			active := i == *view
			s := seg.Segment(i).Label(tr(names[i])).Tooltip(tr(names[i])).
				Padding(theme.Space(1), theme.Space(1.5)).Radius(6).Center().
				Cursor(ui.CursorPointer).Transition(colorMotion())
			if !active && s.Hovered() {
				s.Background(theme.SurfaceHover.Alpha(0.7))
			}
			s.Children(func() {
				if active {
					pill := ui.Box(c.Key(key+"-active-pill")).Absolute().Fill().
						Radius(6).Background(pillActiveBg).
						Border(1, theme.Border.Alpha(0.55)).
						IgnoreInput(true).ZIndex(0).
						Transition(ui.ElementTransition{
							Animation: &uiSpring,
							Match:     fileViewPillMatch{key: key},
						})
					if theme.Dark {
						pill.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.08))
					} else {
						pill.Shadow(0, 1, 2, 0, ui.RGBA(15, 23, 42, 0.08))
					}
				}
				icColor := theme.TextMuted
				if active {
					icColor = theme.Text
				}
				ui.Icon(c, ic).TextColor(icColor).ZIndex(1)
			})
		}
	})
}

func (a *app) fileViewToggle(c *ui.Context) {
	fileViewToggleWidget(c, "file-view", "文件视图", &a.fileView)
}

func (a *app) inspectAmbientColor(c *ui.Context, t *repoTab) ui.Color {
	theme := c.Theme()
	if t.worktree {
		for _, f := range t.worktreeFiles {
			if f.extra == "冲突" {
				return theme.Danger
			}
		}
		return theme.Warning
	}
	if t.comparison != nil {
		return theme.Accent
	}
	if t.inspecting != zeroHash {
		if idx := t.indexOf(t.inspecting); idx >= 0 {
			return graphColor(t.commits[idx].nodeColor, nil)
		}
	}
	return theme.Accent
}

func (a *app) inspectFilesPane(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	tint := a.inspectAmbientColor(c, t)
	alpha := float32(0.065)
	if theme.Dark {
		alpha = 0.095
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).BorderWidth(0, 1, 0, 0).BorderColor(theme.Border).Children(func() {
		ui.Column(c.Key("inspect-files-header-band")).FillWidth().Shrink(0).
			LinearGradient(ui.LinearGradient{From: tint.Alpha(alpha), To: ui.Transparent, Angle: 180, Oklab: true}).
			Children(func() {
				a.fileListHeader(c, t)
				if !t.worktree {
					a.commitSummaryCard(c, t)
				}
			})
		a.fileFilterBar(c, t)
		if t.compareLoading && len(t.compareFiles) == 0 {
			busyBlock(c, "files-loading", tr("正在读取改动…"), "").Grow(1)
			return
		}
		if t.compareErr != "" && len(t.compareFiles) == 0 {
			ui.Column(c).Grow(1).Center().Padding(12).Children(func() {
				ui.Text(c, trMessage(t.compareErr)).TextColor(theme.Danger)
			})
			return
		}
		ui.Themed(c.Key("file-selection-theme"), listTheme(c)).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			if a.fileView == 1 {
				a.fileTreeList(c, t)
			} else {
				a.fileFlatList(c, t)
			}
			if len(t.compareFiles) == 0 && !t.compareLoading {
				emptyHint(c, "files-empty", emptyHintProps{
					Icon:  iconGitCompare,
					Title: filesEmptyHint(t),
				}).Grow(1)
			}
		})
		if t.worktree {
			a.worktreeCommitCard(c, t)
		}
	})
}

// noMatchingFiles says the filter hid every file, and clears it on request.
func noMatchingFiles(c *ui.Context, filter *string) {
	emptyHint(c, "files-no-match", emptyHintProps{
		Icon:  iconSearch,
		Title: tr("无匹配文件"),
		Action: func() {
			if ui.Button(c.Key("files-clear-filter"), tr("清除筛选")).Clicked() {
				*filter = ""
			}
		},
	})
}

// filesEmptyHint is the one sentence an empty file list shows: a clean
// worktree, a commit that changed nothing, or two versions that match.
func filesEmptyHint(t *repoTab) string {
	switch {
	case t.worktree:
		return tr("工作区没有未提交的改动")
	case t.comparison != nil:
		return tr("这两个版本没有文件差异")
	default:
		return tr("这条提交没有改动任何文件")
	}
}

func (a *app) sidebarActiveInspect(c *ui.Context, t *repoTab) {
	a.inspectFilesPane(c, t)
}

func (a *app) fileDiffPane(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	if t.compareLoading && len(t.compareFiles) == 0 {
		busyBlock(c, "diff-loading", tr("正在读取改动…"), "").Grow(1)
		return
	}
	if t.compareErr != "" && len(t.compareFiles) == 0 {
		ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
			ui.Text(c, trMessage(t.compareErr)).TextColor(theme.Danger)
		})
		return
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		a.fileDiffHeader(c, t)
		a.fileDiffContent(c, t.diff, t.simpleDiff, t.imageDiff, t.diffNote, t.diffLoading, t.imageViewMode, t.imageFitActual)
		a.noteInputBar(c, t)
	})
}

// noteInputBar 是 diff 下方那条覆盖式输入条：不占布局，⌘↩ 保存，Esc 取消。
func (a *app) noteInputBar(c *ui.Context, t *repoTab) {
	n := t.noteEdit
	if n == nil {
		return
	}
	theme := c.Theme()
	save := func() {
		text := strings.TrimSpace(n.Text)
		if text != "" && n.noteKey() == t.currentFileRowKey() {
			t.diffNotes = append(t.diffNotes, diffNote{Path: n.Path, Side: n.Side, Line: n.Line, Text: text, Section: n.Section})
		}
		t.noteEdit = nil
	}
	cancel := func() { t.noteEdit = nil }
	var input ui.Element
	bar := ui.Row(c.Key("diff-note-bar")).Attach(ui.AnchorBottomLeft, ui.AnchorBottomLeft).FillWidth().
		Padding(6, 10).Gap(8).AlignItems(ui.Center).
		Background(theme.Surface).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Transition(fadeEnter()).
		OnShortcut(commandModifier(), ui.KeyEnter, save).OnShortcut(0, ui.KeyEscape, cancel)
	bar.Children(func() {
		ui.Text(c, fmt.Sprintf("L%d", n.Line)).Font("monospace").FontSize(11).TextColor(theme.TextMuted).Shrink(0)
		input = ui.TextInput(c.Key("diff-note-input"), &n.Text).Label(tr("笔记")).Placeholder(tr("写一条笔记")).Grow(1).MinWidth(80).AutoFocus()
		if ui.PrimaryButton(c.Key("diff-note-save"), tr("保存")).Shrink(0).Disabled(strings.TrimSpace(n.Text) == "").Clicked() || input.Submitted() {
			save()
		}
		kbdHint(c, "diff-note-save-key", "⌘↩")
		if ui.Button(c.Key("diff-note-cancel"), tr("取消")).Shrink(0).Clicked() {
			cancel()
		}
	})
}

func (a *app) commitSummaryCard(c *ui.Context, t *repoTab) {
	if t.inspecting == zeroHash || t.comparison != nil || t.worktree {
		return
	}
	idx := t.indexOf(t.inspecting)
	if idx < 0 {
		return
	}
	item := &t.commits[idx]
	theme := c.Theme()
	ui.Column(c.Key(fmt.Sprintf("commit-summary-%d", t.id))).Padding(2, 8, 6, 8).Gap(4).Shrink(0).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, item.author).FontSize(12).TextColor(theme.TextMuted).SingleLine()
			ui.Text(c, "·").FontSize(12).TextColor(theme.TextMuted)
			ui.Text(c, item.whenText()).FontSize(12).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
			ui.Text(c, "·").FontSize(12).TextColor(theme.TextMuted)
			ui.Text(c, shortHash(item.hash)).Font("monospace").FontSize(12).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
			copyIconButton(c, "copy-commit-hash", "复制哈希", hashHex(item.hash))
			label := "完整信息"
			if t.detailsOpen {
				label = "收起信息"
			}
			btn := ui.ButtonBase(c.Key("commit-details")).Label(tr(label)).Padding(1, 6).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Transition(colorMotion())
			if t.detailsOpen {
				btn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
			} else {
				btn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border)
			}
			btn.Children(func() {
				ui.Text(c, tr(label)).FontSize(11).SingleLine()
			})
			if btn.Clicked() {
				a.toggleDetails(t)
			}
			if len(item.parents) > 1 {
				for pi := range item.parents {
					pIdx := pi
					label := fmt.Sprintf(tr("父提交 %d"), pi+1)
					btn := ui.ButtonBase(c.Key(fmt.Sprintf("parent-%d", pi))).Padding(1, 6).Radius(theme.Radius).Cursor(ui.CursorPointer).
						Tooltip(tr("对比父提交 ") + shortHash(item.parents[pi])).Transition(colorMotion())
					if t.inspectParent == pi {
						btn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
					} else {
						btn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border)
					}
					btn.Children(func() {
						ui.Text(c, tr(label)).FontSize(11).SingleLine()
					})
					if btn.Clicked() && t.inspectParent != pIdx {
						a.startCommitDiff(t, item, pIdx)
					}
				}
			}
			if tasks := a.boardTasksForCommit(t.path, hashHex(item.hash)); len(tasks) > 0 {
				limit := min(len(tasks), 2)
				for _, bt := range tasks[:limit] {
					taskCopy := bt
					stCol := boardStatusColor(c, taskCopy.Status)
					tBtn := ui.ButtonBase(c.Key("commit-board-task-"+taskCopy.ID)).
						Label(taskCopy.ID).Tooltip(taskCopy.ID + " · " + taskCopy.Title).
						Padding(1, 6).Gap(4).Radius(9).AlignItems(ui.Center).Shrink(0).
						Cursor(ui.CursorPointer).Transition(colorMotion())
					bg := stCol.Alpha(0.12)
					border := stCol.Alpha(0.38)
					if tBtn.Pressed() {
						bg = stCol.Alpha(0.22)
					} else if tBtn.Hovered() {
						bg = stCol.Alpha(0.18)
						border = stCol.Alpha(0.6)
					}
					tBtn.Background(bg).Border(1, border)
					tBtn.Children(func() {
						ui.Icon(c, iconKanban).Size(11, 11).TextColor(stCol).Shrink(0)
						ui.Text(c, taskCopy.ID).Font("monospace").FontSize(11).FontWeight(600).TextColor(stCol).SingleLine()
					})
					if tBtn.Clicked() {
						a.openBoard()
						a.selectBoardTask(taskCopy.ID)
					}
				}
			}
		})
		a.commitDetailsInline(c, t)
		if !t.detailsShown && t.details != nil && t.details.hash == hashHex(item.hash) {
			if body := t.details.body(); body != "" {
				ui.Scroll(c.Key(fmt.Sprintf("commit-body-%d", t.id))).MaxHeight(68).Padding(4, 6).Radius(theme.Radius).Background(theme.Surface).Children(func() {
					ui.Text(c, body).FontSize(12).TextColor(theme.TextMuted)
				})
			}
		}
	})
}

func (a *app) fileListHeader(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	ui.Row(c).FillWidth().Padding(4, 8).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		a.fileListTitle(c, t)
		// 已读计数只在真读过东西时出现，且整屏只有这一处。
		if viewed, total := t.reviewedCount(); viewed > 0 {
			pct := float32(viewed) * 100 / float32(max(total, 1))
			barCol := theme.Accent
			if viewed >= total {
				barCol = theme.Success
			}
			ui.Row(c.Key("file-review-progress")).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Box(c).Width(36).Height(3).Radius(1.5).Background(theme.Border.Alpha(0.55)).Clip().Children(func() {
					ui.Box(c.Key("file-review-progress-fill")).Height(3).WidthPercent(pct).Radius(1.5).
						Background(barCol).Transition(ui.ElementTransition{Animation: &uiSpring, Width: true, Colors: true})
				})
				ui.Text(c, fmt.Sprintf(tr("%d/%d 已读"), viewed, total)).FontSize(11).FontFeatures("tnum").TextColor(theme.TextMuted).Shrink(0)
			})
		}
		if t.comparison != nil {
			if iconButton(c, "compare-close", tr("返回"), iconBack).Shrink(0).Clicked() {
				a.endComparison(t)
			}
		}
		a.fileViewToggle(c)
		if a.fileView == 1 {
			if paneIconButton(c, "tree-expand-all", tr("全部展开"), iconExpandAll).Clicked() {
				t.expandAllTree()
			}
			if paneIconButton(c, "tree-collapse-all", tr("全部收起"), iconCollapseAll).Clicked() {
				t.collapseAllTree()
			}
		}
		// 只剩一侧时列表不分组，这一侧的批量操作就落在列表行尾，不跟着分组标题一起消失。
		a.bulkStageButton(c, t, t.soloFileSide())
		// 导出管的是这批文件，所以放在文件列表行尾，不占 diff 头部。
		if paneIconButton(c, "copy-diff-notes", tr("复制审阅笔记"), iconCopy).Disabled(len(t.diffNotes) == 0).Clicked() {
			a.copyDiffNotes(c, t)
		}
	})
}

func (a *app) fileFilterBar(c *ui.Context, t *repoTab) {
	if len(t.compareFiles) == 0 && !t.compareLoading {
		return
	}
	theme := c.Theme()
	ui.Row(c.Key(fmt.Sprintf("file-filter-bar-%d", t.id))).FillWidth().Padding(2, 8, 6, 8).Shrink(0).Children(func() {
		box := ui.Row(c.Key(fmt.Sprintf("filter-files-box-%d", t.id))).
			Grow(1).MinWidth(60).Height(28).Padding(0, 8).Gap(6).
			AlignItems(ui.Center).Radius(14).Background(theme.Background).
			Transition(colorMotion())
		var input ui.Element
		box.Children(func() {
			ui.Icon(c, iconSearch).Size(13, 13).TextColor(theme.TextMuted).Shrink(0)
			input = ui.TextInputBase(c.Key(fmt.Sprintf("filter-files-input-%d", t.id)), &t.fileFilter).
				Label(tr("过滤文件")).
				Placeholder(tr("过滤文件")).
				Bind(&t.fileFilterInput).
				FontSize(12.5).
				Grow(1).MinWidth(0).FocusRing(false)
			if t.fileFilter != "" {
				indices := t.filteredFileIndices()
				ui.Text(c, fmt.Sprintf("%d/%d", len(indices), len(t.compareFiles))).
					FontSize(11).FontFeatures("tnum").TextColor(theme.TextMuted).Shrink(0)
				if closeIconButton(c, fmt.Sprintf("clear-file-filter-%d", t.id), tr("清除过滤")).Clicked() {
					t.fileFilter = ""
					input.Focus()
				}
			}
		})
		border := theme.Border.Alpha(0.75)
		if box.Hovered() {
			border = theme.TextMuted
		}
		if input.Focused() {
			border = theme.Accent
			box.Shadow(0, 0, 0, 2, theme.Accent.Alpha(0.14))
		}
		box.Border(1, border)
	})
}

func (a *app) fileDiffHeader(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	maxLabel := tr("放大阅读")
	maxIcon := iconMaximize
	if t.diffMaximized {
		maxLabel = tr("还原布局")
		maxIcon = iconMinimize
	}
	ui.Row(c).FillWidth().Padding(5, 12).Gap(8).AlignItems(ui.Center).Shrink(0).
		Background(theme.Surface.Alpha(0.35)).
		BorderWidth(0, 0, 1, 0).BorderColor(theme.Border.Alpha(0.55)).Children(func() {
		if t.comparison != nil {
			if ui.Checkbox(c.Key("compare-three-live"), &t.comparison.threeDot, tr("从分叉点起")).Changed() {
				a.loadComparisonDiff(t)
			}
		}
		if t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
			f := t.compareFiles[t.fileSel]
			dir := filepath.Dir(f.path)
			base := filepath.Base(f.path)
			if f.oldPath != "" && f.oldPath != f.path {
				base = displayPath(f.oldPath) + " → " + displayPath(f.path)
			}
			ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(1).MinWidth(0).Children(func() {
				fileStatusMark(c, f)
				// 列表藏起来时（放大阅读）这里补一句这是哪一侧，不然两行看着一样。
				if f.side != sideNone && t.diffMaximized {
					ui.Text(c, fileSideTitle(f.side)).FontSize(11).TextColor(theme.TextMuted).Shrink(0)
				}
				ui.Text(c, base).Bold().FontSize(12).SingleLine().Shrink(0)
				if dir != "" && dir != "." {
					ui.Text(c, dir).FontSize(11).TextColor(theme.TextMuted).SingleLine().Shrink(1).MinWidth(0)
				}
			})
		} else if t.worktree {
			ui.Text(c, tr("选择文件")).Bold().FontSize(12).TextColor(theme.TextMuted).SingleLine().Shrink(0)
		}
		ui.Box(c).Grow(1)
		if t.imageDiff != nil {
			a.imageDiffControls(c, t.id, t.imageDiff, &t.imageViewMode, &t.imageFitActual)
		} else {
			a.diffLayoutToggle(c)
			a.diffIgnoreWhitespaceToggle(c, t)
			a.diffWrapToggle(c)
			if t.diff != nil || t.simpleDiff != nil {
				findTip := "在 Diff 中查找 (⌘F)"
				if runtime.GOOS != "darwin" {
					findTip = "在 Diff 中查找 (Ctrl+F)"
				}
				if paneIconButton(c, "find-in-diff", tr("在 Diff 中查找"), iconSearch).Tooltip(tr(findTip)).Clicked() {
					a.openDiffFind(t)
				}
			}
		}
		fullWidthTip := tr("全宽展示 (展开至整窗宽度)")
		if a.bottomFullWidth {
			fullWidthTip = tr("侧栏旁展示 (还原底栏宽度)")
		}
		if paneToggleIconButton(c, "toggle-bottom-width", tr("切换底栏全宽"), fullWidthTip, iconPanelBottom, a.bottomFullWidth).Clicked() {
			a.toggleBottomFullWidth()
		}
		if paneIconButton(c, "toggle-diff-max", maxLabel, maxIcon).Clicked() {
			t.diffMaximized = !t.diffMaximized
		}
		if closeIconButton(c, "close-bottom-pane", tr("关闭详情")).Shrink(0).Clicked() {
			a.closeBottomPane(t)
		}
	})
}

func (a *app) currentDiffLayout() editor.DiffLayout {
	if a.diffLayout == 1 {
		return editor.DiffUnified
	}
	return editor.DiffSplit
}

// prepareDiffState 给每个 diff 装上显示选项和按键，两条渲染路径共用。
func (a *app) prepareDiffState(d *editor.DiffState) {
	d.Wrap = a.diffWrap
	d.Layout = a.currentDiffLayout()
	d.Bindings = append(editor.DefaultBindings(),
		editor.Binding{Key: ui.KeyJ, Command: editor.NextHunk},
		editor.Binding{Key: ui.KeyK, Command: editor.PreviousHunk},
	)
	// 行级笔记只装在底栏详情面板的 diff 上：文件标签页共用这个渲染函数，
	// 但输入条画在详情面板里，标签页没有它的位置。
	if t := a.activeTab(); t != nil && t.diff == d {
		a.prepareDiffNotes(t, d)
	}
}

// noteAnnotationHeight 是每条笔记在行下占的高度。
const noteAnnotationHeight float32 = 22

// prepareDiffNotes 把笔记接到 mygo 的行选择、行号加号和行下注释上。
func (a *app) prepareDiffNotes(t *repoTab, d *editor.DiffState) {
	notes := t.notesFor(t.currentFileRowKey())
	d.LineSelection = true
	d.OnGutterButton = func(r editor.DiffLineRange) {
		a.openNoteEditor(t, r)
	}
	d.Annotations = make([]editor.DiffAnnotation, 0, len(notes))
	for i, n := range notes {
		d.Annotations = append(d.Annotations, editor.DiffAnnotation{
			Side:   noteSide(n.Side),
			Line:   n.Line - 1, // 笔记里的行号从 1 起，diff 的从 0 起。
			Height: noteAnnotationHeight,
			Key:    i,
		})
	}
	d.RenderAnnotation = func(c *ui.Context, ann editor.DiffAnnotation) {
		i, ok := ann.Key.(int)
		if !ok || i < 0 || i >= len(notes) {
			return
		}
		renderDiffNote(c, notes[i])
	}
}

// renderDiffNote 在行下画出笔记正文。
func renderDiffNote(c *ui.Context, n diffNote) {
	ui.Row(c).FillWidth().Padding(0, 8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, n.Text).FontSize(11).TextColor(c.Theme().Text).SingleLine().Grow(1).MinWidth(0).Tooltip(n.Text)
	})
}

// openNoteEditor 在点中的那一行下打开输入条。
func (a *app) openNoteEditor(t *repoTab, r editor.DiffLineRange) {
	path := t.currentFilePath()
	if path == "" {
		return
	}
	t.noteEdit = &diffNote{Path: path, Side: noteSideText(r.Side), Line: r.Start + 1, Section: t.compareFiles[t.fileSel].side}
}

// noteSide 把笔记里的边翻回 mygo 的边。
func noteSide(side string) editor.DiffSide {
	if side == "old" {
		return editor.DiffOld
	}
	return editor.DiffNew
}

// noteSideText 把 mygo 的边写成笔记里的 "old" / "new"。
func noteSideText(s editor.DiffSide) string {
	if s == editor.DiffOld {
		return "old"
	}
	return "new"
}

func (a *app) openDiffFind(t *repoTab) {
	if t == nil {
		return
	}
	if t.contentTab > 0 && t.contentTab <= len(t.fileTabs) {
		a.openFileTabFind(t.fileTabs[t.contentTab-1])
		return
	}
	if t.diff != nil {
		t.diff.Run(editor.Find)
	} else if t.simpleDiff != nil {
		t.simpleDiff.new.Run(editor.Find)
	}
}

func (a *app) diffLayoutToggle(c *ui.Context) {
	theme := c.Theme()
	trackBg := ui.RGBA(15, 23, 42, 0.045)
	pillActiveBg := ui.Hex("#FFFFFF")
	if theme.Dark {
		trackBg = ui.RGBA(255, 255, 255, 0.05)
		pillActiveBg = ui.Hex("#232630")
	}
	seg := ui.SegmentedBase(c.Key("diff-layout-toggle"), &a.diffLayout, 2)
	seg.Track.Height(24).Shrink(0).AlignItems(ui.Stretch).Padding(2).Gap(2).Radius(8).
		Background(trackBg).Border(1, theme.Border.Alpha(0.55)).Label(tr("差异排版"))
	seg.Track.Children(func() {
		icons := [2]*ui.SVG{iconColumns2, iconRows2}
		names := [2]string{"并排", "内联"}
		tips := [2]string{"左右分栏 (Split)", "内联展示 (Unified)"}
		for i := 0; i < 2; i++ {
			active := i == a.diffLayout
			s := seg.Segment(i).Label(tr(names[i])).Tooltip(tr(tips[i])).
				Padding(0, 6).Radius(6).Center().Cursor(ui.CursorPointer).Transition(colorMotion())
			if !active && s.Hovered() {
				s.Background(theme.SurfaceHover.Alpha(0.7))
			}
			s.Children(func() {
				if active {
					pill := ui.Box(c.Key("diff-layout-active-pill")).Absolute().Fill().
						Radius(6).Background(pillActiveBg).
						Border(1, theme.Border.Alpha(0.55)).
						IgnoreInput(true).ZIndex(0).
						Transition(ui.ElementTransition{
							Animation: &uiSpring,
							Match:     diffLayoutPillMatch{},
						})
					if theme.Dark {
						pill.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.08))
					} else {
						pill.Shadow(0, 1, 2, 0, ui.RGBA(15, 23, 42, 0.08))
					}
				}
				icColor := theme.TextMuted
				if active {
					icColor = theme.Text
				}
				ui.Icon(c, icons[i]).Size(14, 14).TextColor(icColor).ZIndex(1)
			})
		}
	})
}

func (a *app) diffIgnoreWhitespaceToggle(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	if t != nil && isDockerRepoPath(t.path) && !a.gitReady() {
		// 这一项用的是本机 Git；容器仓库可以照常看差异，只是这个算法暂时不可用。
		ui.ButtonBase(c.Key("diff-ignore-whitespace")).Height(24).Label(tr("忽略空白")).
			Tooltip(tr("忽略空白差异需要本机 Git")).Padding(0, 9).Radius(12).
			Shrink(0).Center().Disabled(true).Children(func() {
			ui.Text(c, tr("忽略空白")).FontSize(11).SingleLine()
		})
		return
	}
	btn := ui.ButtonBase(c.Key("diff-ignore-whitespace")).Height(24).Label(tr("忽略空白")).Tooltip(tr("忽略空白字符改动")).Padding(0, 9).Radius(12).Cursor(ui.CursorPointer).Shrink(0).Center().Transition(colorMotion())
	switch {
	case a.diffIgnoreWhitespace:
		btn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
	case btn.Pressed():
		btn.Background(theme.SurfacePressed).TextColor(theme.Text).Border(1, theme.Border)
	case btn.Hovered():
		btn.Background(theme.SurfaceHover).TextColor(theme.Text).Border(1, theme.Border)
	default:
		btn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border.Alpha(0.8))
	}
	btn.Children(func() {
		ui.Text(c, tr("忽略空白")).FontSize(11).SingleLine()
	})
	if btn.Clicked() {
		a.diffIgnoreWhitespace = !a.diffIgnoreWhitespace
		if t != nil && t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
			a.loadFileDiff(t, t.fileSel)
		}
	}
}

func (a *app) diffWrapToggle(c *ui.Context) {
	theme := c.Theme()
	btn := ui.ButtonBase(c.Key("diff-wrap-toggle")).Height(24).Label(tr("折行")).Tooltip(tr("折行显示长行")).Padding(0, 9).Radius(12).Cursor(ui.CursorPointer).Shrink(0).Center().Transition(colorMotion())
	switch {
	case a.diffWrap:
		btn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
	case btn.Pressed():
		btn.Background(theme.SurfacePressed).TextColor(theme.Text).Border(1, theme.Border)
	case btn.Hovered():
		btn.Background(theme.SurfaceHover).TextColor(theme.Text).Border(1, theme.Border)
	default:
		btn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border.Alpha(0.8))
	}
	btn.Children(func() {
		ui.Text(c, tr("折行")).FontSize(11).SingleLine()
	})
	if btn.Clicked() {
		a.diffWrap = !a.diffWrap
	}
}

func (a *app) formatCompareRef(t *repoTab, hash [20]byte, label string) string {
	if hash == zeroHash {
		if label != "" {
			return label
		}
		return "HEAD"
	}
	short := shortHash(hash)
	refName := label
	if refName == "" || refName == short || refName == hashHex(hash) {
		if t != nil && len(t.refs[hash]) > 0 {
			refName = t.refs[hash][0].name
		} else {
			refName = ""
		}
	}
	if refName != "" && refName != short && refName != hashHex(hash) {
		return fmt.Sprintf("%s (%s)", short, refName)
	}
	return short
}

func (a *app) comparisonBanner(c *ui.Context, tab *repoTab) {
	if tab == nil || tab.comparison == nil {
		return
	}
	t := c.Theme()
	cmp := tab.comparison
	fromText := a.formatCompareRef(tab, cmp.from, cmp.fromLabel)
	toText := a.formatCompareRef(tab, cmp.to, cmp.toLabel)

	ui.Row(c.Key("comparison-banner")).FillWidth().Padding(6, 12).Gap(10).AlignItems(ui.Center).Shrink(0).Background(t.Accent.Alpha(0.08)).Border(1, t.Accent.Alpha(0.2)).Transition(fadeEnter()).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(1).MinWidth(0).Children(func() {
			ui.Text(c, tr("对比模式")).Bold().FontSize(12).TextColor(t.Accent).Shrink(0)
			ui.Text(c, "·").TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, fromText).Font("monospace").FontSize(12).Bold().TextColor(t.Text).SingleLine().Shrink(1).MinWidth(0)
			ui.Text(c, "⇄").FontSize(13).Bold().TextColor(t.Accent).Shrink(0)
			ui.Text(c, toText).Font("monospace").FontSize(12).Bold().TextColor(t.Text).SingleLine().Shrink(1).MinWidth(0)
			if cmp.threeDot {
				ui.Text(c, tr("(三点)")).FontSize(11).TextColor(t.TextMuted).Shrink(0)
			}
		})

		ui.Box(c).Grow(1)

		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			if ui.Button(c.Key("compare-banner-exit"), tr("退出对比")).Label(tr("退出对比")).Shrink(0).Clicked() {
				a.endComparison(tab)
			}
			kbdHint(c, "compare-banner-esc", "Esc")
		})
	})
}

func (a *app) fileListTitle(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	switch {
	case t.worktree:
		ui.Text(c, tr("未提交改动")).Bold().SingleLine().Shrink(0)
	case t.comparison != nil:
		from, to := t.comparison.fromLabel, t.comparison.toLabel
		if from == "" {
			from = shortHash(t.comparison.from)
		}
		if to == "" {
			to = shortHash(t.comparison.to)
		}
		sep := "→"
		if t.comparison.threeDot {
			sep = "..."
		}
		ui.Text(c, from).Font("monospace").SingleLine().Shrink(0)
		ui.Text(c, sep).TextColor(theme.TextMuted).Shrink(0)
		ui.Text(c, to).Font("monospace").SingleLine().Grow(1).MinWidth(0)
	default:
		text := tr("提交")
		idx := t.indexOf(t.inspecting)
		row := t.rowIndex(t.inspecting)
		if idx >= 0 {
			text = t.commits[idx].subject
			if text == "" {
				text = tr("（无说明）")
			}
		}
		title := ui.Text(c, text).Bold().SingleLine().Grow(1).MinWidth(0).Tooltip(text)
		if row >= 0 {
			title.ContextMenu(func(m *ui.Menu) {
				a.commitRowMenu(c, m, t, row)
			})
		}
	}
}

func fileIconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	theme := c.Theme()
	btn := ui.ButtonBase(c.Key(key)).Label(tr(name)).Tooltip(tr(name)).Padding(4).Radius(theme.Radius).Cursor(ui.CursorPointer).Transition(colorMotion())
	switch {
	case btn.Pressed():
		btn.Background(theme.SurfacePressed)
	case btn.Hovered():
		btn.Background(theme.Text.Alpha(0.08))
	}
	btn.Children(func() {
		ui.Icon(c, icon)
	})
	return btn
}

// fileReviewButton 是文件行上的已读勾选按钮，排在悬停操作的最左侧：
// 已读是文件列表的事实，不归 diff 头部管。
func fileReviewButton(c *ui.Context, t *repoTab, f fileChange) {
	theme := c.Theme()
	reviewed := t.fileViewed(f.rowKey())
	tip := "标记为已读"
	if reviewed {
		tip = "标记为未读"
	}
	btn := ui.ButtonBase(c.Key("file-review-" + f.rowKey())).Label(tr(tip)).Tooltip(tr(tip)).
		Padding(4).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Transition(colorMotion())
	switch {
	case btn.Pressed():
		btn.Background(theme.SurfacePressed)
	case btn.Hovered():
		btn.Background(theme.Text.Alpha(0.08))
	}
	color := theme.TextMuted
	if reviewed {
		color = theme.Accent
	}
	btn.Children(func() {
		ui.Icon(c, iconCheck).Size(14, 14).TextColor(color)
	})
	if btn.Clicked() {
		t.toggleFileReviewed(f.rowKey())
	}
}
