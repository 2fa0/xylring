package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type fileDiffTab struct {
	id               int
	file             fileChange
	from, to         [20]byte
	worktree         bool
	openedAt         time.Time
	ignoreWhitespace bool
	source           *fileDiffSource
	loadGen          int
	diff             *editor.DiffState
	simple           *simpleDiff
	imageDiff        *imageDiff
	imageViewMode    int
	imageFitActual   bool
	note             string
	fullFile         bool
	fullRev          string
	fullContent      editor.State
	fullImage        *imageInfo
	backing          string
	cachePending     bool
	cacheCancel      context.CancelFunc
	evicted          bool
	closed           bool
}

func (a *app) openFileTabAt(t *repoTab, i int) {
	if t.compareLoading || i < 0 || i >= len(t.compareFiles) {
		return
	}
	if t.fileSel != i {
		t.fileSel = i
		t.openFileTabAfterLoad = true
		a.loadFileDiff(t, i)
		return
	}
	if t.diffLoading {
		t.openFileTabAfterLoad = true
		return
	}
	a.openFileTab(t)
}

func (a *app) finishOpenFileTab(t *repoTab) {
	if !t.openFileTabAfterLoad {
		return
	}
	t.openFileTabAfterLoad = false
	a.openFileTab(t)
}

func (a *app) openWorktreeFileTab(t *repoTab, f fileChange) {
	for i, tab := range t.fileTabs {
		if tab.file.path == f.path && tab.worktree {
			t.contentTab = i + 1
			t.revealFileTab = true
			return
		}
	}
	t.nextFileTabID++
	newTab := &fileDiffTab{
		id:               t.nextFileTabID,
		file:             f,
		from:             t.head,
		to:               zeroHash,
		worktree:         true,
		ignoreWhitespace: a.diffIgnoreWhitespace,
		openedAt:         time.Now(),
		note:             "正在读取改动…",
	}
	t.fileTabs = append(t.fileTabs, newTab)
	t.contentTab = len(t.fileTabs)
	t.revealFileTab = true

	path, head, ignoreWhitespace := t.path, t.head, a.diffIgnoreWhitespace
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fromRev := ""
		if head != zeroHash {
			fromRev = hashHex(head)
		}
		source, err := readFileDiffSource(ctx, path, fromRev, "", f, true)
		var diff *editor.DiffState
		var simple *simpleDiff
		var imgDiff *imageDiff
		var note string
		if err == nil {
			diff, simple, imgDiff, note, err = source.prepare(ctx, f.path, ignoreWhitespace && f.status != "A" && f.status != "D")
		}
		if err != nil {
			note = err.Error()
		}
		a.post(func() {
			if t.closed || newTab.closed {
				return
			}
			newTab.source = source
			newTab.diff, newTab.simple, newTab.imageDiff, newTab.note = diff, simple, imgDiff, note
			a.trimInactiveHistory()
		})
	}()
}

func (a *app) openFileTab(t *repoTab) {
	t.openFileTabAfterLoad = false
	if t.compareLoading || t.diffLoading || t.fileSel < 0 || t.fileSel >= len(t.compareFiles) {
		return
	}
	f := t.compareFiles[t.fileSel]
	for i, tab := range t.fileTabs {
		if tab.file == f && tab.from == t.diffFrom && tab.to == t.diffTo && tab.worktree == t.worktree {
			// A refreshed worktree may have different bytes for the same path.
			if !t.worktree || (tab.diff == t.diff && tab.imageDiff == t.imageDiff && tab.note == t.diffNote) {
				t.contentTab = i + 1
				t.revealFileTab = true
				return
			}
		}
	}
	t.nextFileTabID++
	// The loaded buffers are read-only. Retain this result and its reading
	// position; selecting another file or refreshing replaces the preview.
	t.fileTabs = append(t.fileTabs, &fileDiffTab{
		id: t.nextFileTabID, file: f, from: t.diffFrom, to: t.diffTo,
		worktree: t.worktree, openedAt: time.Now(),
		ignoreWhitespace: a.diffIgnoreWhitespace, source: t.diffSource, diff: t.diff, simple: t.simpleDiff, imageDiff: t.imageDiff,
		imageViewMode: t.imageViewMode, imageFitActual: t.imageFitActual, note: t.diffNote,
	})
	t.contentTab = len(t.fileTabs)
	t.revealFileTab = true
}

