package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *app) boardModals(c *ui.Context) {
	a.newBoardTaskModal(c)
	a.linkBoardCommitModal(c)
	a.boardSnippetModal(c)
	a.boardDeleteTaskModal(c)
	a.boardDeleteNoteModal(c)
	a.boardImportModal(c)
	a.boardBackupRestoreModal(c)
	a.boardStorageModal(c)
	a.boardSwitchDirModal(c)
}

func (a *app) newBoardTaskModal(c *ui.Context) {
	t := c.Theme()
	appModal(c.Key("board-new-task-modal"), &a.board.showNewModal, tr("新建看板任务"), 480, a.board.newSaving, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Text(c, tr("标题")).FontSize(13).TextColor(t.TextMuted)
			titleInput := ui.TextInput(c.Key("board-new-title"), &a.board.newTitle).
				Placeholder(tr("一句话描述需要完成的任务")).
				FontSize(13.5).
				FillWidth().AutoFocus()

			ui.Text(c, tr("状态")).FontSize(13).TextColor(t.TextMuted)
			if boardStatusSegmented(c, "board-new-status-seg", &a.board.newStatusIdx, 12.5).Track.Changed() {
				a.board.newStatus = boardStatuses[a.board.newStatusIdx]
			}

			ui.Row(c).FillWidth().Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					ui.Text(c, tr("重要紧急")).FontSize(13).TextColor(t.TextMuted)
					boardPriorityPicker(c, "board-new-priority", a.board.newPriority, &a.board.newPriorityOpen, true, func(p boardPriority) {
						a.board.newPriority = p
					})
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					ui.Text(c, tr("截止日期")).FontSize(13).TextColor(t.TextMuted)
					boardDueDatePicker(c, "board-new-due", a.board.newDueDate, false, &a.board.newDueOpen, &a.board.newDuePicker, true, func(d string) {
						a.board.newDueDate = d
					})
				})
			})

			if known := a.boardKnownRepos(); len(known) > 0 {
				ui.Text(c, tr("关联项目（可选）")).FontSize(13).TextColor(t.TextMuted)
				ui.Row(c).Wrap().Gap(6).Children(func() {
					for _, rp := range known {
						repoPath := rp
						selected := slices.Contains(a.board.newRepos, repoPath)
						btn := ui.ButtonBase(c.Key("board-new-repo-"+repoPath)).
							Label(repoTitle(repoPath)).Padding(4, 9).Radius(5).Cursor(ui.CursorPointer).Transition(colorMotion())
						if selected {
							btn.Background(t.Accent.Alpha(0.12)).Border(1, t.Accent.Alpha(0.5))
						} else {
							btn.Background(t.Surface.Alpha(0.6)).Border(1, t.Border.Alpha(0.7))
						}
						btn.Children(func() {
							ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
								boardRepoDot(c, repoPath, 7)
								lbl := ui.Text(c, repoTitle(repoPath)).FontSize(12.5).SingleLine()
								if selected {
									lbl.TextColor(t.Accent).FontWeight(600)
								} else {
									lbl.TextColor(t.TextMuted)
								}
							})
						})
						if btn.Clicked() {
							if selected {
								a.board.newRepos = slices.DeleteFunc(a.board.newRepos, func(s string) bool { return s == repoPath })
							} else {
								a.board.newRepos = append(a.board.newRepos, repoPath)
							}
						}
					}
				})
			}

			ui.Text(c, tr("任务描述（可选）")).FontSize(12).TextColor(t.TextMuted)
			ui.TextArea(c.Key("board-new-desc"), &a.board.newDesc).
				Placeholder(tr("补充任务背景、目标或验收标准…")).
				Height(80).FillWidth()

			if a.board.newErr != "" {
				ui.Text(c, trMessage(a.board.newErr)).FontSize(12).TextColor(t.Danger)
			}
			if titleInput.Submitted() {
				a.submitNewBoardTask()
			}
		})
	}, func() {
		if ui.Button(c.Key("board-new-cancel"), tr("取消")).Clicked() {
			a.board.showNewModal = false
		}
		if ui.PrimaryButton(c.Key("board-new-submit"), tr("创建")).Disabled(a.board.newSaving || strings.TrimSpace(a.board.newTitle) == "").Clicked() {
			a.submitNewBoardTask()
		}
	})
}

func (a *app) submitNewBoardTask() {
	if a.board.newSaving {
		return
	}
	opts := boardAddTaskOptions{
		Title: a.board.newTitle, Description: a.board.newDesc, Status: a.board.newStatus,
		Priority: a.board.newPriority, DueDate: a.board.newDueDate, Repos: slices.Clone(a.board.newRepos),
	}
	a.board.newSaving = true
	a.changeBoard(func() (boardFile, error) {
		bf, _, err := boardAddTaskWithOptions(opts)
		return bf, err
	}, func(err error) {
		a.board.newSaving = false
		if err != nil {
			a.board.newErr = err.Error()
			return
		}
		a.board.showNewModal, a.board.newPriorityOpen, a.board.newDueOpen = false, false, false
	})
}

