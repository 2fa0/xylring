package uikit

import (
	_ "embed"

	"github.com/2fa0/xylring/internal/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

// Lucide icons via Lobe MyGo (MIT) / Lucide (ISC), https://lucide.dev

//go:embed icons/tree-rings.svg
var iconTreeRingsSVG []byte

var (
	IconFolder       = lucide.Folder()
	IconFolderPlus   = lucide.FolderPlus()
	IconHome         = lucide.House()
	IconDownload     = lucide.Download()
	IconUpload       = lucide.Upload()
	IconArrowDown    = lucide.ArrowDown()
	IconScrollTop    = lucide.ArrowUpToLine()
	IconChevronDown  = lucide.ChevronDown()
	IconGitCompare   = lucide.GitCompare()
	IconGitBranch    = lucide.GitBranch()
	IconSearch       = lucide.Search()
	IconLocate       = lucide.LocateFixed()
	IconCopy         = lucide.Copy()
	IconClose        = lucide.X()
	IconBack         = lucide.ArrowLeft()
	IconList         = lucide.List()
	IconListTree     = lucide.ListTree()
	IconOpenTab      = lucide.SquareArrowOutUpRight()
	IconHistory      = lucide.History()
	IconPanelLeft    = lucide.PanelLeft()
	IconPanelBottom  = lucide.PanelBottom()
	IconMaximize     = lucide.Maximize2()
	IconMinimize     = lucide.Minimize2()
	IconAlertCircle  = lucide.AlertCircle()
	IconLock         = lucide.Lock()
	IconExpandAll    = lucide.ChevronsUpDown()
	IconCollapseAll  = lucide.ChevronsDownUp()
	IconPlus         = lucide.Plus()
	IconMinus        = lucide.Minus()
	IconFileText     = lucide.FileText()
	IconTrash2       = lucide.Trash2()
	IconCheck        = lucide.Check()
	IconColumns2     = lucide.Columns2()
	IconRows2        = lucide.Rows2()
	IconImage        = lucide.Image()
	IconGitCommit    = lucide.GitCommit()
	IconKanban       = lucide.Kanban()
	IconRadar        = lucide.Radar()
	IconTerminal     = lucide.Terminal()
	IconRefreshCw    = lucide.RefreshCw()
	IconMore         = lucide.Ellipsis()
	IconArchive      = lucide.Archive()
	IconLink         = lucide.Link2()
	IconMessage      = lucide.MessageSquareText()
	IconTag          = lucide.Tag()
	IconBot          = lucide.Bot()
	IconCircle       = lucide.Circle()
	IconCheckCircle2 = lucide.CheckCircle2()
	IconFolderGit    = lucide.FolderGit2()
	IconPencil       = lucide.Pencil()
	IconGripVertical = lucide.GripVertical()
	IconCalendar     = lucide.Calendar()
	IconChevronLeft  = lucide.ChevronLeft()
	IconChevronRight = lucide.ChevronRight()
	IconFlag         = lucide.Flag()
	IconContainer    = lucide.Box()
	IconStar         = lucide.Star()
	IconRotateCcw    = lucide.RotateCcw()
	IconArrowUpDown  = lucide.ArrowUpDown()
)

var (
	IconTreeRings    = ui.MustParseSVG(iconTreeRingsSVG)
	IconRingSettings = lucide.Settings()
	IconStarFilled   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>`))
)

func IconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	return ui.Button(c.Key(key), "").Label(i18n.Tr(name)).Tooltip(i18n.Tr(name)).Children(func() {
		ui.Icon(c, icon)
	})
}

func ChromeIconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(i18n.Tr(name)).Tooltip(i18n.Tr(name))
	return StyleActionIcon(c, b, icon, 26, 16, c.Theme().Text, false)
}

func PaneIconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(i18n.Tr(name)).Tooltip(i18n.Tr(name))
	return StyleActionIcon(c, b, icon, 24, 14, c.Theme().TextMuted, false)
}

func PaneToggleIconButton(c *ui.Context, key, name, tip string, icon *ui.SVG, active bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(i18n.Tr(name)).Tooltip(i18n.Tr(tip))
	return StyleActionIcon(c, b, icon, 24, 14, c.Theme().TextMuted, active)
}

func CloseIconButton(c *ui.Context, key, name string) ui.Element {
	return PaneIconButton(c, key, name, IconClose)
}
