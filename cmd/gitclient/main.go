// Xylring：导入一个 Git 仓库，画出它的提交图谱。
//
//	go test
//	go tool mygo dev
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	laneGap     = 18
	lanePad     = 10
	graphStroke = 2.5
	// 表格单元格会 Clip，并把内容垫进 Space(1.5)/Space(2.5)。
	// 图谱要画进这块内边距，线才能接到下一行的格线上。
	graphCellPadY   = 1.5
	graphCellPadX   = 2.5
	chromeMinHeight = 40
	chromePad       = 12
)

// 默认泳道色。明度接近，避开纯红纯绿，浅色和深色背景都能分开。
const graphColorDefault = "#4C82B0,#3AA88E,#D08A58,#8B74AE,#D47888,#7EA35C,#5E97B8,#4AA3AE"

var laneColors []ui.Color

func init() {
	laneColors, _ = parsePalette(graphColorDefault)
}

type app struct {
	win      *mygo.Window
	menu     *mygo.Menu
	dispatch func(func())

	tabs          []*repoTab
	active        int
	nextID        int
	recent        []string
	persist       bool
	picking       bool
	banner        string
	found         []string
	foundFrom     string
	foundGen      int
	closed        bool
	watchCancel   context.CancelFunc
	scanCancel    context.CancelFunc
	scanPath      string
	scanCache     map[string]scannedDir
	recentWrites  chan []string
	repoListState ui.ListState
	repoSel       int

	showCompare  bool
	compareFrom  string
	compareTo    string
	compareErr   string
	compareThree bool
}

func newApp() *app {
	return &app{
		recent:  loadRecent(),
		persist: true,
	}
}

func (a *app) view(c *ui.Context) {
	a.handleShortcuts(c)
	a.syncMenu()
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.chrome(c)
		if a.activeTab() == nil && len(a.found) <= 1 {
			a.emptyState(c)
		} else {
			a.graphScreen(c)
		}
	})
	a.comparisonModal(c)
}

func (a *app) handleShortcuts(c *ui.Context) {
	if a.menu == nil {
		if c.Shortcut(commandModifier(), ui.KeyO) {
			a.pickRepo()
		}
		if tab := a.activeTab(); tab != nil {
			if c.Shortcut(commandModifier(), ui.KeyR) {
				a.refresh(tab)
			}
			if c.Shortcut(commandModifier(), ui.KeyW) {
				a.closeTab(a.active)
			}
			if !tab.loading && !a.showCompare && c.Shortcut(commandModifier(), ui.KeyD) {
				a.openCompareModal(tab)
			}
		}
	}
	tab := a.activeTab()
	if tab != nil && !a.showCompare && c.Shortcut(commandModifier(), ui.KeyF) {
		if tab.query.filePath != "" {
			a.leaveFileHistory(tab)
		}
		tab.searchInput.Focus(c)
	}
	if tab == nil || a.showCompare || !c.Shortcut(0, ui.KeyEscape) {
		return
	}
	if tab.detailsOpen {
		tab.detailsOpen = false
		return
	}
	if tab.comparison != nil {
		a.endComparison(tab)
	} else if tab.worktree {
		a.closeWorktree(tab)
	} else if tab.inspecting != zeroHash {
		tab.selected = -1
		tab.selection.Clear()
		a.closeInspection(tab)
	} else if tab.query.filePath != "" {
		a.leaveFileHistory(tab)
	} else if tab.query.term != "" {
		a.leaveSearch(tab)
	}
}