func (a *app) linkBoardCommitModal(c *ui.Context) {
	t := c.Theme()
	appModal(c.Key("board-link-commit-modal"), &a.board.showLinkModal, tr("关联提交到任务"), 540, a.board.linkBusy, func() {
		ui.Column(c).FillWidth().Gap(14).Children(func() {
			a.linkCommitRepoField(c)
			refEl := a.linkCommitRefField(c)
			a.linkCommitRecentPicker(c)

			if a.board.linkErr != "" {
				ui.Row(c.Key("board-link-err")).FillWidth().Padding(8, 10).Gap(6).AlignItems(ui.Center).
					Radius(t.Radius).Background(t.Danger.Alpha(0.08)).Border(1, t.Danger.Alpha(0.3)).Children(func() {
					ui.Icon(c, iconAlertCircle).Size(14, 14).TextColor(t.Danger).Shrink(0)
					ui.Text(c, trMessage(a.board.linkErr)).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0)
				})
			}
			if a.board.linkBusy {
				busyRow(c, "board-link-busy", tr("正在读取提交信息…"), "")
			}
			if refEl.Submitted() && !a.board.linkBusy {
				a.submitBoardLinkCommit()
			}
		})
	}, func() {
		if ui.Button(c.Key("board-link-cancel"), tr("取消")).Disabled(a.board.linkBusy).Clicked() {
			a.board.showLinkModal = false
		}
		canSubmit := !a.board.linkBusy && a.board.linkRepo != "" && strings.TrimSpace(a.board.linkRef) != ""
		if ui.PrimaryButton(c.Key("board-link-submit"), tr("关联")).Disabled(!canSubmit).Clicked() {
			a.submitBoardLinkCommit()
		}
	})
}

func (a *app) linkCommitRepoField(c *ui.Context) {
	t := c.Theme()
	known := a.boardKnownRepos()
	ui.Column(c.Key("board-link-repo-sec")).FillWidth().Gap(6).Children(func() {
		ui.Text(c, tr("选择仓库")).FontSize(12.5).FontWeight(500).TextColor(t.TextMuted)
		if len(known) == 0 {
			ui.Row(c).FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Center).
				Radius(t.Radius).Background(t.Surface.Alpha(0.5)).Border(1, t.Border.Alpha(0.6)).Children(func() {
				ui.Icon(c, iconFolderGit).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, tr("请先打开至少一个 Git 仓库")).FontSize(12.5).TextColor(t.TextMuted)
			})
			return
		}
		repoLabel := tr("选择仓库")
		if a.board.linkRepo != "" {
			repoLabel = repoTitle(a.board.linkRepo) + " (" + homePath(a.board.linkRepo) + ")"
		}
		repoBtn := ui.ButtonBase(c.Key("board-link-repo-btn")).
			Label(repoLabel).
			FillWidth().Padding(8, 10).Radius(t.Radius).AlignItems(ui.Center).
			Cursor(ui.CursorPointer).Transition(colorMotion()).Disabled(a.board.linkBusy)
		if a.board.linkRepo != "" {
			repoBtn.Tooltip(homePath(a.board.linkRepo))
		}
		bg := t.Surface
		border := t.Border
		switch {
		case repoBtn.Pressed():
			bg = t.SurfacePressed
		case repoBtn.Hovered():
			bg = t.SurfaceHover
			border = t.TextMuted.Alpha(0.55)
		}
		repoBtn.Background(bg).Border(1, border)
		repoBtn.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if a.board.linkRepo != "" {
					boardRepoDot(c, a.board.linkRepo, 8)
					ui.Text(c, repoTitle(a.board.linkRepo)).FontSize(13).FontWeight(500).TextColor(t.Text).SingleLine().Shrink(0)
					ui.Text(c, homePath(a.board.linkRepo)).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
				} else {
					ui.Icon(c, iconFolderGit).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
					ui.Text(c, tr("选择仓库")).FontSize(13).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
				}
				ui.Icon(c, iconChevronDown).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
			})
		})
		repoBtn.Menu(func(m *ui.Menu) {
			for _, rp := range known {
				repoPath := rp
				if m.Item(repoTitle(repoPath) + " (" + homePath(repoPath) + ")").Checked(a.board.linkRepo == repoPath).Chosen() {
					a.board.linkRepo = repoPath
					a.board.linkErr = ""
				}
			}
		})
	})
}