func (a *app) closeFileTab(t *repoTab, index int) {
	if index < 0 || index >= len(t.fileTabs) {
		return
	}
	t.fileTabs[index].close()
	t.fileTabs = slices.Delete(t.fileTabs, index, index+1)
	switch {
	case t.contentTab == index+1:
		t.contentTab = 0
		t.revealFileTab = true
	case t.contentTab > index+1:
		t.contentTab--
	}
}

func (a *app) closeOtherFileTabs(t *repoTab, keepIndex int) {
	if keepIndex < 0 || keepIndex >= len(t.fileTabs) {
		return
	}
	target := t.fileTabs[keepIndex]
	for i, tab := range t.fileTabs {
		if i != keepIndex {
			tab.close()
		}
	}
	t.fileTabs = []*fileDiffTab{target}
	t.contentTab = 1
	t.revealFileTab = true
}

func (a *app) closeRightFileTabs(t *repoTab, fromIndex int) {
	if fromIndex < 0 || fromIndex >= len(t.fileTabs)-1 {
		return
	}
	for _, tab := range t.fileTabs[fromIndex+1:] {
		tab.close()
	}
	t.fileTabs = slices.Delete(t.fileTabs, fromIndex+1, len(t.fileTabs))
	if t.contentTab > fromIndex+1 {
		t.contentTab = fromIndex + 1
		t.revealFileTab = true
	}
}

func (a *app) closeAllFileTabs(t *repoTab) {
	for _, tab := range t.fileTabs {
		tab.close()
	}
	t.fileTabs = nil
	t.contentTab = 0
	t.revealFileTab = true
}

func (a *app) openFileHistoryForTab(t *repoTab, tab *fileDiffTab) {
	if tab == nil || t.query.filePath != "" {
		return
	}
	at := tab.to
	if at == zeroHash {
		if tab.worktree {
			at = t.head
		} else {
			at = tab.from
		}
	}
	if at == zeroHash {
		return
	}
	t.contentTab = 0
	t.revealFileTab = true
	t.fileBack = t.location()
	q := historyQuery{filePath: tab.file.path, fileAt: at}
	a.changeHistory(t, q)
}

func openWithDefaultEditor(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		cmd := exec.Command("cmd", "/c", "start", "", path)
		hideConsoleWindow(cmd)
		return cmd.Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

func revealInFileManager(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	case "windows":
		return exec.Command("explorer", "/select,", path).Start()
	default:
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			return exec.Command("xdg-open", path).Start()
		}
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}

func openInTerminal(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-a", "Terminal", abs).Start()
	case "windows":
		cmd := exec.Command("cmd", "/c", "start", "cmd.exe")
		cmd.Dir = abs
		return cmd.Start()
	default:
		term := os.Getenv("TERMINAL")
		if term != "" {
			cmd := exec.Command(term)
			cmd.Dir = abs
			return cmd.Start()
		}
		for _, name := range []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal", "alacritty", "xterm"} {
			if p, err := exec.LookPath(name); err == nil {
				cmd := exec.Command(p)
				cmd.Dir = abs
				return cmd.Start()
			}
		}
		return errors.New("未找到可用的终端应用")
	}
}

func (a *app) closeCurrentTab() {
	if t := a.activeTab(); t != nil {
		if t.contentTab > 0 {
			a.closeFileTab(t, t.contentTab-1)
		} else {
			a.closeTab(a.active)
		}
	}
}

func (tab *fileDiffTab) labels() (string, string) {
	if tab.fullFile {
		return "", tab.fullRev
	}
	from, to := shortHash(tab.from), shortHash(tab.to)
	if tab.from == zeroHash {
		from = "空树"
	}
	if tab.worktree {
		to = "工作区快照 · " + tab.openedAt.Format("15:04:05")
	}
	return from, to
}