func (a *app) chrome(c *ui.Context) {
	bar := c.TitleBar()
	h := max(bar.Height, chromeMinHeight)
	padL, padR := bar.Left, bar.Right
	if padL < chromePad {
		padL = chromePad
	}
	if padR < chromePad {
		padR = chromePad
	}
	tab := a.activeTab()
	empty := tab == nil && len(a.found) <= 1
	ui.Row(c).Height(h).Padding(0, padR, 0, padL).Gap(8).AlignItems(ui.Center).Shrink(0).DragWindow().Children(func() {
		if empty {
			ui.Box(c).Grow(1)
			return
		}
		if tab != nil {
			label := tab.title
			if tab.branch != "" {
				label += "  ·  " + tab.branch
			}
			ui.Text(c, label).Bold().SingleLine().Grow(1).MinWidth(0).Tooltip(tab.path)
		} else {
			ui.Box(c).Grow(1)
		}
		if ui.Button(c.Key("import"), "导入仓库").Clicked() {
			a.pickRepo()
		}
		if tab == nil {
			return
		}
		if ui.Button(c.Key("close"), "关闭仓库").Clicked() {
			a.closeTab(a.active)
		}
	})
}

func (a *app) emptyState(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(12).Padding(24).Children(func() {
		ui.Text(c, "Xylring").FontSize(28).Bold()
		if ui.PrimaryButton(c.Key("import-empty"), "导入仓库").Clicked() {
			a.pickRepo()
		}
		if a.banner != "" {
			ui.Text(c, a.banner).TextColor(t.Danger).SingleLine()
		}
		if len(a.recent) > 0 {
			ui.Text(c, "最近的仓库").TextColor(t.TextMuted)
			for i, p := range a.recent {
				p := p
				if ui.Button(c.Key(fmt.Sprintf("recent-%d", i)), repoTitle(p)).Tooltip(p).Clicked() {
					a.openRepo(p)
				}
			}
		}
	})
}

func (a *app) showRepoSidebar() bool {
	return len(a.found) > 1 || len(a.tabs) > 1
}

func (a *app) repoItems() []string {
	if len(a.found) > 1 {
		items := append([]string{}, a.found...)
		seen := map[string]bool{}
		for _, p := range items {
			seen[p] = true
		}
		for _, tab := range a.tabs {
			if !seen[tab.path] {
				items = append(items, tab.path)
			}
		}
		return items
	}
	items := make([]string, 0, len(a.tabs))
	for _, tab := range a.tabs {
		items = append(items, tab.path)
	}
	return items
}

func (a *app) repoList(c *ui.Context) {
	active := ""
	if tab := a.activeTab(); tab != nil {
		active = tab.path
	}
	ui.Column(c).Width(200).Shrink(0).FillHeight().MinHeight(0).Padding(8).Gap(4).Children(func() {
		items := a.repoItems()
		a.repoSel = -1
		for i, p := range items {
			if p == active {
				a.repoSel = i
				break
			}
		}
		a.repoListState.Selected = &a.repoSel
		a.repoListState.Key = func(i int) any { return items[i] }
		list := ui.List(c.Key("repositories"), &a.repoListState, len(items), func(i int) {
			p := items[i]
			label := repoTitle(p)
			if len(a.found) > 1 {
				label = repoChoiceLabel(a.foundFrom, p)
			}
			ui.Text(c, label).SingleLine().Tooltip(p)
		}).Grow(1).MinHeight(0)
		if list.Changed() && a.repoSel >= 0 && a.repoSel < len(items) {
			a.openRoot(items[a.repoSel])
		}
	})
}

func (a *app) graphScreen(c *ui.Context) {
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		if a.showRepoSidebar() {
			a.repoList(c)
		}
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			a.graphMain(c)
		})
	})
}

