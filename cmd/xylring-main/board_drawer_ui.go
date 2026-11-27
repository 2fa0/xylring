package main

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *app) boardDetailDrawer(c *ui.Context) {
	t := c.Theme()
	idx := findBoardTaskIndex(a.board.file.Tasks, a.board.selectedID)
	if idx < 0 {
		a.board.selectedID = ""
		return
	}
	task := a.board.file.Tasks[idx]
	if a.board.editTaskID != task.ID {
		a.syncBoardDrafts(task)
		a.verifyBoardTaskCommits(task)
	}

	stCol := boardStatusColor(c, task.Status)
	bandCol := stCol
	if task.Pinned && task.Status != boardDone {
		bandCol = t.Warning
	}
	h, _, _, _, _, _, _ := a.chromeMetrics(c)
	drawer := ui.Column(c.Key("board-detail-drawer")).
		Absolute().Top(h+12).Bottom(12).Right(12).WidthPercent(50).ZIndex(20).
		Radius(16).Background(t.Background).Border(1, t.Border.Alpha(0.8)).Clip().
		Transition(boardDrawerMotion())
	if t.Dark {
		drawer.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.10)).
			Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, 0.32)).
			Shadow(0, 18, 42, -6, ui.RGBA(0, 0, 0, 0.50))
	} else {
		drawer.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.90)).
			Shadow(0, 4, 12, 0, ui.RGBA(15, 23, 42, 0.08)).
			Shadow(0, 18, 42, -6, ui.RGBA(15, 23, 42, 0.16))
	}
	drawer.Children(func() {
		// Top ambient drawer header
		ui.Row(c.Key("board-drawer-header")).FillWidth().Padding(11, 18).Gap(6).AlignItems(ui.Center).
			LinearGradient(ui.LinearGradient{From: bandCol.Alpha(0.09), To: ui.Transparent, Angle: 180, Oklab: true}).
			BorderWidth(0, 0, 1, 0).BorderColor(t.Border.Alpha(0.65)).Shrink(0).Children(func() {
			ui.Row(c.Key("board-drawer-id")).Padding(3, 8).Radius(5).Background(t.Surface).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Text(c, task.ID).Font("monospace").FontSize(14).FontWeight(600).TextColor(t.TextMuted).SingleLine()
			})
			copyIconButton(c, "board-drawer-copy-id", tr("复制任务编号"), task.ID)
			if task.Archived {
				ui.Row(c.Key("board-drawer-archived-badge")).Padding(3, 8).Radius(5).Background(t.Surface).AlignItems(ui.Center).Shrink(0).Children(func() {
					ui.Text(c, tr("已归档")).FontSize(12).TextColor(t.TextMuted).SingleLine()
				})
			}
			ui.Box(c).Grow(1)
			ctxBtn := ui.ButtonBase(c.Key("board-drawer-copy-ctx")).
				Label(tr("复制 Agent 上下文")).Tooltip(tr("复制 Agent 上下文")).
				Height(24).Padding(0, 8).Gap(4).Radius(6).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).Shrink(0).
				Transition(colorMotion())
			switch {
			case ctxBtn.Pressed():
				ctxBtn.Background(t.SurfacePressed)
			case ctxBtn.Hovered():
				ctxBtn.Background(t.SurfaceHover)
			}
			ctxBtn.Children(func() {
				ui.Icon(c, iconCopy).Size(12.5, 12.5).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, tr("复制上下文")).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
			if ctxBtn.Clicked() {
				c.Services().WriteClipboard(formatBoardAgentContext(task))
				c.Toast(tr("已复制 Agent 上下文"))
			}

			pinTip := tr("置顶聚焦")
			pinIcon := iconStar
			pinFg := t.TextMuted
			if task.Pinned {
				pinTip = tr("取消置顶")
				pinIcon = iconStarFilled
				pinFg = t.Warning
			}
			pinBtn := ui.ButtonBase(c.Key("board-drawer-pin")).Label(pinTip).Tooltip(pinTip)
			if styleActionIcon(c, pinBtn, pinIcon, 24, 14, pinFg, task.Pinned).Clicked() {
				next := !task.Pinned
				a.applyBoardTaskPatch(task.ID, boardTaskPatch{Pinned: &next})
			}

			moreBtn := paneIconButton(c, "board-drawer-more", tr("任务操作"), iconMore)
			moreBtn.Menu(func(m *ui.Menu) {
				if task.Archived {
					if m.Item(tr("取消归档")).Chosen() {
						next := false
						a.applyBoardTaskPatch(task.ID, boardTaskPatch{Archived: &next}, tr("已取消归档"))
					}
				} else {
					if m.Item(tr("归档任务")).Chosen() {
						next := true
						a.applyBoardTaskPatch(task.ID, boardTaskPatch{Archived: &next}, tr("已归档"))
					}
				}
				m.Separator()
				if m.Item(tr("删除任务")).Chosen() {
					a.board.deleteTaskID = task.ID
				}
			})
			if closeIconButton(c, "board-drawer-close", tr("关闭详情")).Clicked() {
				a.closeBoardDrawer()
				return
			}
		})

		ui.Scroll(c.Key("board-drawer-scroll")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c.Key("board-drawer-content-"+task.ID)).FillWidth().Padding(16, 20, 28, 20).Gap(14).
				Transition(fadeEnter()).Children(func() {
				// Borderless document-style title input
				titleEl := ui.TextInputBase(c.Key("board-drawer-title"), &a.board.titleDraft).
					Label(tr("标题")).
					Placeholder(tr("输入任务标题")).
					Bind(&a.board.titleInput).
					FontSize(20).FontWeight(700).
					Padding(5, 7).Radius(6).FillWidth().FocusRing(false).
					Transition(colorMotion())
				switch {
				case a.board.titleInput.Focused(c):
					titleEl.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
				case titleEl.Hovered():
					titleEl.Background(t.Surface.Alpha(0.65))
				}
				if titleEl.Submitted() || (!a.board.titleInput.Focused(c) && strings.TrimSpace(a.board.titleDraft) != "" && strings.TrimSpace(a.board.titleDraft) != task.Title) {
					a.commitBoardDrafts()
				}

				// Compact metadata rows
				ui.Column(c.Key("board-drawer-meta")).FillWidth().Gap(8).Children(func() {
					// Status segmented control
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tr("状态")).Width(72).FontSize(14.5).TextColor(t.TextMuted).Shrink(0)
						ui.Box(c).Grow(1).MinWidth(0).Children(func() {
							seg := boardStatusSegmented(c, "board-drawer-status-seg", &a.board.drawerStatusIdx, 14)
							if seg.Track.Changed() {
								target := boardStatuses[a.board.drawerStatusIdx]
								if target != task.Status {
									a.applyBoardTaskPatch(task.ID, boardTaskPatch{Status: &target})
								}
							}
						})
					})

					// Priority row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tr("重要紧急")).Width(72).FontSize(14.5).TextColor(t.TextMuted).Shrink(0)
						boardPriorityPicker(c, "board-drawer-priority", task.Priority, &a.board.drawerPriorityOpen, false, func(next boardPriority) {
							a.applyBoardTaskPatch(task.ID, boardTaskPatch{Priority: &next})
						})
					})

					// Due date row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tr("截止日期")).Width(72).FontSize(14.5).TextColor(t.TextMuted).Shrink(0)
						boardDueDatePicker(c, "board-drawer-due", task.DueDate, task.Status == boardDone || task.Archived, &a.board.drawerDueOpen, &a.board.drawerDuePicker, false, func(next string) {
							a.applyBoardTaskPatch(task.ID, boardTaskPatch{DueDate: &next})
						})
					})

					// Repos row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
						ui.Text(c, tr("关联项目")).Width(72).FontSize(14.5).TextColor(t.TextMuted).Shrink(0).Padding(4, 0, 0, 0)
						ui.Row(c).Grow(1).MinWidth(0).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
							repos := boardTaskAllRepos(task)
							removeRepo := ""
							for _, rp := range repos {
								repoPath := rp
								explicit := slices.ContainsFunc(task.Repos, func(r string) bool {
									return normalizeBoardRepo(r) == repoPath
								})
								filtered := normalizeBoardRepo(a.board.repoFilter) == repoPath
								pillBg := t.Surface.Alpha(0.7)
								pillBorder := t.Border.Alpha(0.7)
								if filtered {
									pillBg = t.Accent.Alpha(0.12)
									pillBorder = t.Accent.Alpha(0.45)
								}
								ui.Row(c.Key("board-drawer-repo-pill-"+repoPath)).Padding(3, 7).Gap(5).
									Radius(5).Background(pillBg).Border(1, pillBorder).AlignItems(ui.Center).Children(func() {
									filterBtn := ui.ButtonBase(c.Key("board-drawer-repo-" + repoPath)).
										Label(repoTitle(repoPath)).
										Tooltip(tr("按此项目筛选") + "\n" + repoPath).
										Cursor(ui.CursorPointer)
									filterBtn.Children(func() {
										ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
											boardRepoDot(c, repoPath, 7)
											lbl := ui.Text(c, repoTitle(repoPath)).FontSize(14).SingleLine()
											if filtered {
												lbl.TextColor(t.Accent).FontWeight(500)
											} else {
												lbl.TextColor(t.Text)
											}
										})
									})
									if filterBtn.Clicked() {
										if filtered {
											a.board.repoFilter = ""
										} else {
											a.board.repoFilter = repoPath
										}
									}
									if explicit {
										rmBtn := ui.ButtonBase(c.Key("board-drawer-repo-rm-"+repoPath)).
											Label(tr("从任务移除")).Tooltip(tr("从任务移除")).
											Size(15, 15).Radius(3).Center().Cursor(ui.CursorPointer)
										if rmBtn.Hovered() {
											rmBtn.Background(t.SurfaceHover)
										}
										rmBtn.Children(func() {
											ui.Icon(c, iconClose).Size(10, 10).TextColor(t.TextMuted)
										})
										if rmBtn.Clicked() {
											removeRepo = repoPath
										}
									}
								})
							}
							if removeRepo != "" {
								next := make([]string, 0, len(task.Repos))
								for _, r := range task.Repos {
									if normalizeBoardRepo(r) != removeRepo {
										next = append(next, r)
									}
								}
								a.applyBoardTaskPatch(task.ID, boardTaskPatch{Repos: &next})
							}

							addRepoBtn := ui.Button(c.Key("board-drawer-add-repo"), tr("+ 项目")).Padding(3, 9).FontSize(14)
							addRepoBtn.Menu(func(m *ui.Menu) {
								known := a.boardKnownRepos()
								if len(known) == 0 {
									m.Item(tr("暂无已打开仓库")).Disabled(true)
									return
								}
								for _, rp := range known {
									repoPath := rp
									already := slices.Contains(repos, repoPath)
									if m.Item(repoTitle(repoPath) + " (" + homePath(repoPath) + ")").Checked(already).Disabled(already).Chosen() {
										a.applyBoardTaskPatch(task.ID, boardTaskPatch{AddRepos: []string{repoPath}})
									}
								}
							})
						})
					})
				})

				ui.Box(c).FillWidth().Height(1).Background(t.Border.Alpha(0.6))

				// Description & interactive checklist
				ui.Column(c.Key("board-drawer-desc-sec")).FillWidth().Gap(8).Children(func() {
					ui.Text(c, tr("任务描述")).FontSize(14.5).FontWeight(600).TextColor(t.TextMuted)
					// The checklist and the field below show the same text: the draft
					// holds every edit not saved yet, so an item added in it gets the
					// line the user sees and a click rewrites only that marker.
					effectiveDesc := a.board.descDraft
					if effectiveDesc == "" {
						effectiveDesc = task.Description
					}
					if drawerChecklist := parseBoardChecklist(effectiveDesc); len(drawerChecklist) > 0 {
						doneCount := 0
						for _, item := range drawerChecklist {
							if item.Checked {
								doneCount++
							}
						}
						pct := float32(doneCount) * 100 / float32(len(drawerChecklist))
						barCol := t.Accent
						if doneCount == len(drawerChecklist) {
							barCol = t.Success
						}
						ui.Column(c.Key("board-drawer-checklist")).FillWidth().Padding(8, 10).Gap(4).
							Radius(10).Background(t.Surface.Alpha(0.5)).Border(1, t.Border.Alpha(0.6)).Children(func() {
							ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(0, 4, 2, 4).Children(func() {
								ui.Box(c).Grow(1).MinWidth(0).Height(3).Radius(1.5).Background(t.Border.Alpha(0.5)).Clip().Children(func() {
									ui.Box(c.Key("board-drawer-chk-fill")).Height(3).WidthPercent(pct).Radius(1.5).
										Background(barCol).Transition(ui.ElementTransition{Animation: &boardSpring, Width: true, Colors: true})
								})
								ui.Text(c, fmt.Sprintf("%d/%d", doneCount, len(drawerChecklist))).FontSize(11.5).FontFeatures("tnum").
									TextColor(t.TextMuted).SingleLine().Shrink(0)
							})
							for _, item := range drawerChecklist {
								rowBtn := ui.ButtonBase(c.Key(fmt.Sprintf("board-drawer-chk-%s-%d", task.ID, item.LineIndex))).
									Label(item.Text).
									Role(ui.RoleCheckBox).Checked(item.Checked).
									FillWidth().Padding(4, 6).Gap(7).Radius(6).
									AlignItems(ui.Center).Justify(ui.Start).
									Cursor(ui.CursorPointer).Transition(colorMotion())
								if rowBtn.Hovered() || rowBtn.Pressed() {
									rowBtn.Background(t.SurfaceHover)
								}
								rowBtn.Children(func() {
									if item.Checked {
										ui.Icon(c, iconCheckCircle2).Size(14, 14).TextColor(t.Success).Shrink(0)
										ui.Text(c, item.Text).FontSize(13.5).TextColor(t.TextMuted).Strikethrough().Grow(1).MinWidth(0)
									} else {
										ui.Icon(c, iconCircle).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
										ui.Text(c, item.Text).FontSize(13.5).TextColor(t.Text).Grow(1).MinWidth(0)
									}
								})
								if rowBtn.Clicked() {
									picked := item
									a.toggleBoardChecklistItem(task.ID, picked)
								}
							}
						})
					}
					descEl := ui.TextAreaBase(c.Key("board-drawer-desc"), &a.board.descDraft).
						Label(tr("任务描述")).
						Placeholder(tr("补充任务背景、目标或验收标准…")).
						Bind(&a.board.descInput).
						Height(96).FillWidth().Padding(9, 11).Radius(8).
						FontSize(15).LineHeight(1.4).FocusRing(false).Transition(colorMotion())
					switch {
					case a.board.descInput.Focused(c):
						descEl.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
					case descEl.Hovered():
						descEl.Background(t.Surface.Alpha(0.65)).Border(1, t.Border)
					default:
						descEl.Background(t.Surface.Alpha(0.45)).Border(1, t.Border.Alpha(0.6))
					}
					if !a.board.descInput.Focused(c) && a.board.descDraft != task.Description {
						a.commitBoardDrafts()
					}
				})

				ui.Box(c).FillWidth().Height(1).Background(t.Border.Alpha(0.6))

				// Linked commits (full-width section)
				ui.Column(c.Key("board-drawer-commits-sec")).FillWidth().Gap(8).Children(func() {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconGitCommit).Size(15, 15).TextColor(t.TextMuted).Shrink(0)
						ui.Text(c, tr("关联提交")).FontSize(14.5).FontWeight(600).TextColor(t.TextMuted)
						if len(task.Commits) > 0 {
							ui.Text(c, fmt.Sprintf("%d", len(task.Commits))).FontSize(14).TextColor(t.TextMuted)
						}
						ui.Box(c).Grow(1)
						if paneIconButton(c, "board-drawer-link-commit", tr("关联提交到任务"), iconPlus).Clicked() {
							a.openLinkCommitModal()
						}
					})
					for _, link := range task.Commits {
						a.boardCommitChipRow(c, task, link)
					}
				})

				ui.Box(c).FillWidth().Height(1).Background(t.Border.Alpha(0.6))

				// Notes timeline
				ui.Column(c.Key("board-drawer-notes-sec")).FillWidth().Gap(8).Children(func() {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconMessage).Size(15, 15).TextColor(t.TextMuted).Shrink(0)
						ui.Text(c, tr("备注")).FontSize(14.5).FontWeight(600).TextColor(t.TextMuted)
						if len(task.Notes) > 0 {
							ui.Text(c, fmt.Sprintf("%d", len(task.Notes))).FontSize(14).TextColor(t.TextMuted)
						}
					})
					if len(task.Notes) > 0 {
						ui.Column(c.Key("board-drawer-notes-list")).FillWidth().Gap(0).Children(func() {
							for idx, n := range task.Notes {
								a.boardNoteCard(c, task, idx, n)
							}
						})
					}

					submitNote := func() {
						if strings.TrimSpace(a.board.noteDraft) == "" {
							return
						}
						text := a.board.noteDraft
						a.forgetBoardTaskDraft(task.ID)
						a.changeBoard(func() (boardFile, error) {
							bf, _, err := boardAddNote(task.ID, text, "human", "")
							return bf, err
						}, func(err error) {
							if err == nil {
								return
							}
							a.setBanner(err.Error(), "")
							if a.board.selectedID == task.ID && a.board.noteDraft == "" {
								a.board.noteDraft = text
							} else if a.board.noteDrafts[task.ID] == "" {
								if a.board.noteDrafts == nil {
									a.board.noteDrafts = map[string]string{}
								}
								a.board.noteDrafts[task.ID] = text
							}
						})
					}
					composer := ui.Column(c.Key("board-drawer-note-box")).FillWidth().Padding(9, 11).Gap(6).
						Radius(8).Transition(colorMotion()).
						OnShortcut(commandModifier(), ui.KeyEnter, submitNote)
					composerFocused := a.board.noteInput.Focused(c)
					switch {
					case composerFocused:
						composer.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
					case composer.Hovered():
						composer.Background(t.Surface.Alpha(0.65)).Border(1, t.Border)
					default:
						composer.Background(t.Surface.Alpha(0.45)).Border(1, t.Border.Alpha(0.6))
					}
					composer.Children(func() {
						ui.TextAreaBase(c.Key("board-drawer-note-input"), &a.board.noteDraft).
							Label(tr("备注")).
							Placeholder(tr("记录进展或给 Agent 留备注…")).
							Bind(&a.board.noteInput).
							Height(60).FillWidth().FontSize(15).LineHeight(1.4).FocusRing(false)
						ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Justify(ui.End).Children(func() {
							shortcut := "Ctrl+↩"
							if runtime.GOOS == "darwin" {
								shortcut = "⌘↩"
							}
							kbdHint(c, "board-drawer-note-key", shortcut)
							canSend := strings.TrimSpace(a.board.noteDraft) != ""
							if ui.PrimaryButton(c.Key("board-drawer-note-add"), tr("添加备注")).
								Padding(4, 12).FontSize(14.5).Disabled(!canSend).Clicked() {
								submitNote()
							}
						})
					})
				})
			})
		})
	})
}

