// Git 客户端原型：验证 MyGo 原生界面能否画出提交图谱、打开 diff，
// 并在多仓库标签之间释放大块数据。这不是完整客户端。
//
//	go test
//	go tool mygo dev
package main

import (
	"fmt"
	"log"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const (
	laneGap = 14
	lanePad = 10
)

var laneColors = []ui.Color{
	ui.RGBA(31, 120, 180, 1),
	ui.RGBA(51, 160, 44, 1),
	ui.RGBA(227, 26, 28, 1),
	ui.RGBA(255, 127, 0, 1),
	ui.RGBA(106, 61, 154, 1),
	ui.RGBA(177, 89, 40, 1),
	ui.RGBA(23, 190, 207, 1),
	ui.RGBA(188, 128, 189, 1),
}

type app struct {
	win *mygo.Window

	tabs   []*repoTab
	active int
	nextID int
	recent []string

	split    float32
	filePane float32

	picking    bool
	showRecent bool
	banner     string

	branching  bool
	branchBusy bool
	branchName string
	branchErr  string
	branchRepo string
	branchHash string
	flash      string
	flashUntil time.Time
	heap       uint64
	heapAt     time.Time
}

func newApp() *app {
	return &app{
		split:    64,
		filePane: 300,
		recent:   loadRecent(),
	}
}

func (a *app) view(c *ui.Context) {
	a.sampleMem(c)
	a.expireFlash(c)
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.tabBar(c)
		if a.activeTab() == nil {
			a.emptyState(c)
		} else {
			a.toolbar(c)
			ui.SplitVerticalPercent(c, &a.split, func() {
				a.commitPane(c)
			}, func() {
				a.detail(c)
			}).Grow(1).MinHeight(0)
		}
		a.statusBar(c)
	})
	a.recentModal(c)
	a.branchModal(c)
}

func (a *app) tabBar(c *ui.Context) {
	t := c.Theme()
	activateAt, closeAt := -1, -1
	ui.Row(c).Padding(6, 8).Gap(6).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		for i, tab := range a.tabs {
			label := tab.title
			if i == a.active && !tab.released {
				label = "● " + label
			}
			if tab.released {
				label = "○ " + label
			}
			if ui.Button(c.Key(fmt.Sprintf("tab-%d", tab.id)), label).Clicked() {
				activateAt = i
			}
			if ui.Button(c.Key(fmt.Sprintf("tabx-%d", tab.id)), "×").Clicked() {
				closeAt = i
			}
		}
		if ui.Button(c.Key("add-tab"), "+").Tooltip("打开仓库").Clicked() {
			a.pickRepo()
		}
	})
	if closeAt >= 0 {
		a.closeTab(closeAt)
	} else if activateAt >= 0 {
		a.activate(activateAt)
	}
}

func (a *app) emptyState(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(12).Padding(24).Children(func() {
		ui.Text(c, "Git 客户端原型").FontSize(28).Bold()
		ui.Text(c, "打开一个仓库，查看提交图谱和 diff。后台标签会释放已加载的历史。").
			TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c.Key("open-empty"), "打开仓库").Clicked() {
				a.pickRepo()
			}
			if ui.Button(c.Key("recent-empty"), "最近的仓库").Clicked() {
				a.showRecent = true
			}
		})
	})
}

func (a *app) toolbar(c *ui.Context) {
	tab := a.activeTab()
	if tab == nil {
		return
	}
	t := c.Theme()
	if c.Shortcut(ui.Cmd, ui.KeyR) {
		a.reload(tab)
	}
	ui.Row(c).Padding(6, 8).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, tab.branch).Bold().Shrink(0)
		if ui.Button(c.Key("refresh"), "刷新").Clicked() {
			a.reload(tab)
		}
		if ui.Button(c.Key("open-more"), "打开").Clicked() {
			a.pickRepo()
		}
		if ui.Button(c.Key("recent-open"), "最近").Clicked() {
			a.showRecent = true
		}
		ui.TextInput(c.Key(fmt.Sprintf("find-%d", tab.id)), &tab.query).Placeholder("消息、作者或哈希").Width(220)
		if ui.Button(c.Key("find-next"), "下一个").Clicked() || c.Shortcut(ui.Cmd, ui.KeyG) {
			a.findNext(tab)
		}
		ui.Textf(c, "已加载 %d", len(tab.commits)).TextColor(t.TextMuted).Shrink(0)
		if tab.hasMore {
			ui.Text(c, "可继续").TextColor(t.TextMuted).Shrink(0)
		}
	})
}