func (a *app) linkCommitRefField(c *ui.Context) ui.Element {
	t := c.Theme()
	var refEl ui.Element
	ui.Column(c.Key("board-link-ref-sec")).FillWidth().Gap(6).Children(func() {
		ui.Text(c, tr("输入提交哈希或引用")).FontSize(12.5).FontWeight(500).TextColor(t.TextMuted)
		box := ui.Row(c.Key("board-link-ref-box")).FillWidth().Height(34).Padding(0, 6, 0, 10).Gap(6).
			AlignItems(ui.Center).Radius(t.Radius).Background(t.Background).Transition(colorMotion())
		box.Children(func() {
			ui.Icon(c, iconGitCommit).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
			refEl = ui.TextInputBase(c.Key("board-link-ref"), &a.board.linkRef).
				Label(tr("输入提交哈希或引用")).
				Placeholder(tr("例如 HEAD、main 或提交哈希前缀")).
				Bind(&a.board.linkInput).
				FontSize(13).
				Disabled(a.board.linkBusy).
				Grow(1).MinWidth(0).FocusRing(false).AutoFocus()
			if refEl.Changed() {
				a.board.linkErr = ""
			}
			if a.board.linkRef != "" && !a.board.linkBusy {
				if closeIconButton(c, "board-link-ref-clear", tr("清空")).Clicked() {
					a.board.linkRef = ""
					a.board.linkErr = ""
					refEl.Focus()
				}
			}
		})
		border := t.Border
		switch {
		case a.board.linkErr != "":
			border = t.Danger
		case refEl.Focused():
			border = t.Accent
			box.Shadow(0, 0, 0, 2, t.Accent.Alpha(0.15))
		case box.Hovered():
			border = t.TextMuted.Alpha(0.6)
		}
		box.Border(1, border)
	})
	return refEl
}

func (a *app) linkCommitRecentPicker(c *ui.Context) {
	t := c.Theme()
	var matchedTab *repoTab
	for _, tab := range a.tabs {
		if normalizeBoardRepo(tab.path) == normalizeBoardRepo(a.board.linkRepo) {
			matchedTab = tab
			break
		}
	}
	if matchedTab == nil {
		return
	}

	var recent []commit
	for _, cm := range matchedTab.commits {
		if cm.hash == worktreeHash || cm.hash == zeroHash {
			continue
		}
		recent = append(recent, cm)
		if len(recent) >= 6 {
			break
		}
	}
	if len(recent) == 0 {
		return
	}

	linkedHashes := map[string]bool{}
	if idx := findBoardTaskIndex(a.board.file.Tasks, a.board.selectedID); idx >= 0 {
		for _, l := range a.board.file.Tasks[idx].Commits {
			if normalizeBoardRepo(l.Repo) == normalizeBoardRepo(a.board.linkRepo) {
				linkedHashes[strings.ToLower(l.Hash)] = true
			}
		}
	}
	trimmedRef := strings.ToLower(strings.TrimSpace(a.board.linkRef))

	ui.Column(c.Key("board-link-recent-sec")).FillWidth().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("或从近期提交选择")).FontSize(12.5).FontWeight(500).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
			if matchedTab.branch != "" {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
					ui.Icon(c, iconGitBranch).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
					ui.Text(c, matchedTab.branch).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
				})
			}
		})

		ui.Column(c.Key("board-link-recent-list")).FillWidth().Padding(4).Gap(2).
			Radius(8).Background(t.Surface.Alpha(0.45)).Border(1, t.Border).Children(func() {
			for _, cm := range recent {
				hexStr := hashHex(cm.hash)
				short := hexStr[:7]
				selected := trimmedRef != "" && (strings.EqualFold(trimmedRef, hexStr) || (len(trimmedRef) >= 6 && strings.HasPrefix(hexStr, trimmedRef)))
				alreadyLinked := linkedHashes[hexStr]
				subj := cm.subject
				if subj == "" {
					subj = tr("（无说明）")
				}

				tipParts := []string{short}
				if cm.author != "" {
					tipParts = append(tipParts, cm.author)
				}
				if when := cm.whenText(); when != "" {
					tipParts = append(tipParts, when)
				}
				tip := strings.Join(tipParts, " · ") + "\n" + subj
				if alreadyLinked {
					tip += "\n" + tr("已关联到此任务")
				}

				row := ui.ButtonBase(c.Key("board-link-pick-"+hexStr)).
					Label(short+" "+subj).
					Tooltip(tip).
					FillWidth().Padding(6, 8).Radius(6).Justify(ui.Start).
					Cursor(ui.CursorPointer).Transition(colorMotion()).Disabled(a.board.linkBusy)
				switch {
				case selected:
					row.Background(t.Accent.Alpha(0.12)).Border(1, t.Accent.Alpha(0.4))
				case row.Pressed():
					row.Background(t.SurfacePressed).Border(1, ui.Transparent)
				case row.Hovered():
					row.Background(t.SurfaceHover).Border(1, ui.Transparent)
				default:
					row.Border(1, ui.Transparent)
				}

				row.Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						hashBg := t.SurfaceHover
						hashFg := t.TextMuted
						if selected {
							hashBg = t.Accent.Alpha(0.16)
							hashFg = t.Accent
						}
						ui.Row(c).Padding(1, 6).Radius(4).Background(hashBg).Shrink(0).Children(func() {
							ht := ui.Text(c, short).Font("monospace").FontSize(11.5).TextColor(hashFg).SingleLine()
							if selected {
								ht.FontWeight(600)
							}
						})
						subEl := ui.Text(c, subj).FontSize(12.5).SingleLine().Grow(1).MinWidth(0)
						switch {
						case selected:
							subEl.FontWeight(500).TextColor(t.Text)
						case alreadyLinked:
							subEl.TextColor(t.TextMuted)
						default:
							subEl.TextColor(t.Text)
						}
						switch {
						case selected:
							ui.Icon(c, iconCheck).Size(14, 14).TextColor(t.Accent).Shrink(0)
						case alreadyLinked:
							ui.Icon(c, iconLink).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
						case cm.when > 0:
							ui.Text(c, time.Unix(cm.when, 0).Local().Format("01-02")).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Shrink(0)
						}
					})
				})
				if row.Clicked() {
					a.board.linkRef = hexStr
					a.board.linkErr = ""
				}
			}
		})
	})
}