func (a *app) graphMain(c *ui.Context) {
	t := c.Theme()
	tab := a.activeTab()
	if tab == nil {
		ui.Box(c).Grow(1)
		return
	}
	a.historyToolbar(c, tab)
	if tab.hasMore && !tab.loading && !tab.canLoadHistory() {
		ui.Row(c).Padding(0, 12, 6, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c.Key("history-continue"), "继续加载").Clicked() {
				tab.historyBudget += historyMemoryStep
				tab.notice = ""
				a.loadMore(tab)
			}
		})
	}
	a.worktreeBar(c, tab)
	a.commitPane(c, tab)
	ui.Row(c).Padding(4, 12).Gap(12).AlignItems(ui.Center).Shrink(0).Children(func() {
		if tab.err == "" && tab.locateErr == "" && a.banner == "" && tab.notice == "" && tab.inspecting == zeroHash && tab.comparison == nil && !tab.worktree && !tab.loading && tab.loaded {
			ui.Textf(c, "%d 条提交", len(tab.commits)).FontSize(12).TextColor(t.TextMuted)
		}
		if tab.err != "" {
			ui.Text(c, tab.err).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0).SingleLine()
		} else if tab.locateErr != "" {
			ui.Text(c, tab.locateErr).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0).SingleLine()
		} else if a.banner != "" {
			ui.Text(c, a.banner).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0).SingleLine()
		} else if tab.notice != "" {
			ui.Text(c, tab.notice).FontSize(12).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
		}
	})
}

func (a *app) commitPane(c *ui.Context, tab *repoTab) {
	t := c.Theme()
	if tab.loading && len(tab.commits) == 0 && tab.err == "" {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, "正在读取提交图谱…").TextColor(t.TextMuted)
		})
		return
	}
	if tab.err != "" && len(tab.commits) == 0 {
		ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
			ui.Text(c, tab.err).TextColor(t.Danger)
		})
		return
	}
	if tab.holdAuto {
		tab.holdAuto = false
	} else if tab.hasMore && !tab.loading && tab.err == "" && len(tab.commits) > 0 && tab.list.AtEnd() && tab.canLoadHistory() {
		a.loadMore(tab)
	}
	tab.list.Selected = &tab.selected
	tab.list.Selection = &tab.selection
	tab.list.Key = func(i int) any {
		if commit := tab.rowData(i); commit != nil {
			return commit.hash
		}
		return i
	}
	tab.list.Label = func(i int) string {
		if commit := tab.rowData(i); commit != nil {
			return commit.subject
		}
		return ""
	}
	columns := []ui.TableColumn{
		{ID: "graph", Title: "图谱", Width: graphWidth(tab.graph.lanes()), MinWidth: 40},
		{ID: "subject", Title: "描述 / 引用", MinWidth: 200},
		{ID: "date", Title: "日期", Width: 150, MinWidth: 100},
		{ID: "author", Title: "作者", Width: 120, MinWidth: 60},
		{ID: "hash", Title: "哈希", Width: 85, MinWidth: 70},
	}
	if tab.query.term != "" || tab.query.filePath != "" {
		columns = columns[1:]
	}
	var pointerY float32
	var pointerOver bool
	rowHovered := func(r ui.Rect) bool {
		return pointerOver && pointerY >= r.Y && pointerY < r.Y+r.H
	}
	graph := func() {
		if tab.rows() == 0 && !tab.loading {
			text := "这个仓库还没有提交"
			if tab.query.term != "" {
				text = "没有匹配的提交"
			} else if tab.query.filePath != "" {
				text = "这个文件在所选版本之前没有历史"
			} else if tab.query.ref != "" || tab.query.hideRemote {
				text = "当前范围没有提交"
			}
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, text).TextColor(t.TextMuted)
			})
			return
		}
		table := ui.Table(c.Key(fmt.Sprintf("commits-%d", tab.id)), &tab.list, columns, tab.rows(), func(row, col int) {
			a.commitCell(c, tab, row, columns[col].ID, rowHovered)
		}).Grow(1).MinHeight(0)
		_, y, over := table.PointerPosition()
		pointerY, pointerOver = table.Bounds().Y+y, over
	}
	if tab.inspecting != zeroHash || tab.comparison != nil || tab.worktree {
		if tab.detailSplit == 0 {
			tab.detailSplit = 52
		}
		ui.SplitVerticalPercent(c.Key(fmt.Sprintf("inspect-split-%d", tab.id)), &tab.detailSplit, graph, func() {
			switch {
			case tab.comparison != nil:
				a.comparisonHeader(c, tab)
				a.fileDiffPane(c, tab)
			case tab.worktree:
				a.fileDiffPane(c, tab)
			default:
				a.inspectPane(c, tab)
			}
		}).Grow(1).MinHeight(0)
	} else {
		graph()
	}
	a.maybeInspectOrCompare(tab)
}

