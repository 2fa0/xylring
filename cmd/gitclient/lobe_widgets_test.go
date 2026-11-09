package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestCopyIconConfirmationAndKeyboard(t *testing.T) {
	content := "first error"
	tt := ui.NewTester(func(c *ui.Context) {
		copyIconButton(c, "copy", "复制错误信息", content)
	}, 200, 80)
	defer retireTester(tt)
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tt.Clipboard() != content {
		t.Fatalf("keyboard copy: got %q", tt.Clipboard())
	}
	confirmed, ok := tt.AccessByRole(ui.RoleButton, "已复制")
	if !ok || confirmed.Bounds.W != 24 || confirmed.Bounds.H != 24 {
		t.Fatalf("missing fixed-size confirmation: %+v", confirmed)
	}
	tt.Advance(time.Second)
	content = "second error"
	if err := tt.Click("已复制"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != content {
		t.Fatalf("repeat copy used stale content: %q", tt.Clipboard())
	}
	tt.Advance(1500 * time.Millisecond)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "已复制"); !ok {
		t.Fatal("repeat copy did not restart confirmation")
	}
	tt.Advance(time.Second)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "复制错误信息"); !ok {
		t.Fatal("confirmation did not expire")
	}
}

func TestActionIconDisabledAndKeyboard(t *testing.T) {
	disabled, clicks := true, 0
	tt := ui.NewTester(func(c *ui.Context) {
		if chromeIconButton(c, "action", "测试操作", iconDownload).Disabled(disabled).Clicked() {
			clicks++
		}
	}, 200, 80)
	defer retireTester(tt)
	tt.Frame()
	info, ok := tt.AccessByRole(ui.RoleButton, "测试操作")
	if !ok || !info.Disabled || info.Focusable {
		t.Fatalf("disabled semantics lost: %+v", info)
	}
	tt.ClickAt(info.Bounds.X+13, info.Bounds.Y+13)
	tt.Frame()
	if clicks != 0 {
		t.Fatal("disabled button activated")
	}
	disabled = false
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if clicks != 1 {
		t.Fatalf("keyboard activation count = %d", clicks)
	}
}