func (a *app) submitBoardLinkCommit() {
	taskID := a.board.selectedID
	repo := a.board.linkRepo
	ref := strings.TrimSpace(a.board.linkRef)
	if taskID == "" || repo == "" || ref == "" {
		return
	}
	a.board.linkBusy = true
	a.board.linkErr = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		link, err := readBoardCommitSnapshot(ctx, repo, ref, "human")
		if err != nil {
			a.post(func() {
				a.board.linkBusy = false
				a.board.linkErr = err.Error()
			})
			return
		}
		a.post(func() {
			if a.closed {
				return
			}
			a.changeBoard(func() (boardFile, error) {
				bf, _, err := boardLinkCommit(taskID, link)
				return bf, err
			}, func(err error) {
				a.board.linkBusy = false
				if err != nil {
					a.board.linkErr = err.Error()
					return
				}
				a.rememberBoardCommit(link)
				a.board.showLinkModal = false
			})
		})
	}()
}

func (a *app) boardSnippetModal(c *ui.Context) {
	snippet := boardRuleSnippet()
	appModal(c.Key("board-snippet-modal"), &a.board.showSnippet, tr("Agent 看板协作规则"), 560, false, func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.TextArea(c.Key("board-snippet-text"), &snippet).Font("monospace").FontSize(12).ReadOnly(true).Height(210).FillWidth()
		})
	}, func() {
		copyTextButton(c, "board-snippet-copy", "复制规则", snippet)
		if ui.PrimaryButton(c.Key("board-snippet-close"), tr("完成")).Clicked() {
			a.board.showSnippet = false
		}
	})
}

// boardDeleteTaskModal asks before a task and everything linked to it disappears;
// there is no undo, so one click in the card menu or drawer must not be enough.
func (a *app) boardDeleteTaskModal(c *ui.Context) {
	open := a.board.deleteTaskID != ""
	if !open {
		return
	}
	id := a.board.deleteTaskID
	t := c.Theme()
	task, ok := boardTaskByID(a.board.file.Tasks, id)
	label := id
	if ok {
		label = task.ID + " " + task.Title
	}
	appModal(c.Key("board-delete-task-modal"), &open, tr("删除看板任务"), 420, false, func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(t.Text)
			ui.Text(c, tr("任务和它的关联提交、备注会一起删除，无法恢复。")).FontSize(12.5).TextColor(t.TextMuted)
		})
	}, func() {
		if ui.Button(c.Key("board-delete-cancel"), tr("取消")).Clicked() {
			open = false
		}
		if dangerButton(c, "board-delete-confirm", tr("确认删除")).Clicked() {
			open = false
			a.changeBoard(func() (boardFile, error) { return boardDeleteTask(id) }, func(err error) {
				if err != nil {
					a.setBanner(err.Error(), "")
				} else {
					a.forgetBoardTaskDraft(id)
				}
			})
		}
	})
	if !open {
		a.board.deleteTaskID = ""
	}
}

// boardDeleteNoteModal asks before removing a note from a task; notes have no
// undo, so clicking the trash icon opens a confirmation dialog first.
func (a *app) boardDeleteNoteModal(c *ui.Context) {
	open := a.board.deleteNoteTaskID != "" && a.board.deleteNoteID != ""
	if !open {
		return
	}
	taskID := a.board.deleteNoteTaskID
	noteID := a.board.deleteNoteID
	task, ok := boardTaskByID(a.board.file.Tasks, taskID)
	var note boardNote
	found := false
	if ok {
		for _, n := range task.Notes {
			if n.ID == noteID {
				note = n
				found = true
				break
			}
		}
	}
	if !found {
		a.board.deleteNoteTaskID = ""
		a.board.deleteNoteID = ""
		return
	}
	t := c.Theme()
	preview := strings.Join(strings.Fields(note.Text), " ")
	if runes := []rune(preview); len(runes) > 120 {
		preview = string(runes[:120]) + "…"
	}
	appModal(c.Key("board-delete-note-modal"), &open, tr("删除备注"), 420, false, func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			if preview != "" {
				ui.Text(c, preview).FontSize(13).FontWeight(500).TextColor(t.Text)
			}
			ui.Text(c, tr("备注删除后无法恢复。")).FontSize(12.5).TextColor(t.TextMuted)
		})
	}, func() {
		if ui.Button(c.Key("board-delete-note-cancel"), tr("取消")).Clicked() {
			open = false
		}
		if dangerButton(c, "board-delete-note-confirm", tr("确认删除")).Clicked() {
			open = false
			a.changeBoard(func() (boardFile, error) {
				bf, _, err := boardDeleteNote(taskID, noteID)
				return bf, err
			}, func(err error) {
				if err != nil {
					a.setBanner(err.Error(), "")
				} else if a.board.editingNoteID == noteID {
					a.board.editingNoteID, a.board.editingNoteText = "", ""
				}
			})
		}
	})
	if !open {
		a.board.deleteNoteTaskID = ""
		a.board.deleteNoteID = ""
	}
}