func (a *app) fileTabBar(c *ui.Context, t *repoTab) {
	if len(t.fileTabs) == 0 {
		return
	}
	labels := []string{"提交历史"}
	for _, tab := range t.fileTabs {
		name := filepath.Base(tab.file.path)
		if tab.fullFile {
			name += " (" + tab.fullRev + ")"
		}
		labels = append(labels, name)
	}
	theme := c.Theme()
	ui.Row(c.Key(fmt.Sprintf("file-tabs-%d", t.id))).
		Padding(0, 12).
		AlignItems(ui.Center).
		Shrink(0).
		BorderWidth(0, 0, 1, 0).
		BorderColor(theme.Border).
		Children(func() {
			ui.ScrollHorizontal(c.Key("file-tab-scroll")).
				Scrollbars(false).
				TrackScroll(&t.fileTabsScroll).
				HandleInput(func(ev ui.InputEvent) bool {
					if ev.Kind == ui.InputScroll && ev.DY != 0 && ev.DX == 0 && (ev.Mods == 0 || ev.Mods == ui.Shift) {
						if ev.Precise {
							t.fileTabsScroll.X = max(0, min(t.fileTabsScroll.X+ev.DY, t.fileTabsScroll.MaxX))
							t.fileTabsScrollTarget = t.fileTabsScroll.X
						} else {
							if !t.fileTabsScroll.Scrolling() {
								t.fileTabsScrollTarget = t.fileTabsScroll.X
							}
							t.fileTabsScrollTarget = max(0, min(t.fileTabsScrollTarget+ev.DY, t.fileTabsScroll.MaxX))
							t.fileTabsScroll.ScrollTo(t.fileTabsScrollTarget, 0, ui.Smooth)
						}
						return true
					}
					return false
				}).
				Grow(1).
				MinWidth(0).
				Height(40).
				Children(func() {
					tabs := ui.TabsBase(c.Key("file-tab-choices"), &t.contentTab, len(labels))
					selected := t.contentTab
					var action func()
					tabs.List.Gap(4).Children(func() {
						for i, label := range labels {
							item := tabs.Tab(i).Padding(6, 8).Gap(4).AlignItems(ui.Center).MaxWidth(280).Radius(4).TextColor(theme.TextMuted).Transition(colorMotion())
							if i > 0 && i-1 < len(t.fileTabs) {
								fileIdx := i - 1
								tab := t.fileTabs[fileIdx]
								_, to := tab.labels()
								item.Label(label + " · " + to).Tooltip(tab.file.path + " · " + to)
								item.ContextMenu(func(m *ui.Menu) {
									fullPath := repoFilePath(t.path, tab.file.path, true)
									if m.Item("关闭").Shortcut(commandModifier(), ui.KeyW).Chosen() {
										action = func() { a.closeFileTab(t, fileIdx) }
									}
									if len(t.fileTabs) > 1 {
										if m.Item("关闭其他").Chosen() {
											action = func() { a.closeOtherFileTabs(t, fileIdx) }
										}
									}
									if fileIdx < len(t.fileTabs)-1 {
										if m.Item("关闭右侧标签页").Chosen() {
											action = func() { a.closeRightFileTabs(t, fileIdx) }
										}
									}
									if m.Item("全部关闭").Chosen() {
										action = func() { a.closeAllFileTabs(t) }
									}

									m.Separator()

									if m.Item("复制相对路径").Chosen() {
										copyFilePath(c, t.path, tab.file.path, 0)
									}
									if m.Item("复制绝对路径").Chosen() {
										copyFilePath(c, t.path, tab.file.path, ui.Alt)
									}

									m.Separator()

									if t.query.filePath == "" {
										if m.Item("查看文件历史").Chosen() {
											action = func() { a.openFileHistoryForTab(t, tab) }
										}
									}
									if m.Item("使用默认编辑器打开").Chosen() {
										if _, err := os.Stat(fullPath); err != nil {
											c.Toast("工作区中不存在此文件")
										} else if err := openWithDefaultEditor(fullPath); err != nil {
											c.Toast("打开失败: " + err.Error())
										}
									}
									revealName := "在访达中显示"
									if runtime.GOOS != "darwin" {
										revealName = "在文件管理器中显示"
									}
									if m.Item(revealName).Chosen() {
										if _, err := os.Stat(fullPath); err != nil {
											c.Toast("工作区中不存在此文件")
										} else if err := revealInFileManager(fullPath); err != nil {
											c.Toast("定位失败: " + err.Error())
										}
									}
								})
							} else if i == 0 {
								if len(t.fileTabs) > 0 {
									item.ContextMenu(func(m *ui.Menu) {
										if m.Item("关闭全部文件标签页").Chosen() {
											action = func() { a.closeAllFileTabs(t) }
										}
									})
								}
							}
							if i == t.contentTab {
								item.TextColor(theme.Text)
								if t.revealFileTab || t.shownFileTab != selected {
									item.ScrollIntoView()
								}
							} else if item.Hovered() {
								item.Background(theme.SurfaceHover.Alpha(0.6)).TextColor(theme.Text)
							}
							item.Children(func() {
								if i == t.contentTab {
									fileTabIndicator(c, t)
								}
								ui.Text(c, label).SingleLine().MinWidth(0)
								if i > 0 && i-1 < len(t.fileTabs) {
									fileIdx := i - 1
									id := t.fileTabs[fileIdx].id
									if closeIconButton(c, fmt.Sprintf("close-file-tab-%d", id), "关闭标签页").Clicked() {
										action = func() { a.closeFileTab(t, fileIdx) }
									}
								}
							})
						}
					})
					t.revealFileTab = false
					t.shownFileTab = selected
					if action != nil {
						action()
					}
				})
		})
}