func (a *app) commitPane(c *ui.Context) {
	tab := a.activeTab()
	t := c.Theme()
	if tab == nil {
		return
	}
	if tab.loading && len(tab.commits) == 0 && tab.err == "" {
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, "正在读取提交…").TextColor(t.TextMuted)
		})
		return
	}
	if tab.holdAuto {
		tab.holdAuto = false
	} else if tab.hasMore && !tab.loading && tab.err == "" && len(tab.commits) > 0 && tab.list.AtEnd() {
		a.loadMore(tab)
	}
	tab.list.Selected = &tab.selected
	tab.list.Key = func(i int) any {
		ci := tab.rowCommit(i)
		if ci < 0 || ci >= len(tab.commits) {
			return "dirty"
		}
		return tab.commits[ci].hash
	}
	tab.list.Label = func(i int) string {
		if tab.dirty > 0 && i == 0 {
			return "未提交更改"
		}
		ci := tab.rowCommit(i)
		if ci < 0 || ci >= len(tab.commits) {
			return ""
		}
		return tab.commits[ci].subject
	}
	width := graphWidth(tab.graph.lanes()) + 560
	ui.List(c.Key(fmt.Sprintf("commits-%d", tab.id)), &tab.list, tab.rows(), func(i int) {
		a.commitRow(c, tab, i)
	}).Grow(1).MinHeight(0).RowWidth(width).Children(func() {
		if tab.rows() == 0 && !tab.loading {
			msg := "这个仓库还没有提交"
			if tab.err != "" {
				msg = tab.err
			}
			ui.Text(c, msg).Padding(12).TextColor(t.TextMuted)
		}
	})
	if tab.selected != tab.commitApplied {
		tab.commitApplied = tab.selected
		a.onCommitSelected(tab)
	}
}

func (a *app) commitRow(c *ui.Context, tab *repoTab, i int) {
	t := c.Theme()
	row := ui.Row(c).MinHeight(28).Padding(2, 8).Gap(8).AlignItems(ui.Center)
	ci := tab.rowCommit(i)
	if ci < 0 {
		row.ContextMenu(func(m *ui.Menu) {
			m.Item("未提交更改").Disabled(true)
		})
	} else {
		commit := tab.commits[ci]
		row.ContextMenu(func(m *ui.Menu) {
			if m.Item("复制哈希").Chosen() {
				c.WriteClipboard(hashHex(commit.hash))
				a.setFlash("已复制 " + shortHash(commit.hash))
			}
			if m.Item("在此提交新建分支…").Chosen() {
				a.branching = true
				a.branchErr = ""
				a.branchName = ""
				a.branchRepo = tab.path
				a.branchHash = hashHex(commit.hash)
			}
		})
	}
	row.Children(func() {
		gw := graphWidth(tab.graph.lanes())
		ui.Box(c).Width(gw).MinHeight(28).AlignSelf(ui.Stretch).Draw(func(p *ui.Painter, r ui.Rect) {
			if ci < 0 {
				drawDirty(p, r, tab)
				return
			}
			drawCommit(p, r, &tab.commits[ci], ci == 0 && tab.dirty > 0)
		})
		if ci < 0 {
			ui.Textf(c, "未提交更改（%d）", tab.dirty).Grow(1).SingleLine()
			return
		}
		commit := tab.commits[ci]
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			if pref := tab.refText(commit.hash); pref != "" {
				ui.Text(c, pref).SingleLine().Shrink(0).TextColor(t.Success)
			}
			subject := commit.subject
			if subject == "" {
				subject = "（无说明）"
			}
			ui.Text(c, subject).SingleLine().Grow(1).MinWidth(0)
		})
		ui.Text(c, commit.whenText()).Width(118).SingleLine().Shrink(0).TextColor(t.TextMuted)
		ui.Text(c, commit.author).Width(120).SingleLine().Shrink(0).TextColor(t.TextMuted)
		ui.Text(c, shortHash(commit.hash)).Width(64).SingleLine().Shrink(0).Font("monospace").TextColor(t.TextMuted)
	})
}

func (a *app) detail(c *ui.Context) {
	tab := a.activeTab()
	t := c.Theme()
	if tab == nil {
		return
	}
	ui.Column(c).Fill().MinHeight(0).Children(func() {
		if tab.selected < 0 || (tab.selectedCommit() == nil && !(tab.dirty > 0 && tab.selected == 0)) {
			ui.Text(c, "单击一条提交查看改动").Padding(12).TextColor(t.TextMuted)
			return
		}
		a.detailHeader(c, tab)
		if tab.detailErr != "" {
			ui.Text(c, tab.detailErr).Padding(8, 12).TextColor(t.Danger)
		}
		if tab.detailLoading {
			ui.Text(c, "正在读取文件列表…").Padding(8, 12).TextColor(t.TextMuted)
		}
		ui.Split(c.Key(fmt.Sprintf("file-split-%d", tab.id)), &a.filePane, func() {
			a.filePaneView(c, tab)
		}, func() {
			a.diffPane(c, tab)
		}).Grow(1).MinHeight(0)
	})
}

