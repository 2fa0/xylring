package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

// Lucide icons via Lobe MyGo (MIT) / Lucide (ISC), https://lucide.dev

//go:embed icons/tree-rings.svg
var iconTreeRingsSVG []byte

var (
	iconFolder       = lucide.Folder()
	iconFolderPlus   = lucide.FolderPlus()
	iconHome         = lucide.House()
	iconDownload     = lucide.Download()
	iconUpload       = lucide.Upload()
	iconArrowDown    = lucide.ArrowDown()
	iconScrollTop    = lucide.ArrowUpToLine()
	iconChevronDown  = lucide.ChevronDown()
	iconGitCompare   = lucide.GitCompare()
	iconGitBranch    = lucide.GitBranch()
	iconSearch       = lucide.Search()
	iconLocate       = lucide.LocateFixed()
	iconCopy         = lucide.Copy()
	iconClose        = lucide.X()
	iconBack         = lucide.ArrowLeft()
	iconList         = lucide.List()
	iconListTree     = lucide.ListTree()
	iconOpenTab      = lucide.SquareArrowOutUpRight()
	iconHistory      = lucide.History()
	iconPanelLeft    = lucide.PanelLeft()
	iconPanelBottom  = lucide.PanelBottom()
	iconMaximize     = lucide.Maximize2()
	iconMinimize     = lucide.Minimize2()
	iconAlertCircle  = lucide.AlertCircle()
	iconLock         = lucide.Lock()
	iconExpandAll    = lucide.ChevronsUpDown()
	iconCollapseAll  = lucide.ChevronsDownUp()
	iconPlus         = lucide.Plus()
	iconMinus        = lucide.Minus()
	iconFileText     = lucide.FileText()
	iconTrash2       = lucide.Trash2()
	iconCheck        = lucide.Check()
	iconColumns2     = lucide.Columns2()
	iconRows2        = lucide.Rows2()
	iconImage        = lucide.Image()
	iconGitCommit    = lucide.GitCommit()
	iconKanban       = lucide.Kanban()
	iconMore         = lucide.Ellipsis()
	iconArchive      = lucide.Archive()
	iconLink         = lucide.Link2()
	iconMessage      = lucide.MessageSquareText()
	iconTag          = lucide.Tag()
	iconBot          = lucide.Bot()
	iconCircle       = lucide.Circle()
	iconCheckCircle2 = lucide.CheckCircle2()
	iconFolderGit    = lucide.FolderGit2()
	iconPencil       = lucide.Pencil()
	iconGripVertical = lucide.GripVertical()
	iconCalendar     = lucide.Calendar()
	iconChevronLeft  = lucide.ChevronLeft()
	iconChevronRight = lucide.ChevronRight()
	iconFlag         = lucide.Flag()
	iconContainer    = lucide.Box()
	iconStar         = lucide.Star()
	iconRotateCcw    = lucide.RotateCcw()
	iconArrowUpDown  = lucide.ArrowUpDown()
)

var (
	iconTreeRings    = ui.MustParseSVG(iconTreeRingsSVG)
	iconRingSettings = lucide.Settings()
	iconStarFilled   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>`))
)

func iconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	return ui.Button(c.Key(key), "").Label(tr(name)).Tooltip(tr(name)).Children(func() {
		ui.Icon(c, icon)
	})
}

func chromeIconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(tr(name)).Tooltip(tr(name))
	return styleActionIcon(c, b, icon, 26, 16, c.Theme().Text, false)
}

func paneIconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(tr(name)).Tooltip(tr(name))
	return styleActionIcon(c, b, icon, 24, 14, c.Theme().TextMuted, false)
}

func paneToggleIconButton(c *ui.Context, key, name, tip string, icon *ui.SVG, active bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(tr(name)).Tooltip(tr(tip))
	return styleActionIcon(c, b, icon, 24, 14, c.Theme().TextMuted, active)
}

func closeIconButton(c *ui.Context, key, name string) ui.Element {
	return paneIconButton(c, key, name, iconClose)
}