func (a *app) fileTabPane(c *ui.Context, t *repoTab, tab *fileDiffTab) {
	if tab.evicted {
		a.restoreFileTab(t, tab)
	}
	if tab.fullFile {
		a.fullFileTabPane(c, t, tab)
		return
	}
	fileIdx := -1
	if tab.from == t.diffFrom && tab.to == t.diffTo && tab.worktree == t.worktree {
		for i, f := range t.compareFiles {
			if f.path == tab.file.path {
				fileIdx = i
				break
			}
		}
	}
	ui.Column(c.Key(fmt.Sprintf("file-tab-%d-%d", t.id, tab.id))).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			if iconButton(c, "back-to-history", "返回提交历史", iconBack).Shrink(0).Clicked() {
				t.contentTab = 0
				t.revealFileTab = true
			}
			pathBreadcrumb(c, "file-tab-path", tab.file.fullPath())
			if fileIdx >= 0 && len(t.compareFiles) > 1 {
				ui.Text(c, fmt.Sprintf("%d / %d", fileIdx+1, len(t.compareFiles))).FontSize(12).TextColor(c.Theme().TextMuted).Shrink(0)
				if ui.Button(c.Key("prev-file"), "上一文件").Disabled(fileIdx <= 0).Shrink(0).Clicked() {
					a.openFileTabAt(t, fileIdx-1)
				}
				if ui.Button(c.Key("next-file"), "下一文件").Disabled(fileIdx >= len(t.compareFiles)-1).Shrink(0).Clicked() {
					a.openFileTabAt(t, fileIdx+1)
				}
			}
			copy := iconButton(c, "copy-tab-path", "复制路径", iconCopy).Tooltip(copyPathTip(c))
			if copy.Clicked() {
				copyFilePath(c, t.path, tab.file.path, copy.ClickModifiers())
			}
			ui.Box(c).Grow(1)
			if tab.imageDiff != nil {
				a.imageDiffControls(c, tab.id, tab.imageDiff, &tab.imageViewMode, &tab.imageFitActual)
			} else {
				a.diffLayoutToggle(c)
				tabIgnoreBtn := ui.ButtonBase(c.Key(fmt.Sprintf("tab-diff-ignore-whitespace-%d", tab.id))).Height(24).Label("忽略空白").Disabled(tab.source == nil).Tooltip("忽略空白字符改动").Padding(0, 8).Radius(c.Theme().Radius).Cursor(ui.CursorPointer).Shrink(0).Center().Transition(colorMotion())
				if tab.ignoreWhitespace {
					tabIgnoreBtn.Background(c.Theme().Accent.Alpha(0.16)).TextColor(c.Theme().Accent).Border(1, c.Theme().Accent.Alpha(0.4))
				} else {
					tabIgnoreBtn.Background(c.Theme().Surface).TextColor(c.Theme().TextMuted).Border(1, c.Theme().Border)
				}
				tabIgnoreBtn.Children(func() {
					ui.Text(c, "忽略空白").FontSize(11).SingleLine()
				})
				if tabIgnoreBtn.Clicked() {
					tab.ignoreWhitespace = !tab.ignoreWhitespace
					a.reloadFileTab(t, tab)
				}
				if tab.diff != nil || tab.simple != nil {
					findTip := "在 Diff 中查找 (⌘F)"
					if runtime.GOOS != "darwin" {
						findTip = "在 Diff 中查找 (Ctrl+F)"
					}
					if paneIconButton(c, fmt.Sprintf("tab-find-in-diff-%d", tab.id), "在 Diff 中查找", iconSearch).Tooltip(findTip).Clicked() {
						a.openFileTabFind(tab)
					}
				}
			}
		})
		from, to := tab.labels()
		if tab.imageDiff == nil {
			ui.Row(c).Padding(0, 12, 8, 12).Gap(12).Shrink(0).Children(func() {
				ui.Text(c, "原版本 · "+from).FontSize(12).TextColor(c.Theme().TextMuted).Grow(1).Basis(0).MinWidth(0).SingleLine().Tooltip(hashHex(tab.from))
				ui.Text(c, "新版本 · "+to).FontSize(12).TextColor(c.Theme().TextMuted).Grow(1).Basis(0).MinWidth(0).SingleLine().Tooltip(hashHex(tab.to))
			})
		}
		a.fileDiffContent(c, tab.diff, tab.simple, tab.imageDiff, tab.note, false, tab.imageViewMode, tab.imageFitActual)
	})
}