func (a *app) linkCommitToBoardTask(tab *repoTab, cm commit, taskID string) {
	if a.dockerActionBlocked(tab) {
		return
	}
	if tab == nil || cm.hash == zeroHash || cm.hash == worktreeHash {
		return
	}
	link := boardCommitFromRow(tab, cm)
	a.changeBoard(func() (boardFile, error) {
		bf, _, err := boardLinkCommit(taskID, link)
		return bf, err
	}, func(err error) {
		if err != nil {
			a.setBanner(err.Error(), "")
			return
		}
		a.rememberBoardCommit(link)
		a.pendingToast = fmt.Sprintf("已关联到 %s", taskID)
	})
}

func boardCommitFromRow(tab *repoTab, cm commit) boardCommitLink {
	prov := tab.provenance.Get(cm.prov)
	return boardCommitLink{
		Repo:    canonicalRepoDir(tab.path),
		Hash:    hashHex(cm.hash),
		Subject: cm.subject,
		Branch:  tab.branch,
		Agent:   prov.Agent,
		Model:   prov.Model,
		AddedBy: "human",
		AddedAt: time.Now().UTC(),
	}
}

func (a *app) rememberBoardCommit(link boardCommitLink) {
	if a.board.commitValid == nil {
		a.board.commitValid = map[string]bool{}
	}
	a.board.commitValid[boardCommitCacheKey(link.Repo, link.Hash)] = true
}

func (a *app) createBoardTaskWithCommit(tab *repoTab, cm commit) {
	if a.dockerActionBlocked(tab) {
		return
	}
	if tab == nil || cm.hash == zeroHash || cm.hash == worktreeHash {
		return
	}
	title := strings.TrimSpace(cm.subject)
	if title == "" {
		title = hashHex(cm.hash)[:7]
	}
	link := boardCommitFromRow(tab, cm)
	opts := boardAddTaskOptions{Title: title, Status: boardDoing, Repos: []string{tab.path}, Commit: &link}
	var created boardTask
	a.changeBoard(func() (boardFile, error) {
		bf, task, err := boardAddTaskWithOptions(opts)
		created = task
		return bf, err
	}, func(err error) {
		if err != nil {
			a.setBanner(err.Error(), "")
			return
		}
		a.rememberBoardCommit(link)
		a.pendingToast = fmt.Sprintf("已关联到 %s", created.ID)
	})
}

func boardBackupReasonLabel(reason string) string {
	switch reason {
	case "delete":
		return tr("删除前")
	case "archive":
		return tr("归档前")
	case "import":
		return tr("导入前")
	case "restore":
		return tr("恢复前")
	case "switch-merge", "switch-overwrite":
		return tr("切换目录前")
	default:
		return tr("自动快照")
	}
}

func (a *app) startBoardExport() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	defaultName := fmt.Sprintf("xylring-board-%s.json", time.Now().Format("20060102"))
	go func() {
		outPath, err := saveBoardExportDialog(win, defaultName)
		if err != nil || strings.TrimSpace(outPath) == "" {
			a.post(func() {
				a.picking = false
				if err != nil {
					a.setBanner(trimMsg(err.Error()), "")
				}
			})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		bundle, expErr := boardExport(ctx, boardExportOptions{IncludeArchived: true})
		if expErr == nil {
			expErr = writeBoardExportFile(outPath, bundle)
		}
		a.post(func() {
			a.picking = false
			if expErr != nil {
				a.setBanner(trimMsg(expErr.Error()), "")
				return
			}
			a.pendingToast = fmt.Sprintf("已导出 %d 个任务", len(bundle.Tasks))
		})
	}()
}

func (a *app) startBoardImport() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	known := a.boardKnownRepos()
	go func() {
		paths, err := openBoardImportDialog(win)
		if err != nil || len(paths) == 0 {
			a.post(func() {
				a.picking = false
				if err != nil {
					a.setBanner(trimMsg(err.Error()), "")
				}
			})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		plan, planErr := buildBoardImportPlan(ctx, paths[0], known)
		a.post(func() {
			a.picking = false
			if planErr != nil {
				a.setBanner(trimMsg(planErr.Error()), "")
				return
			}
			a.board.importPlan = plan
			a.board.importModeIdx = 0
			a.board.importErr = ""
			a.board.importBusy = false
			a.board.showImportModal = true
		})
	}()
}