func (a *app) boardNoteCard(c *ui.Context, task boardTask, idx int, n boardNote) {
	t := c.Theme()
	editing := a.board.editingNoteID == n.ID
	isHuman := strings.EqualFold(n.Author, "human") || n.Author == ""
	who := n.Author
	if isHuman {
		who = tr("人工")
	}

	saveEdit := func() {
		if strings.TrimSpace(a.board.editingNoteText) == "" {
			return
		}
		text := a.board.editingNoteText
		a.changeBoard(func() (boardFile, error) {
			bf, _, err := boardUpdateNote(task.ID, n.ID, text)
			return bf, err
		}, func(err error) {
			if err != nil {
				a.setBanner(err.Error(), "")
			} else if a.board.editingNoteID == n.ID && a.board.editingNoteText == text {
				a.board.editingNoteID, a.board.editingNoteText = "", ""
			}
		})
	}
	cancelEdit := func() {
		a.board.editingNoteID = ""
		a.board.editingNoteText = ""
	}

	slot := ui.Column(c.Key("board-note-slot-"+n.ID)).FillWidth().Padding(4, 0)
	var dropOver *boardNoteDrag
	if !editing && len(task.Notes) > 1 {
		if dragged, over := ui.DragOver[boardNoteDrag](slot); over && dragged.taskID == task.ID && dragged.noteID != n.ID {
			dropOver = &dragged
		}
	}

	slot.Children(func() {
		card := ui.Column(c.Key("board-note-"+n.ID)).FillWidth().Padding(9, 11).Gap(5).
			Radius(8).Transition(boardNoteMotion(n.ID))

		switch {
		case editing:
			card.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14)).
				OnShortcut(commandModifier(), ui.KeyEnter, saveEdit).
				OnShortcut(0, ui.KeyEscape, cancelEdit)
		default:
			if len(task.Notes) > 1 {
				card.Drag(boardNoteDrag{taskID: task.ID, noteID: n.ID, index: idx})
				if card.Dragging() {
					card.Opacity(0.4).Cursor(ui.CursorGrabbing)
				} else {
					card.Cursor(ui.CursorGrab)
				}
			}
			switch {
			case card.Hovered() && dropOver == nil:
				card.Background(t.Surface.Alpha(0.75)).Border(1, t.Border.Alpha(0.85))
			default:
				card.Background(t.Surface.Alpha(0.6)).Border(1, t.Border.Alpha(0.45))
			}
		}

		card.Children(func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if !editing && len(task.Notes) > 1 {
					ui.Icon(c, iconGripVertical).Size(13, 13).TextColor(t.TextMuted.Alpha(0.55)).Shrink(0)
				}
				if !isHuman {
					ui.Icon(c, iconBot).Size(13, 13).TextColor(t.Accent).Shrink(0)
					ui.Text(c, who).FontSize(14).FontWeight(600).TextColor(t.Accent).SingleLine()
				} else {
					ui.Text(c, who).FontSize(14).FontWeight(600).TextColor(t.Text).SingleLine()
				}
				if n.Model != "" {
					ui.Text(c, n.Model).FontSize(13.5).TextColor(t.TextMuted).SingleLine().MinWidth(0)
				}
				ui.Box(c).Grow(1)
				timeEl := ui.Text(c, n.DisplayTime().Local().Format("01-02 15:04")).FontSize(13.5).TextColor(t.TextMuted).SingleLine().Shrink(0)
				if !n.UpdatedAt.IsZero() && n.UpdatedAt.After(n.CreatedAt) {
					timeEl.Tooltip(fmt.Sprintf(tr("创建于 %s · 编辑于 %s"), n.CreatedAt.Local().Format("01-02 15:04"), n.UpdatedAt.Local().Format("01-02 15:04")))
				}
				if !editing {
					editBtn := ui.ButtonBase(c.Key("board-note-edit-" + n.ID)).Label(tr("编辑备注")).Tooltip(tr("编辑备注"))
					if styleActionIcon(c, editBtn, iconPencil, 22, 13, t.TextMuted, false).Clicked() {
						a.board.editingNoteID = n.ID
						a.board.editingNoteText = n.Text
						a.board.editNoteInput.Focus(c)
					}
					delBtn := ui.ButtonBase(c.Key("board-note-del-" + n.ID)).Label(tr("删除备注")).Tooltip(tr("删除备注"))
					delFg := t.TextMuted
					if delBtn.Hovered() || delBtn.Pressed() {
						delFg = t.Danger
					}
					if styleActionIcon(c, delBtn, iconTrash2, 22, 13, delFg, false).Clicked() {
						a.board.deleteNoteTaskID = task.ID
						a.board.deleteNoteID = n.ID
					}
				}
			})
			if editing {
				ui.TextAreaBase(c.Key("board-note-edit-input-"+n.ID), &a.board.editingNoteText).
					Label(tr("编辑备注")).
					Bind(&a.board.editNoteInput).
					Height(64).FillWidth().FontSize(15).LineHeight(1.4).FocusRing(false).AutoFocus()
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Justify(ui.End).Children(func() {
					shortcut := "Ctrl+↩"
					if runtime.GOOS == "darwin" {
						shortcut = "⌘↩"
					}
					kbdHint(c, "board-note-edit-key-"+n.ID, shortcut)
					if ui.Button(c.Key("board-note-edit-cancel-"+n.ID), tr("取消")).Padding(3, 10).FontSize(13.5).Clicked() {
						cancelEdit()
					}
					canSave := strings.TrimSpace(a.board.editingNoteText) != ""
					if ui.PrimaryButton(c.Key("board-note-edit-save-"+n.ID), tr("保存")).
						Padding(3, 10).FontSize(13.5).Disabled(!canSave).Clicked() {
						saveEdit()
					}
				})
			} else {
				ui.Text(c, n.Text).FontSize(15).LineHeight(1.4).TextColor(t.Text)
			}
		})
	})

	if dropOver != nil {
		insertAfter := dropOver.index < idx
		slot.DrawOver(func(p *ui.Painter, r ui.Rect) {
			drawBoardDropLine(p, r, insertAfter, t.Accent)
		})
	}

	if !editing && len(task.Notes) > 1 {
		if dropped, ok := ui.Drop[boardNoteDrag](slot); ok && dropped.taskID == task.ID && dropped.noteID != n.ID {
			a.changeBoard(func() (boardFile, error) {
				bf, _, err := boardMoveNote(task.ID, dropped.noteID, idx)
				return bf, err
			}, nil)
		}
	}
}

