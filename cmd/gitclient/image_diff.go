package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"

	"github.com/egoist/mygo/ui"
)

// isImageFile 判断给定路径是否为支持预览的图片文件。
func isImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".jpe", ".gif", ".webp", ".bmp", ".svg":
		return true
	}
	return false
}

// imageInfo 保存解析后的图片元数据及用于 UI 渲染的 ImageSource。
type imageInfo struct {
	img    ui.ImageSource
	width  int
	height int
	size   int64
	format string
	data   []byte
}

// imageDiff 表示单张图片或新旧两张图片的对比数据。
type imageDiff struct {
	old *imageInfo
	new *imageInfo
}

// decodeImageInfo 解码图片数据并提取尺寸、格式等元数据。
func decodeImageInfo(data []byte, path string) (*imageInfo, error) {
	if len(data) == 0 {
		return nil, errors.New("图片数据为空")
	}
	src, err := ui.DecodeImage(data)
	if err != nil {
		return nil, err
	}
	info := &imageInfo{
		img:  src,
		size: int64(len(data)),
		data: data,
	}

	cfg, format, cfgErr := image.DecodeConfig(bytes.NewReader(data))
	if cfgErr == nil {
		info.width = cfg.Width
		info.height = cfg.Height
		info.format = strings.ToUpper(format)
	} else {
		w, h := ui.ImageSize(src)
		info.width = int(w)
		info.height = int(h)
		header := data
		if len(header) > 512 {
			header = header[:512]
		}
		if bytes.Contains(header, []byte("<svg")) {
			info.format = "SVG"
		}
	}
	if info.format == "" {
		ext := strings.TrimPrefix(filepath.Ext(path), ".")
		info.format = strings.ToUpper(ext)
	}
	return info, nil
}

// formatFileSize 将字节数格式化为易读的字符串。
func formatFileSize(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	}
	return fmt.Sprintf("%.2f MB", float64(b)/(1024*1024))
}

// formatResolution 返回格式化的分辨率字符串。
func formatResolution(info *imageInfo) string {
	if info == nil || info.width <= 0 || info.height <= 0 {
		return ""
	}
	return fmt.Sprintf("%d × %d", info.width, info.height)
}

// formatSizeDiff 比较新旧大小差异。
func formatSizeDiff(oldSize, newSize int64) (string, bool) {
	diff := newSize - oldSize
	if diff == 0 {
		return tr("大小相同"), false
	}
	if diff > 0 {
		return "+" + formatFileSize(diff), false
	}
	return "-" + formatFileSize(-diff), true
}

// paintCheckerboard 绘制透明棋盘格背景。
func paintCheckerboard(p *ui.Painter, r ui.Rect, tileSize float32, c1, c2 ui.Color) {
	if tileSize <= 0 {
		tileSize = 10
	}
	p.Fill(r, c1, 0)
	for y := r.Y; y < r.Y+r.H; y += tileSize {
		row := int((y - r.Y) / tileSize)
		h := min(tileSize, r.Y+r.H-y)
		for x := r.X; x < r.X+r.W; x += tileSize {
			col := int((x - r.X) / tileSize)
			if (row+col)%2 == 1 {
				w := min(tileSize, r.X+r.W-x)
				p.Fill(ui.Rect{X: x, Y: y, W: w, H: h}, c2, 0)
			}
		}
	}
}

// imageDiffControls 在顶栏渲染图片对比专用的视图控制与缩放切换按钮。
func (a *app) imageDiffControls(c *ui.Context, keyID int, imgDiff *imageDiff, viewMode *int, fitActual *bool) {
	theme := c.Theme()
	if imgDiff != nil && imgDiff.old != nil && imgDiff.new != nil {
		seg := ui.SegmentedBase(c.Key(fmt.Sprintf("img-view-mode-%d", keyID)), viewMode, 3)
		seg.Track.Height(24).Shrink(0).AlignItems(ui.Stretch).Padding(theme.Space(0.5)).Gap(theme.Space(0.5)).Radius(theme.Radius).Background(theme.Surface).Border(1, theme.Border).Label(tr("显示方式"))
		seg.Track.Children(func() {
			names := [3]string{"并排", "新版", "旧版"}
			tips := [3]string{"并排对比新旧版本", "仅显示新版本图片", "仅显示原版本图片"}
			for i := 0; i < 3; i++ {
				s := seg.Segment(i).Label(tr(names[i])).Tooltip(tr(tips[i])).Padding(0, 8).Radius(max(theme.Radius-theme.Space(0.5), 0)).Center().Transition(colorMotion())
				if i == *viewMode {
					bg := theme.Background
					if theme.Dark {
						bg = theme.SurfacePressed
					}
					s.Background(bg)
				}
				s.Children(func() {
					ui.Text(c, tr(names[i])).FontSize(11).SingleLine()
				})
			}
		})
	}

	fitLabel := "自适应"
	fitTip := "当前自适应窗口，点击切换至 1:1 实际尺寸"
	if *fitActual {
		fitLabel = "1:1 尺寸"
		fitTip = "当前 1:1 实际尺寸，点击切换至自适应缩放"
	}
	fitBtn := ui.ButtonBase(c.Key(fmt.Sprintf("img-fit-%d", keyID))).Height(24).Label(tr(fitLabel)).Tooltip(tr(fitTip)).Padding(0, 8).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Center().Transition(colorMotion())
	if *fitActual {
		fitBtn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
	} else {
		fitBtn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border)
	}
	fitBtn.Children(func() {
		ui.Text(c, tr(fitLabel)).FontSize(11).SingleLine()
	})
	if fitBtn.Clicked() {
		*fitActual = !*fitActual
	}
}