func (a *app) executeBoardImport() {
	if a.board.importBusy {
		return
	}
	mode := boardImportMerge
	if a.board.importModeIdx == 1 {
		mode = boardImportReplace
	}
	repoMap := map[string]string{}
	for _, m := range a.board.importPlan.RepoMappings {
		if strings.TrimSpace(m.ChosenPath) != "" && normalizeBoardRepo(m.ChosenPath) != normalizeBoardRepo(m.OldPath) {
			repoMap[m.OldPath] = m.ChosenPath
		}
	}
	a.board.importBusy = true
	a.board.importErr = ""
	srcPath := a.board.importPlan.SourcePath
	var bf boardFile
	var res boardImportResult
	var err error
	a.runBoardJob(func() {
		bf, res, err = boardImport(srcPath, boardImportOptions{Mode: mode, RepoMap: repoMap})
	}, func() {
		a.board.importBusy = false
		if err != nil {
			a.board.importErr = err.Error()
			return
		}
		a.resetBoardAfterReplace()
		a.applyBoardFile(bf, nil)
		a.board.loaded = true
		a.board.showImportModal = false
		a.pendingToast = fmt.Sprintf("已导入 %d 个任务", res.ImportedTasks)
	})
}

func (a *app) pickRemapRepoDir(idx int) {
	if a.picking || a.win == nil || idx < 0 || idx >= len(a.board.importPlan.RepoMappings) {
		return
	}
	a.picking = true
	win := a.win
	defDir := a.board.importPlan.RepoMappings[idx].ChosenPath
	go func() {
		paths, err := openBoardDirDialog(win, defDir)
		a.post(func() {
			a.picking = false
			if err != nil {
				a.board.importErr = trimMsg(err.Error())
				return
			}
			if len(paths) == 0 || idx >= len(a.board.importPlan.RepoMappings) {
				return
			}
			chosen := normalizeBoardRepo(paths[0])
			if !isBoardRepoAvailable(chosen) {
				a.board.importErr = "所选目录不是有效的 Git 仓库"
				return
			}
			a.board.importErr = ""
			a.board.importPlan.RepoMappings[idx].ChosenPath = chosen
			a.board.importPlan.RepoMappings[idx].Status = "matched"
		})
	}()
}

func (a *app) openBoardBackupsModal() {
	items, err := listBoardBackups()
	if err != nil {
		a.board.backups = nil
		a.board.backupsErr = err.Error()
	} else {
		a.board.backups = items
		a.board.backupsErr = ""
	}
	a.board.showBackupsModal = true
}

func (a *app) restoreBoardFromBackup(backupPath string) {
	if a.board.storageBusy {
		return
	}
	a.board.storageBusy = true
	var bf boardFile
	var err error
	a.runBoardJob(func() { bf, err = boardRestoreBackup(backupPath) }, func() {
		a.board.storageBusy = false
		if err != nil {
			a.board.backupsErr = err.Error()
			return
		}
		a.resetBoardAfterReplace()
		a.applyBoardFile(bf, nil)
		a.board.loaded = true
		a.board.showBackupsModal = false
		a.pendingToast = fmt.Sprintf("已恢复 %d 个任务", len(bf.Tasks))
	})
}

func (a *app) openBoardStorageModal() {
	a.refreshBoardStorageInfo()
	a.board.storageErr = ""
	a.board.showStorageModal = true
}

func (a *app) openBoardFolder() {
	info := a.currentBoardStorageInfo()
	if info.ActiveDir == "" {
		return
	}
	_ = os.MkdirAll(info.ActiveDir, 0o755)
	if err := openFolderInFileManager(info.ActiveDir); err != nil {
		a.setBanner(trimMsg(err.Error()), "")
	}
}

func (a *app) pickBoardStorageDir() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	info := a.currentBoardStorageInfo()
	go func() {
		paths, err := openBoardDirDialog(win, info.ActiveDir)
		a.post(func() {
			a.picking = false
			if err != nil {
				a.board.storageErr = trimMsg(err.Error())
				return
			}
			if len(paths) == 0 {
				return
			}
			a.beginSwitchBoardDir(paths[0])
		})
	}()
}

func (a *app) beginSwitchBoardDir(targetDir string) {
	clean := filepath.Clean(strings.TrimSpace(targetDir))
	info := a.currentBoardStorageInfo()
	if clean == "" || clean == filepath.Clean(info.ActiveDir) {
		return
	}
	hasBoard, count, err := boardProbeDir(clean)
	if err != nil {
		a.board.storageErr = err.Error()
		return
	}
	if hasBoard {
		a.board.switchTargetDir = clean
		a.board.switchTargetCount = count
		a.board.switchDirErr = ""
		a.board.showSwitchDirModal = true
		return
	}
	a.applySwitchBoardDir(clean, boardSwitchAuto)
}

