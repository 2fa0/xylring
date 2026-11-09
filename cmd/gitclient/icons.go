package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

// Lucide icons, ISC License, https://lucide.dev

//go:embed icons/folder.svg
var iconFolderSVG []byte

//go:embed icons/folder-plus.svg
var iconFolderPlusSVG []byte

//go:embed icons/house.svg
var iconHomeSVG []byte

//go:embed icons/download.svg
var iconDownloadSVG []byte

//go:embed icons/git-compare.svg
var iconGitCompareSVG []byte

//go:embed icons/git-branch.svg
var iconGitBranchSVG []byte

//go:embed icons/search.svg
var iconSearchSVG []byte

//go:embed icons/locate-fixed.svg
var iconLocateSVG []byte

//go:embed icons/copy.svg
var iconCopySVG []byte

//go:embed icons/x.svg
var iconCloseSVG []byte

//go:embed icons/arrow-left.svg
var iconBackSVG []byte

//go:embed icons/list.svg
var iconListSVG []byte

//go:embed icons/list-tree.svg
var iconListTreeSVG []byte

//go:embed icons/square-arrow-out-up-right.svg
var iconOpenTabSVG []byte

//go:embed icons/history.svg
var iconHistorySVG []byte

//go:embed icons/panel-left.svg
var iconPanelLeftSVG []byte

//go:embed icons/panel-bottom.svg
var iconPanelBottomSVG []byte

//go:embed icons/maximize-2.svg
var iconMaximizeSVG []byte

//go:embed icons/minimize-2.svg
var iconMinimizeSVG []byte

//go:embed icons/alert-circle.svg
var iconAlertCircleSVG []byte

//go:embed icons/chevrons-up-down.svg
var iconExpandAllSVG []byte

//go:embed icons/chevrons-down-up.svg
var iconCollapseAllSVG []byte

//go:embed icons/upload.svg
var iconUploadSVG []byte

//go:embed icons/arrow-down.svg
var iconArrowDownSVG []byte

//go:embed icons/plus.svg
var iconPlusSVG []byte

//go:embed icons/minus.svg
var iconMinusSVG []byte

//go:embed icons/file-text.svg
var iconFileTextSVG []byte

//go:embed icons/trash-2.svg
var iconTrash2SVG []byte

//go:embed icons/check.svg
var iconCheckSVG []byte

//go:embed icons/columns-2.svg
var iconColumns2SVG []byte

//go:embed icons/rows-2.svg
var iconRows2SVG []byte

//go:embed icons/tree-rings.svg
var iconTreeRingsSVG []byte

//go:embed icons/settings.svg
var iconRingSettingsSVG []byte

var (
	iconFolder      = ui.MustParseSVG(iconFolderSVG)
	iconFolderPlus  = ui.MustParseSVG(iconFolderPlusSVG)
	iconHome        = ui.MustParseSVG(iconHomeSVG)
	iconDownload    = ui.MustParseSVG(iconDownloadSVG)
	iconUpload      = ui.MustParseSVG(iconUploadSVG)
	iconArrowDown   = ui.MustParseSVG(iconArrowDownSVG)
	iconGitCompare  = ui.MustParseSVG(iconGitCompareSVG)
	iconGitBranch   = ui.MustParseSVG(iconGitBranchSVG)
	iconSearch      = ui.MustParseSVG(iconSearchSVG)
	iconLocate      = ui.MustParseSVG(iconLocateSVG)
	iconCopy        = ui.MustParseSVG(iconCopySVG)
	iconClose       = ui.MustParseSVG(iconCloseSVG)
	iconBack        = ui.MustParseSVG(iconBackSVG)
	iconList        = ui.MustParseSVG(iconListSVG)
	iconListTree    = ui.MustParseSVG(iconListTreeSVG)
	iconOpenTab     = ui.MustParseSVG(iconOpenTabSVG)
	iconHistory     = ui.MustParseSVG(iconHistorySVG)
	iconPanelLeft   = ui.MustParseSVG(iconPanelLeftSVG)
	iconPanelBottom = ui.MustParseSVG(iconPanelBottomSVG)
	iconMaximize    = ui.MustParseSVG(iconMaximizeSVG)
	iconMinimize    = ui.MustParseSVG(iconMinimizeSVG)
	iconAlertCircle = ui.MustParseSVG(iconAlertCircleSVG)
	iconExpandAll   = ui.MustParseSVG(iconExpandAllSVG)
	iconCollapseAll = ui.MustParseSVG(iconCollapseAllSVG)
	iconPlus        = ui.MustParseSVG(iconPlusSVG)
	iconMinus       = ui.MustParseSVG(iconMinusSVG)
	iconFileText    = ui.MustParseSVG(iconFileTextSVG)
	iconTrash2      = ui.MustParseSVG(iconTrash2SVG)
	iconCheck       = ui.MustParseSVG(iconCheckSVG)
	iconColumns2    = ui.MustParseSVG(iconColumns2SVG)
	iconRows2       = ui.MustParseSVG(iconRows2SVG)
)

var (
	iconTreeRings    = ui.MustParseSVG(iconTreeRingsSVG)
	iconRingSettings = ui.MustParseSVG(iconRingSettingsSVG)
)

func iconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	return ui.Button(c.Key(key), "").Label(name).Tooltip(name).Children(func() {
		ui.Icon(c, icon)
	})
}

func chromeIconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Label(name).Tooltip(name).Size(26, 26).Radius(6).Center().Shrink(0).FocusRing(false).Cursor(ui.CursorPointer).Transition(colorMotion())
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(16, 16).TextColor(t.Text)
	})
	return b
}

func paneIconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Label(name).Tooltip(name).Padding(4).Radius(t.Radius).Shrink(0).Cursor(ui.CursorPointer).Transition(colorMotion())
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(t.TextMuted)
	})
	return b
}

func paneToggleIconButton(c *ui.Context, key, name, tip string, icon *ui.SVG, active bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Label(name).Tooltip(tip).Padding(4).Radius(t.Radius).Shrink(0).Cursor(ui.CursorPointer).Transition(colorMotion())
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	case active:
		b.Background(t.Accent.Alpha(0.12))
	}
	textColor := t.TextMuted
	if active {
		textColor = t.Accent
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(textColor)
	})
	return b
}

func closeIconButton(c *ui.Context, key, name string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Label(name).Tooltip(name).Padding(4).Radius(t.Radius).Shrink(0).Cursor(ui.CursorPointer).Transition(colorMotion())
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Icon(c, iconClose).Size(14, 14).TextColor(t.TextMuted)
	})
	return b
}