func (a *app) detailHeader(c *ui.Context, tab *repoTab) {
	t := c.Theme()
	if tab.dirty > 0 && tab.selected == 0 {
		ui.Column(c).Padding(8, 12).Gap(4).Shrink(0).Children(func() {
			ui.Textf(c, "未提交更改（%d 个文件，不含未跟踪）", tab.dirty).Bold()
			ui.Text(c, "对比的是工作区相对 HEAD 的内容").FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	commit := tab.selectedCommit()
	if commit == nil {
		return
	}
	ui.Column(c).Padding(8, 12).Gap(4).Shrink(0).Children(func() {
		subject := commit.subject
		if subject == "" {
			subject = "（无说明）"
		}
		ui.Text(c, subject).Bold()
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, hashHex(commit.hash)).Font("monospace").FontSize(12).TextColor(t.TextMuted)
			if ui.Button(c.Key("copy-hash"), "复制").Clicked() {
				c.WriteClipboard(hashHex(commit.hash))
				a.setFlash("已复制 " + shortHash(commit.hash))
			}
			ui.Text(c, commit.author).TextColor(t.TextMuted)
			ui.Text(c, commit.whenText()).TextColor(t.TextMuted)
		})
		if len(commit.parents) > 0 {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "父提交").FontSize(12).TextColor(t.TextMuted)
				for _, p := range commit.parents {
					p := p
					label := shortHash(p)
					if ui.Button(c.Key("parent-"+hashHex(p)), label).Clicked() {
						a.jumpHash(tab, p)
					}
				}
			})
		}
	})
}

func (a *app) filePaneView(c *ui.Context, tab *repoTab) {
	t := c.Theme()
	tab.fileList.Selected = &tab.fileSel
	tab.fileList.Key = func(i int) any {
		if i < 0 || i >= len(tab.files) {
			return i
		}
		return tab.files[i].path
	}
	tab.fileList.Label = func(i int) string {
		if i < 0 || i >= len(tab.files) {
			return ""
		}
		return tab.files[i].label()
	}
	ui.List(c.Key(fmt.Sprintf("files-%d", tab.id)), &tab.fileList, len(tab.files), func(i int) {
		ui.Text(c, tab.files[i].label()).Padding(4, 8).SingleLine()
	}).Grow(1).MinHeight(0).Children(func() {
		if len(tab.files) == 0 && !tab.detailLoading {
			ui.Text(c, "没有文件改动").Padding(12).TextColor(t.TextMuted)
		}
	})
	if tab.fileSel != tab.fileApplied {
		tab.fileApplied = tab.fileSel
		a.onFileSelected(tab)
	}
}

func (a *app) diffPane(c *ui.Context, tab *repoTab) {
	t := c.Theme()
	ui.Column(c).Fill().MinHeight(0).Children(func() {
		ui.Row(c).Padding(4, 8).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			label := "并排"
			if tab.layout == editor.DiffUnified {
				label = "统一"
			}
			if ui.Button(c.Key("layout"), label).Clicked() {
				if tab.layout == editor.DiffSplit {
					tab.layout = editor.DiffUnified
				} else {
					tab.layout = editor.DiffSplit
				}
				if tab.diff != nil {
					tab.diff.Layout = tab.layout
				}
			}
			if tab.fileSel >= 0 && tab.fileSel < len(tab.files) {
				ui.Text(c, tab.files[tab.fileSel].path).SingleLine().Grow(1).MinWidth(0)
			}
		})
		switch {
		case tab.diffLoading:
			ui.Text(c, "正在读取 diff…").Padding(12).TextColor(t.TextMuted)
		case tab.detailNote != "":
			ui.Text(c, tab.detailNote).Padding(12).TextColor(t.TextMuted)
		case tab.diff != nil:
			editor.DiffView(c.Key(fmt.Sprintf("diff-%d", tab.id)), tab.diff).Grow(1).MinHeight(0)
		default:
			ui.Text(c, "选择左侧的文件").Padding(12).TextColor(t.TextMuted)
		}
	})
}