// renderImageCard 渲染单个图片预览卡片，包含头部信息栏和带棋盘格背景的图像区域。
func (a *app) renderImageCard(c *ui.Context, key string, title string, badge string, badgeColor ui.Color, info *imageInfo, fitActual bool, compareInfo *imageInfo) {
	theme := c.Theme()
	checkerC1 := ui.RGBA(255, 255, 255, 255)
	checkerC2 := ui.RGBA(238, 240, 243, 255)
	if theme.Dark {
		checkerC1 = ui.RGBA(28, 29, 31, 255)
		checkerC2 = ui.RGBA(38, 40, 44, 255)
	}

	ui.Column(c.Key(key)).Grow(1).Basis(0).MinWidth(0).MinHeight(0).Radius(theme.Radius).Border(1, theme.Border).Background(theme.Surface).Children(func() {
		// 顶部信息栏
		ui.Row(c).FillWidth().Padding(6, 10).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Shrink(0).Children(func() {
			ui.Text(c, title).Bold().FontSize(12).SingleLine()
			if badge != "" {
				chipSoft(c, key+"-badge", tr(badge), badgeColor, false)
			}
			ui.Box(c).Grow(1)
			if info != nil {
				if res := formatResolution(info); res != "" {
					ui.Text(c, res).FontSize(11).TextColor(theme.TextMuted).SingleLine()
					ui.Text(c, "·").FontSize(11).TextColor(theme.TextMuted)
				}
				sizeStr := formatFileSize(info.size)
				ui.Text(c, sizeStr).FontSize(11).TextColor(theme.TextMuted).SingleLine()
				if compareInfo != nil && compareInfo.size != info.size {
					diffStr, decreased := formatSizeDiff(compareInfo.size, info.size)
					col := theme.TextMuted
					if decreased {
						col = theme.Success
					} else {
						col = theme.Accent
					}
					ui.Text(c, fmt.Sprintf("(%s)", diffStr)).FontSize(11).TextColor(col).SingleLine()
				}
				if info.format != "" {
					chip(c, key+"-format", info.format, chipStyle{Text: theme.TextMuted, Background: theme.Border.Alpha(0.5), FontSize: 10})
				}
			}
		})

		// 图像展示区
		if info == nil || info.img == nil {
			emptyHint(c, key+"-empty", emptyHintProps{Icon: iconImage, Title: tr("无图片内容")}).Grow(1)
			return
		}
		ui.Scroll(c.Key(key + "-scroll")).Grow(1).FillWidth().Children(func() {
			ui.Box(c.Key(key + "-canvas")).Grow(1).FillWidth().FillHeight().MinHeight(220).Center().Padding(16).Draw(func(p *ui.Painter, r ui.Rect) {
				paintCheckerboard(p, r, 12, checkerC1, checkerC2)
			}).Children(func() {
				w, h := ui.ImageSize(info.img)
				if fitActual {
					ui.Image(c, info.img).Size(w, h).Fit(ui.NaturalSize)
				} else {
					ui.Image(c, info.img).Fill().Fit(ui.ScaleDown)
				}
			})
		})
	})
}

// imageDiffView 渲染图片比对整体视图（支持并排、新增、删除及单侧查看）。
func (a *app) imageDiffView(c *ui.Context, imgDiff *imageDiff, viewMode int, fitActual bool) {
	theme := c.Theme()
	ui.Column(c.Key("image-diff-view")).Grow(1).Padding(8).Children(func() {
		switch {
		case imgDiff.old == nil && imgDiff.new != nil:
			a.renderImageCard(c, "img-added", tr("新增图片"), "新增", theme.Success, imgDiff.new, fitActual, nil)
		case imgDiff.old != nil && imgDiff.new == nil:
			a.renderImageCard(c, "img-deleted", tr("已删除图片"), "删除", theme.Danger, imgDiff.old, fitActual, nil)
		case imgDiff.old != nil && imgDiff.new != nil:
			switch viewMode {
			case 1:
				a.renderImageCard(c, "img-modified-new", tr("新版本"), "新版", theme.Accent, imgDiff.new, fitActual, imgDiff.old)
			case 2:
				a.renderImageCard(c, "img-modified-old", tr("原版本"), "原版", theme.TextMuted, imgDiff.old, fitActual, nil)
			default:
				ui.Row(c).Grow(1).Gap(8).Children(func() {
					a.renderImageCard(c, "img-split-old", tr("原版本"), "", theme.TextMuted, imgDiff.old, fitActual, nil)
					a.renderImageCard(c, "img-split-new", tr("新版本"), "", theme.Accent, imgDiff.new, fitActual, imgDiff.old)
				})
			}
		default:
			emptyHint(c, "image-diff-empty", emptyHintProps{Icon: iconImage, Title: tr("无图片内容")}).Grow(1)
		}
	})
}

// imageSingleView 渲染单张图片全貌（用于完整文件标签页）。
func (a *app) imageSingleView(c *ui.Context, info *imageInfo, path string, fitActual bool) {
	ui.Column(c.Key("image-single-view")).Grow(1).Padding(8).Children(func() {
		a.renderImageCard(c, "img-single", displayPath(path), "", c.Theme().TextMuted, info, fitActual, nil)
	})
}