func (a *app) reloadFileTab(t *repoTab, tab *fileDiffTab) {
	if tab == nil || tab.source == nil {
		return
	}
	tab.loadGen++
	gen, source, f, ignoreWhitespace := tab.loadGen, tab.source, tab.file, tab.ignoreWhitespace
	tab.note = "正在读取改动…"
	tab.diff, tab.simple, tab.imageDiff = nil, nil, nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		diff, simple, imgDiff, note, err := source.prepare(ctx, f.path, ignoreWhitespace && f.status != "A" && f.status != "D")
		if err != nil {
			note = err.Error()
		}
		a.post(func() {
			if t.closed || tab.closed || tab.loadGen != gen {
				return
			}
			tab.note = note
			tab.diff, tab.simple, tab.imageDiff = diff, simple, imgDiff
		})
	}()
}

func (a *app) openFullFileTab(t *repoTab, filePath, rev, revLabel string) {
	if t == nil || filePath == "" {
		return
	}
	for i, tab := range t.fileTabs {
		if tab.fullFile && tab.file.path == filePath && tab.fullRev == revLabel {
			t.contentTab = i + 1
			t.revealFileTab = true
			return
		}
	}
	t.nextFileTabID++
	f := fileChange{path: filePath}
	newTab := &fileDiffTab{
		id:       t.nextFileTabID,
		file:     f,
		fullFile: true,
		fullRev:  revLabel,
		openedAt: time.Now(),
		note:     "正在读取文件…",
	}
	t.fileTabs = append(t.fileTabs, newTab)
	t.contentTab = len(t.fileTabs)
	t.revealFileTab = true

	repoPath := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var content []byte
		var note string
		var err error
		if rev == "" || rev == "worktree" {
			content, note, err = readWorktreeFile(repoPath, filePath)
		} else {
			content, note, err = readBlob(ctx, repoPath, rev, filePath)
		}
		if err != nil {
			note = err.Error()
		}
		var imgInfo *imageInfo
		var state editor.State
		if isImageFile(filePath) && len(content) > 0 && err == nil {
			imgInfo, err = decodeImageInfo(content, filePath)
			if err != nil {
				note = fmt.Sprintf("图片解码失败：%v", err)
			}
		} else {
			buf := editor.NewBuffer(string(content))
			buf.SetLanguage(editor.LanguageFor(filePath))
			state = editor.State{
				Buffer:   buf,
				ReadOnly: true,
				Label:    filePath + " (" + revLabel + ")",
			}
		}
		a.post(func() {
			if t.closed || newTab.closed {
				return
			}
			newTab.fullContent = state
			newTab.fullImage = imgInfo
			newTab.note = note
			a.trimInactiveHistory()
		})
	}()
}

