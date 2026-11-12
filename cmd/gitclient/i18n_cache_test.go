package main

import (
	"context"
	"strings"
	"testing"
	"unicode"

	"github.com/egoist/mygo/ui"
)

func TestCommitDetailsLanguageSwitchPreservesUserText(t *testing.T) {
	old := englishUI.Load()
	t.Cleanup(func() { englishUI.Store(old) })
	englishUI.Store(false)
	d := &commitDetails{hash: "abc123", author: "中文作者", authorEmail: "author@example.com", committer: "Test", commitEmail: "test@example.com", message: "用户提交说明\n中文正文", prov: provenance{agent: "human"}}
	d.updateViewLanguage()
	chineseView := d.view.Buffer
	if !strings.HasPrefix(d.text(), "年轮印记：") || !strings.Contains(chineseView.String(), "父提交：无（初始提交）") {
		t.Fatal("missing Chinese commit metadata")
	}
	englishUI.Store(true)
	d.updateViewLanguage()
	if d.view.Buffer == chineseView {
		t.Fatal("cached editor did not update after language switch")
	}
	if d.view.Label != "Full commit information" {
		t.Fatalf("stale editor label %q", d.view.Label)
	}
	englishText := d.view.Buffer.String()
	if englishText != d.text() {
		t.Fatal("editor and copied commit information use different languages")
	}
	if !strings.Contains(englishText, "Parents: None (initial commit)") || !strings.Contains(englishText, "Human (Explicit)") {
		t.Fatalf("missing English commit metadata: %s", englishText)
	}
	for _, original := range []string{d.author, d.message} {
		if !strings.Contains(englishText, original) {
			t.Fatalf("user text changed: %q", original)
		}
		englishText = strings.ReplaceAll(englishText, original, "")
	}
	for _, r := range englishText {
		if unicode.Is(unicode.Han, r) {
			t.Fatalf("Chinese app text remains: %s", englishText)
		}
	}
	englishUI.Store(false)
	d.updateViewLanguage()
	if d.view.Buffer.String() != d.text() || !strings.Contains(d.text(), "父提交：") {
		t.Fatal("cannot switch cached details back to Chinese")
	}
}

func TestCachedProvenanceBadgesFollowLanguage(t *testing.T) {
	old := englishUI.Load()
	t.Cleanup(func() { englishUI.Store(old) })
	englishUI.Store(false)
	pool := &provenanceTable{}
	human := pool.intern(provenance{agent: "human"})
	custom := pool.intern(provenance{agent: "人工", model: "中文模型"})
	if pool.get(human).badge != "人工" {
		t.Fatal("missing original human badge")
	}
	englishUI.Store(true)
	if pool.get(human).badge != "Human" {
		t.Fatalf("cached badge retained Chinese: %q", pool.get(human).badge)
	}
	if pool.values[human].badge != "人工" {
		t.Fatal("display translation mutated stored provenance")
	}
	if pool.get(custom).badge != "◎ 人工 · 中文模型" {
		t.Fatal("custom agent or model was translated")
	}
	if pool.get(1).badge != "◎ Other" {
		t.Fatal("overflow badge retained Chinese")
	}
	englishUI.Store(false)
	if pool.get(human).badge != "人工" {
		t.Fatal("cached badge cannot switch back to Chinese")
	}
}

func TestCachedRingStatusFollowsLanguage(t *testing.T) {
	old := englishUI.Load()
	t.Cleanup(func() { englishUI.Store(old) })
	englishUI.Store(false)
	tab := &repoTab{ring: ringUI{status: "未开启"}}
	a := &app{}
	tt := ui.NewTester(func(c *ui.Context) { a.ringStatusControl(c, tab, "ring") }, 420, 300)
	defer retireTester(tt)
	tt.Frame()
	if !tt.HasText("未开启") {
		t.Fatal("missing Chinese ring status")
	}
	englishUI.Store(true)
	tt.Frame()
	if !tt.HasText("Disabled") || tt.HasText("未开启") {
		t.Fatal("cached ring status did not switch language")
	}
	if tab.ring.status != "未开启" {
		t.Fatal("display mutated the stored ring status")
	}
}

func TestImageDiffNoteKeepsSourceAcrossLanguageSwitch(t *testing.T) {
	old := englishUI.Load()
	englishUI.Store(true)
	t.Cleanup(func() { englishUI.Store(old) })
	source := &fileDiffSource{new: []byte("invalid image")}
	_, _, _, note, err := source.prepare(context.Background(), "image.png", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(note, "新图片解码失败：") {
		t.Fatalf("cached note was translated before display: %q", note)
	}
	for _, r := range trMessage(note) {
		if unicode.Is(unicode.Han, r) {
			t.Fatalf("untranslated image error: %q", trMessage(note))
		}
	}
	englishUI.Store(false)
	if trMessage(note) != note {
		t.Fatal("switching back lost the original diagnostic")
	}
}