func (a *app) boardCommitChipRow(c *ui.Context, task boardTask, link boardCommitLink) {
	t := c.Theme()
	short := link.Hash
	if len(short) > 7 {
		short = short[:7]
	}
	key := boardCommitCacheKey(link.Repo, link.Hash)
	valid, checked := a.board.commitValid[key]
	stale := checked && !valid

	bg := t.Surface.Alpha(0.55)
	border := t.Border.Alpha(0.7)
	if stale {
		bg = t.Danger.Alpha(0.08)
		border = t.Danger.Alpha(0.35)
	}
	row := ui.Row(c.Key("board-commit-row-"+link.Repo+"-"+link.Hash)).
		FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Center).
		Radius(8).Background(bg).Transition(colorMotion())
	if row.Hovered() && !stale {
		border = t.Accent.Alpha(0.5)
	}
	row.Border(1, border)
	row.Children(func() {
		jumpBtn := ui.ButtonBase(c.Key("board-commit-open-" + link.Repo + "-" + link.Hash)).
			Label(repoTitle(link.Repo) + " " + short).
			Tooltip(link.Repo + " @ " + link.Hash).
			Grow(1).MinWidth(0).Justify(ui.Start).Cursor(ui.CursorPointer)
		jumpBtn.Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					boardRepoDot(c, link.Repo, 7)
					ui.Text(c, repoTitle(link.Repo)).FontSize(14).FontWeight(600).TextColor(t.Text).SingleLine().Shrink(0)
					ui.Text(c, short).Font("monospace").FontSize(13.5).TextColor(t.TextMuted).SingleLine().Shrink(0)
					ui.Box(c).Grow(1)
					if link.Agent != "" {
						boardAgentTag(c, "board-commit-agent-"+link.Repo+"-"+link.Hash, link.Agent)
					}
					if stale {
						chipSoft(c, "board-commit-stale-"+link.Repo+"-"+link.Hash, tr("提交已失效"), t.Danger, false)
					}
				})
				if link.Subject != "" {
					sub := ui.Text(c, link.Subject).FontSize(14.5).SingleLine().MinWidth(0)
					if stale {
						sub.TextColor(t.TextMuted)
					} else {
						sub.TextColor(t.Text)
					}
				}
			})
		})
		if jumpBtn.Clicked() {
			a.openBoardCommit(link)
		}
		if paneIconButton(c, "board-commit-unlink-"+link.Repo+"-"+link.Hash, tr("移除关联"), iconClose).Clicked() {
			a.commitBoardDrafts()
			a.changeBoard(func() (boardFile, error) {
				bf, _, err := boardUnlinkCommit(task.ID, link.Repo, link.Hash)
				return bf, err
			}, nil)
		}
	})
}