func (a *app) applySwitchBoardDir(targetDir string, strategy boardDirSwitchStrategy) {
	if a.board.storageBusy {
		return
	}
	a.board.storageBusy = true
	var bf boardFile
	var info boardStorageInfo
	var err error
	a.runBoardJob(func() {
		bf, err = boardSwitchStorageDir(targetDir, strategy)
		if err == nil {
			info, _ = boardLocationInfo()
		}
	}, func() {
		a.board.storageBusy = false
		if err != nil {
			a.board.switchDirErr = err.Error()
			a.board.storageErr = err.Error()
			return
		}
		a.board.showSwitchDirModal = false
		a.board.showStorageModal = false
		a.board.storageErr = ""
		a.board.switchDirErr = ""
		if info.ActiveDir != "" {
			a.board.storageInfo, a.board.storageLoaded = info, true
		}
		a.resetBoardAfterReplace()
		a.applyBoardFile(bf, nil)
		a.board.loaded = true
		a.pendingToast = "已切换看板数据目录"
	})
}

func (a *app) boardImportModal(c *ui.Context) {
	if !a.board.showImportModal {
		return
	}
	t := c.Theme()
	plan := &a.board.importPlan
	appModal(c.Key("board-import-modal"), &a.board.showImportModal, tr("导入看板备份"), 520, a.board.importBusy, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconFileText).Size(15, 15).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, filepath.Base(plan.SourcePath)).FontSize(13).FontWeight(500).TextColor(t.Text).
					SingleLine().Grow(1).MinWidth(0).Tooltip(plan.SourcePath)
				ui.Text(c, fmt.Sprintf(tr("%d 个任务"), plan.TotalTasks)).FontSize(12.5).TextColor(t.TextMuted).Shrink(0)
			})
			ui.Segmented(c.Key("board-import-mode"), &a.board.importModeIdx, tr("合并到现有看板"), tr("覆盖现有看板")).FillWidth()
			if a.board.importModeIdx == 0 && len(plan.ConflictIDs) > 0 {
				ui.Text(c, fmt.Sprintf(tr("%d 个任务编号冲突，导入时将自动重新编号。"), len(plan.ConflictIDs))).
					FontSize(12.5).TextColor(t.TextMuted)
			}
			if a.board.importModeIdx == 1 {
				ui.Text(c, tr("覆盖将替换当前所有任务，操作前会自动备份。")).FontSize(12.5).TextColor(t.Danger)
			}
			hasUnmatched := false
			for _, m := range plan.RepoMappings {
				if m.Status != "matched" {
					hasUnmatched = true
					break
				}
			}
			if hasUnmatched {
				ui.Divider(c)
				ui.Text(c, tr("仓库路径映射")).FontSize(12.5).FontWeight(500).TextColor(t.Text)
				for i := range plan.RepoMappings {
					idx := i
					m := plan.RepoMappings[idx]
					if m.Status == "matched" {
						continue
					}
					ui.Row(c.Key(fmt.Sprintf("remap-repo-%d", idx))).FillWidth().Padding(8, 10).Gap(8).
						AlignItems(ui.Center).Radius(t.Radius).Background(t.Surface).Border(1, t.Border).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, repoTitle(m.OldPath)).FontSize(12.5).FontWeight(500).TextColor(t.Text).SingleLine()
								if m.Status == "suggested" {
									chip(c, "suggested", tr("已自动匹配"), chipStyle{Text: t.Accent, Background: t.Accent.Alpha(0.12), FontSize: 10})
								} else {
									chip(c, "missing", tr("未在本机找到"), chipStyle{Text: t.Warning, Background: t.Warning.Alpha(0.12), FontSize: 10})
								}
							})
							sub := m.OldPath
							if m.ChosenPath != "" {
								sub = homePath(m.ChosenPath)
							}
							ui.Text(c, sub).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Tooltip(sub)
						})
						if ui.Button(c.Key(fmt.Sprintf("remap-pick-%d", idx)), tr("选择目录…")).Padding(3, 8).FontSize(12).Disabled(a.picking).Clicked() {
							a.pickRemapRepoDir(idx)
						}
					})
				}
			}
			if a.board.importErr != "" {
				ui.Text(c, trMessage(a.board.importErr)).FontSize(12).TextColor(t.Danger)
			}
		})
	}, func() {
		if ui.Button(c.Key("board-import-cancel"), tr("取消")).Disabled(a.board.importBusy).Clicked() {
			a.board.showImportModal = false
		}
		if a.board.importModeIdx == 1 {
			if dangerButton(c, "board-import-confirm", tr("确认覆盖")).Disabled(a.board.importBusy).Clicked() {
				a.executeBoardImport()
			}
		} else {
			if ui.PrimaryButton(c.Key("board-import-confirm"), tr("确认导入")).Disabled(a.board.importBusy).Clicked() {
				a.executeBoardImport()
			}
		}
	})
}