func (a *app) fullFileTabPane(c *ui.Context, t *repoTab, tab *fileDiffTab) {
	theme := c.Theme()
	ui.Column(c.Key(fmt.Sprintf("full-file-tab-%d-%d", t.id, tab.id))).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			if iconButton(c, "back-to-history-full", "返回提交历史", iconBack).Shrink(0).Clicked() {
				t.contentTab = 0
				t.revealFileTab = true
			}
			ui.Icon(c, iconFileText).Size(16, 16).TextColor(theme.TextMuted)
			pathBreadcrumb(c, "file-tab-path", tab.file.fullPath())
			ui.Text(c, "· "+tab.fullRev).FontSize(12).TextColor(theme.TextMuted).Shrink(0)
			copy := iconButton(c, "copy-tab-path-full", "复制路径", iconCopy).Tooltip(copyPathTip(c))
			if copy.Clicked() {
				copyFilePath(c, t.path, tab.file.path, copy.ClickModifiers())
			}
			if tab.fullImage != nil {
				ui.Box(c).Grow(1)
				fitLabel := "自适应"
				fitTip := "当前自适应窗口，点击切换至 1:1 实际尺寸"
				if tab.imageFitActual {
					fitLabel = "1:1 尺寸"
					fitTip = "当前 1:1 实际尺寸，点击切换至自适应缩放"
				}
				fitBtn := ui.ButtonBase(c.Key(fmt.Sprintf("full-img-fit-%d", tab.id))).Height(24).Label(fitLabel).Tooltip(fitTip).Padding(0, 8).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Center().Transition(colorMotion())
				if tab.imageFitActual {
					fitBtn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
				} else {
					fitBtn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border)
				}
				fitBtn.Children(func() {
					ui.Text(c, fitLabel).FontSize(11).SingleLine()
				})
				if fitBtn.Clicked() {
					tab.imageFitActual = !tab.imageFitActual
				}
			} else if tab.fullContent.Buffer != nil {
				findTip := "在文件中查找 (⌘F)"
				if runtime.GOOS != "darwin" {
					findTip = "在文件中查找 (Ctrl+F)"
				}
				if paneIconButton(c, fmt.Sprintf("tab-find-in-file-%d", tab.id), "在文件中查找", iconSearch).Tooltip(findTip).Clicked() {
					a.openFileTabFind(tab)
				}
			}
		})
		if tab.fullImage != nil {
			a.imageSingleView(c, tab.fullImage, tab.file.path, tab.imageFitActual)
			return
		}
		if tab.note != "" && tab.fullContent.Buffer == nil {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, tab.note).TextColor(theme.TextMuted)
			})
			return
		}
		tab.fullContent.RenderFindBar = renderFindBar
		editor.View(c.Key(fmt.Sprintf("full-editor-%d", tab.id)), &tab.fullContent).Grow(1).MinHeight(0)
	})
}

func (a *app) openFileTabFind(ft *fileDiffTab) {
	if ft == nil {
		return
	}
	if ft.fullFile {
		if ft.fullContent.Buffer != nil {
			ft.fullContent.Run(editor.Find)
		}
	} else {
		if ft.diff != nil {
			ft.diff.Run(editor.Find)
		} else if ft.simple != nil {
			ft.simple.new.Run(editor.Find)
		}
	}
}