func (a *app) commitCell(c *ui.Context, tab *repoTab, i int, column string, rowHovered func(ui.Rect) bool) {
	t := c.Theme()
	item := tab.rowData(i)
	if item == nil {
		return
	}
	selected := tab.selection.Has(item.hash)
	muted := t.TextMuted
	if selected {
		muted = t.AccentText
	}
	if column == "graph" {
		padX, padY := c.Theme().Space(graphCellPadX), c.Theme().Space(graphCellPadY)
		background := t.Background
		if selected {
			background = t.Accent
		}
		hasRefs := item.hash == tab.head
		for _, ref := range tab.refs[item.hash] {
			if !tab.query.hideRemote || ref.kind != refRemote {
				hasRefs = true
				break
			}
		}
		ui.Box(c).Grow(1).MinHeight(28).AlignSelf(ui.Stretch).Draw(func(p *ui.Painter, r ui.Rect) {
			full := ui.Rect{X: r.X - padX, Y: r.Y - padY, W: r.W + padX*2, H: r.H + padY*2}
			bg := background
			if !selected && rowHovered(full) {
				bg = t.SurfaceHover
			}
			paintCommit(p, full, item, false, nil, bg, hasRefs)
		})
		return
	}
	ui.Row(c).Fill().MinHeight(28).Padding(2, 8).Gap(6).AlignItems(ui.Center).Children(func() {
		color := graphColor(item.nodeColor, nil)
		if selected {
			color = t.AccentText
		}
		switch column {
		case "subject":
			if item.hash == tab.head && tab.head != zeroHash {
				ui.Text(c, "HEAD").TextColor(color).Background(color.Alpha(0.12)).Padding(2, 6).Radius(4).Shrink(0)
			}
			var refs []string
			for _, ref := range tab.refs[item.hash] {
				if tab.query.hideRemote && ref.kind == refRemote {
					continue
				}
				label := ref.name
				if ref.kind == refTag {
					label = "标签 " + label
				}
				refs = append(refs, label)
			}
			for i, label := range refs {
				if i == 4 {
					ui.Text(c, "…").TextColor(muted).Shrink(0).Tooltip(strings.Join(refs[4:], "\n"))
					break
				}
				ui.Text(c, label).SingleLine().Shrink(0).TextColor(color).Background(color.Alpha(0.12)).Padding(2, 6).Radius(4)
			}
			text := item.subject
			if text == "" {
				text = "（无说明）"
			}
			e := ui.Text(c, text).SingleLine().Grow(1).MinWidth(0)
			if len(item.parents) > 1 {
				e.TextColor(muted)
			}
		case "date":
			ui.Text(c, item.whenText()).SingleLine().TextColor(muted)
		case "author":
			ui.Text(c, item.author).SingleLine().TextColor(muted)
		case "hash":
			ui.Text(c, shortHash(item.hash)).Font("monospace").SingleLine().TextColor(muted)
		}
	})
}

func parsePalette(s string) ([]ui.Color, error) {
	var colors []ui.Color
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "#")
		b, err := hex.DecodeString(part)
		if err != nil || len(b) != 3 {
			return nil, fmt.Errorf("图谱颜色须为逗号分隔的 #RRGGBB")
		}
		colors = append(colors, ui.RGBA(b[0], b[1], b[2], 1))
	}
	if len(colors) == 0 {
		return nil, fmt.Errorf("没有可用的图谱颜色")
	}
	return colors, nil
}

func graphWidth(lanes int) float32 {
	if lanes < 1 {
		lanes = 1
	}
	return lanePad*2 + float32(lanes)*laneGap
}

func laneCenter(r ui.Rect, col int) float32 {
	return r.X + lanePad + float32(col)*laneGap + laneGap/2
}

