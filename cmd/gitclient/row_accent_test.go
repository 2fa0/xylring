package main

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// relativeLuminance 是 WCAG 的相对亮度，用来算两色的对比度。
func relativeLuminance(c ui.Color) float64 {
	channel := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

func contrastRatio(a, b ui.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestRowAccentBeatsListTheme 钉住一件事：行里的徽章不能用 listTheme 的 Accent。
// 那个位置被换成了 SurfacePressed，亮色模式下和行底色只差一点点，图标等于没画
// （用户就是这么反馈的：待推送的箭头看不清）。
func TestRowAccentBeatsListTheme(t *testing.T) {
	for _, tc := range []struct {
		name  string
		theme *ui.Theme
	}{
		{name: "light", theme: ui.LightTheme()},
		{name: "dark", theme: ui.DarkTheme()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 列表里用的是覆盖过的副本，徽章必须绕过它。
			theme := *tc.theme
			theme.Accent = theme.SurfacePressed
			accent := rowAccent(&theme)
			if accent == theme.Accent {
				t.Fatalf("rowAccent 拿到了列表主题的 Accent，就是那个看不见的 SurfacePressed")
			}
			if got := contrastRatio(accent, tc.theme.Background); got < 3 {
				t.Fatalf("徽章图标对底色的对比度 %.2f，低于 3（WCAG 图形元素下限）", got)
			}
			// 反例：拿列表覆盖后的 Accent 画，就是用户看到的样子。
			if got := contrastRatio(theme.Accent, tc.theme.Background); got >= 2.5 {
				t.Fatalf("反例已经不难看了（%.2f），这条断言该更新", got)
			}
		})
	}
}
