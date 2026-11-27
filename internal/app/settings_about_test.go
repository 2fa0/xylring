package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 关于卡片上的两条路都通仓库：左边是可点的地址，右边是那颗星。
func TestAboutSettingsCard(t *testing.T) {
	newTester := func() *ui.Tester {
		a := &app{dispatch: func(func()) {}}
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).FillWidth().Padding(20).Children(func() { a.aboutSettings(c) })
		}, 640, 200)
		tt.Frame()
		t.Cleanup(func() { retireTester(tt) })
		return tt
	}
	tt := newTester()
	if !tt.HasText("关于") || !tt.HasText("项目主页") || !tt.HasText("github.com/2fa0/xylring") {
		t.Fatalf("missing about card: %v", tt.Texts())
	}
	if err := tt.Click("github.com/2fa0/xylring"); err != nil {
		t.Fatalf("click repo link: %v", err)
	}
	tt.Frame()
	if urls := tt.OpenedURLs(); len(urls) != 1 || urls[0] != projectURL {
		t.Fatalf("repo link opened %v", urls)
	}

	star := newTester()
	if err := star.Click("在 GitHub 上点 Star"); err != nil {
		t.Fatalf("click star: %v", err)
	}
	star.Frame()
	if urls := star.OpenedURLs(); len(urls) != 1 || urls[0] != projectURL {
		t.Fatalf("star opened %v", urls)
	}
}