func (a *app) boardBackupRestoreModal(c *ui.Context) {
	if !a.board.showBackupsModal {
		return
	}
	t := c.Theme()
	appModal(c.Key("board-backups-modal"), &a.board.showBackupsModal, tr("恢复自动备份"), 500, a.board.storageBusy, func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			if a.board.backupsErr != "" {
				ui.Text(c, trMessage(a.board.backupsErr)).FontSize(12).TextColor(t.Danger)
			}
			if len(a.board.backups) == 0 {
				ui.Column(c).FillWidth().Padding(18).Center().Children(func() {
					ui.Text(c, tr("暂无自动备份")).FontSize(13).TextColor(t.TextMuted)
				})
				return
			}
			for i, b := range a.board.backups {
				backup := b
				ui.Row(c.Key(fmt.Sprintf("backup-item-%d", i))).FillWidth().Padding(9, 12).Gap(10).
					AlignItems(ui.Center).Radius(t.Radius).Background(t.Surface).Border(1, t.Border).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, backup.CreatedAt.Local().Format("2006-01-02 15:04:05")).FontSize(13).FontWeight(500).TextColor(t.Text)
							chip(c, "reason", boardBackupReasonLabel(backup.Reason), chipStyle{Text: t.TextMuted, Background: t.Border.Alpha(0.4), FontSize: 10})
						})
						ui.Text(c, fmt.Sprintf(tr("%d 个任务"), backup.TaskCount)).FontSize(12).TextColor(t.TextMuted)
					})
					if ui.Button(c.Key(fmt.Sprintf("backup-restore-%d", i)), tr("恢复")).Padding(4, 10).FontSize(12).Clicked() {
						a.restoreBoardFromBackup(backup.Path)
					}
				})
			}
		})
	}, func() {
		if ui.Button(c.Key("board-backups-close"), tr("完成")).Clicked() {
			a.board.showBackupsModal = false
		}
	})
}

func (a *app) boardStorageModal(c *ui.Context) {
	if !a.board.showStorageModal {
		return
	}
	t := c.Theme()
	info := a.currentBoardStorageInfo()
	appModal(c.Key("board-storage-modal"), &a.board.showStorageModal, tr("看板数据目录"), 480, false, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Row(c).FillWidth().Padding(10, 12).Gap(8).AlignItems(ui.Center).
				Radius(t.Radius).Background(t.Surface).Border(1, t.Border).Children(func() {
				ui.Icon(c, iconFolder).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, homePath(info.ActiveDir)).FontSize(12.5).TextColor(t.Text).
					SingleLine().Grow(1).MinWidth(0).Tooltip(info.ActiveDir)
				if info.IsCustom {
					chip(c, "custom", tr("自定义"), chipStyle{Text: t.Accent, Background: t.Accent.Alpha(0.12), FontSize: 10})
				}
			})
			if info.FromEnv {
				ui.Text(c, tr("当前通过环境变量 XYLRING_BOARD_DIR 指定目录。")).FontSize(12).TextColor(t.TextMuted)
			}
			if a.board.storageErr != "" {
				ui.Text(c, trMessage(a.board.storageErr)).FontSize(12).TextColor(t.Danger)
			}
		})
	}, func() {
		if ui.Button(c.Key("board-storage-open"), tr("打开目录")).Clicked() {
			a.openBoardFolder()
		}
		if info.IsCustom {
			if ui.Button(c.Key("board-storage-reset"), tr("恢复默认")).Disabled(info.FromEnv || a.picking).Clicked() {
				a.beginSwitchBoardDir(info.DefaultDir)
			}
		}
		if ui.PrimaryButton(c.Key("board-storage-change"), tr("更改目录…")).Disabled(info.FromEnv || a.picking).Clicked() {
			a.pickBoardStorageDir()
		}
	})
}

func (a *app) boardSwitchDirModal(c *ui.Context) {
	if !a.board.showSwitchDirModal {
		return
	}
	t := c.Theme()
	appModal(c.Key("board-switch-dir-modal"), &a.board.showSwitchDirModal, tr("目标目录已有看板数据"), 480, a.board.storageBusy, func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, homePath(a.board.switchTargetDir)).FontSize(12.5).FontWeight(500).TextColor(t.Text).
				SingleLine().Tooltip(a.board.switchTargetDir)
			ui.Text(c, fmt.Sprintf(tr("该目录中已有 %d 个任务，请选择处理方式。"), a.board.switchTargetCount)).
				FontSize(12.5).TextColor(t.TextMuted)
			if a.board.switchDirErr != "" {
				ui.Text(c, trMessage(a.board.switchDirErr)).FontSize(12).TextColor(t.Danger)
			}
		})
	}, func() {
		if ui.Button(c.Key("board-switch-cancel"), tr("取消")).Clicked() {
			a.board.showSwitchDirModal = false
		}
		if dangerButton(c, "board-switch-overwrite", tr("覆盖目标")).Clicked() {
			a.applySwitchBoardDir(a.board.switchTargetDir, boardSwitchOverwriteTarget)
		}
		if ui.Button(c.Key("board-switch-merge"), tr("合并过去")).Clicked() {
			a.applySwitchBoardDir(a.board.switchTargetDir, boardSwitchMergeTarget)
		}
		if ui.PrimaryButton(c.Key("board-switch-use"), tr("使用目标数据")).Clicked() {
			a.applySwitchBoardDir(a.board.switchTargetDir, boardSwitchUseTarget)
		}
	})
}