func (a *app) statusBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Padding(4, 8).Gap(12).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		ui.Textf(c, "Go 堆 %.1f MB", float64(a.heap)/1024/1024).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, "不含窗口和 GPU").FontSize(12).TextColor(t.TextMuted)
		if n := a.releasedCount(); n > 0 {
			ui.Textf(c, "后台标签已释放 %d", n).FontSize(12).TextColor(t.TextMuted)
		}
		if tab := a.activeTab(); tab != nil && tab.loading {
			ui.Text(c, "读取中").FontSize(12).TextColor(t.TextMuted)
		}
		if tab := a.activeTab(); tab != nil && tab.err != "" {
			ui.Text(c, tab.err).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0).SingleLine()
		}
		if a.banner != "" {
			ui.Text(c, a.banner).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0).SingleLine()
		}
		if a.flash != "" {
			ui.Text(c, a.flash).FontSize(12).Grow(1).MinWidth(0).SingleLine()
		}
	})
}

func (a *app) releasedCount() int {
	n := 0
	for _, tab := range a.tabs {
		if tab.released {
			n++
		}
	}
	return n
}

func (a *app) recentModal(c *ui.Context) {
	ui.Modal(c.Key("recent-modal"), &a.showRecent, func() {
		ui.Column(c).Gap(8).Width(460).Children(func() {
			ui.Text(c, "最近的仓库").Bold()
			if len(a.recent) == 0 {
				ui.Text(c, "还没有。打开一个仓库后会出现在这里。").TextColor(c.Theme().TextMuted)
			}
			for i, p := range a.recent {
				p := p
				if ui.Button(c.Key(fmt.Sprintf("recent-%d", i)), p).Clicked() {
					a.showRecent = false
					a.openPath(p)
				}
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("recent-close"), "关闭").Clicked() {
					a.showRecent = false
				}
			})
		})
	})
}

func (a *app) branchModal(c *ui.Context) {
	ui.Modal(c.Key("branch-modal"), &a.branching, func() {
		ui.Column(c).Gap(8).Width(380).Children(func() {
			ui.Text(c, "新建分支").Bold()
			shown := a.branchHash
			if len(shown) > 7 {
				shown = shown[:7]
			}
			ui.Text(c, "从 "+shown+" 创建，不会检出。").FontSize(12).TextColor(c.Theme().TextMuted)
			input := ui.TextInput(c.Key("branch-name"), &a.branchName).Placeholder("分支名").Width(340).AutoFocus()
			submit := input.Submitted()
			if a.branchErr != "" {
				ui.Text(c, a.branchErr).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("branch-cancel"), "取消").Clicked() {
					a.branching = false
				}
				if ui.PrimaryButton(c.Key("branch-create"), "创建").Disabled(a.branchBusy).Clicked() || submit {
					a.createBranch()
				}
			})
		})
	})
}

func (a *app) sampleMem(c *ui.Context) {
	now := c.Now()
	if !a.heapAt.IsZero() && now.Sub(a.heapAt) < 2*time.Second {
		return
	}
	a.heap = readHeap()
	a.heapAt = now
	c.After(2 * time.Second)
}

func (a *app) expireFlash(c *ui.Context) {
	if a.flash == "" {
		return
	}
	if !c.Now().Before(a.flashUntil) {
		a.flash = ""
		return
	}
	c.After(time.Until(a.flashUntil))
}

func readHeap() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
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

func drawCommit(p *ui.Painter, r ui.Rect, c *commit, contFromTop bool) {
	var path ui.Path
	mid := r.Y + r.H/2
	for _, s := range c.segs {
		y0, y1 := r.Y, r.Y+r.H
		if !s.top {
			y0 = mid
		}
		if !s.bot {
			y1 = mid
		}
		color := laneColor(s.a)
		x0, x1 := laneCenter(r, s.a), laneCenter(r, s.b)
		if x0 == x1 {
			p.Line(x0, y0, x1, y1, 2, color)
			continue
		}
		path.Reset()
		path.MoveTo(x0, y0)
		path.CubeTo(x0, (y0+y1)/2, x1, (y0+y1)/2, x1, y1)
		p.StrokePath(&path, 2, color)
	}
	if contFromTop {
		x := laneCenter(r, c.nodeCol)
		p.Line(x, r.Y, x, mid, 2, laneColor(c.nodeCol))
	}
	var node ui.Path
	node.Circle(laneCenter(r, c.nodeCol), mid, 4.5)
	p.FillPath(&node, laneColor(c.nodeCol))
}

func drawDirty(p *ui.Painter, r ui.Rect, tab *repoTab) {
	col := 0
	if len(tab.commits) > 0 {
		col = tab.commits[0].nodeCol
	}
	color := ui.RGBA(230, 160, 0, 1)
	x := laneCenter(r, col)
	mid := r.Y + r.H/2
	p.Line(x, mid, x, r.Y+r.H, 2, color)
	var node ui.Path
	node.Circle(x, mid, 4.5)
	p.FillPath(&node, color)
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Git 客户端原型",
			Width:     1200,
			Height:    800,
			MinWidth:  860,
			MinHeight: 560,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