func laneColor(col int) ui.Color {
	if col < 0 {
		col = 0
	}
	return laneColors[col%len(laneColors)]
}

func graphColor(chain int, palette []ui.Color) ui.Color {
	if len(palette) > 0 {
		return palette[max(0, chain)%len(palette)]
	}
	return laneColor(chain)
}

func paintCommit(p *ui.Painter, r ui.Rect, c *commit, contFromTop bool, palette []ui.Color, background ui.Color, hasRefs bool) {
	mid := r.Y + r.H/2
	// 长泳道在下，短曲线带背景色衬底跨过泳道，最后再画节点。
	for _, bends := range []bool{false, true} {
		for _, s := range c.segs {
			if (s.bend != 0) != bends {
				continue
			}
			path := lanePath(r, s)
			if bends {
				p.StrokePath(&path, graphStroke+3, background)
			}
			p.StrokePath(&path, graphStroke, graphColor(s.color, palette))
		}
	}
	if contFromTop {
		x := laneCenter(r, c.nodeCol)
		p.Line(x, r.Y-1, x, mid, graphStroke, graphColor(c.nodeColor, palette))
	}
	radius := float32(4.5)
	if hasRefs {
		radius = 5.5
	}
	paintGraphNode(p, laneCenter(r, c.nodeCol), mid, radius, graphColor(c.nodeColor, palette), background, len(c.parents) > 1)
}

// 每次换道用一个行高的三次贝塞尔。两行分别画下半段和上半段，
// 行边界处位置和切线相同，节点处则沿泳道竖直进出。
func lanePath(r ui.Rect, s seg) ui.Path {
	x0, x1 := laneCenter(r, s.a), laneCenter(r, s.b)
	mid, bottom := r.Y+r.H/2, r.Y+r.H
	var path ui.Path
	switch s.bend {
	case 1:
		path.MoveTo(x0, mid)
		path.CubeTo(x0, mid+r.H/4, x0+(x1-x0)/4, bottom-r.H/8, (x0+x1)/2, bottom)
	case 2:
		path.MoveTo((x0+x1)/2, r.Y)
		path.CubeTo(x1-(x1-x0)/4, r.Y+r.H/8, x1, r.Y+r.H/4, x1, mid)
	default:
		y0, y1 := mid, mid
		if s.top {
			y0 = r.Y - 1
		}
		if s.bot {
			y1 = bottom + 1
		}
		path.MoveTo(x0, y0).LineTo(x1, y1)
	}
	return path
}

func paintGraphNode(p *ui.Painter, x, y, radius float32, color, background ui.Color, hollow bool) {
	var halo ui.Path
	halo.Circle(x, y, radius+2)
	p.FillPath(&halo, background)
	var node ui.Path
	if hollow {
		node.Circle(x, y, radius-1)
		p.StrokePath(&node, 2, color)
	} else {
		node.Circle(x, y, radius)
		p.FillPath(&node, color)
	}
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		a.installMenu()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:          "Xylring",
			Width:          1200,
			Height:         800,
			MinWidth:       860,
			MinHeight:      560,
			StateKey:       "main",
			TitleBarStyle:  mygo.TitleBarHiddenInset,
			TitleBarHeight: chromeMinHeight,
			Content:        ui.View(a.view),
		})
		for _, path := range os.Args[1:] {
			a.openPath(path)
		}
		a.startAutoRefresh()
		a.win.OnFocus(a.focusRepo)
		a.win.OnClosed(func() {
			a.closed = true
			if a.scanCancel != nil {
				a.scanCancel()
			}
			if a.recentWrites != nil {
				close(a.recentWrites)
				a.recentWrites = nil
			}
			if a.watchCancel != nil {
				a.watchCancel()
			}
			for _, tab := range a.tabs {
				tab.closed = true
				tab.cancel()
			}
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func commandModifier() ui.Modifiers {
	if runtime.GOOS == "darwin" {
		return ui.Cmd
	}
	return ui.Ctrl
}
