package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type commitDetails struct {
	prov                          provenance
	hash, parents                 string
	author, authorEmail, authored string
	committer, commitEmail, at    string
	message                       string
	formatted                     string
	view                          editor.State
	viewEnglish                   bool
}

func readCommitDetails(ctx context.Context, dir string, hash [20]byte) (*commitDetails, error) {
	out, err := runGitLimit(ctx, dir, 1<<20, "show", "--no-patch", "--format=%H%x00%P%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%B", hashHex(hash), "--")
	if err != nil {
		return nil, err
	}
	f := strings.SplitN(string(out), "\x00", 9)
	if len(f) != 9 {
		return nil, errors.New("无法解析完整提交信息")
	}
	d := &commitDetails{hash: f[0], parents: f[1], author: f[2], authorEmail: f[3], authored: f[4], committer: f[5], commitEmail: f[6], at: f[7], message: strings.TrimRight(f[8], "\n")}
	d.prov, err = messageProvenance(ctx, dir, d.message)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (d *commitDetails) text() string {
	if d.formatted != "" {
		return d.formatted
	}
	parents := d.parents
	if parents == "" {
		parents = tr("无（初始提交）")
	}
	ring := ""
	if p := d.prov; p.agent != "" {
		ring = tr("年轮印记：") + p.description() + "\n"
	}
	return ring + fmt.Sprintf(tr("提交：%s\n父提交：%s\n作者：%s <%s>\n创作时间：%s\n提交者：%s <%s>\n提交时间：%s\n\n%s"), d.hash, parents, d.author, d.authorEmail, d.authored, d.committer, d.commitEmail, d.at, d.message)
}

func (d *commitDetails) updateViewLanguage() {
	english := englishUI.Load()
	if d.view.Buffer != nil && d.viewEnglish == english {
		return
	}
	d.view = editor.State{Buffer: editor.NewBuffer(d.text()), ReadOnly: true, Label: tr("完整提交信息")}
	d.viewEnglish = english
}

func (d *commitDetails) body() string {
	if d == nil {
		return ""
	}
	_, rest, ok := strings.Cut(d.message, "\n")
	if !ok {
		return ""
	}
	return strings.TrimSpace(rest)
}

func (a *app) ensureDetails(t *repoTab) {
	if t == nil || t.details != nil || t.detailsLoading || t.inspecting == zeroHash {
		return
	}
	if t.detailsCancel != nil {
		t.detailsCancel()
	}
	t.detailsGen++
	gen, hash, path := t.detailsGen, t.inspecting, t.path
	t.detailsLoading, t.detailsErr = true, ""
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.detailsCancel = cancel
	go func() {
		defer cancel()
		details, err := readCommitDetails(ctx, path, hash)
		a.post(func() {
			if t.closed || t.detailsGen != gen || t.inspecting != hash {
				return
			}
			t.detailsLoading = false
			if err != nil {
				t.detailsErr = err.Error()
				return
			}
			t.details = details
		})
	}()
}

func (a *app) toggleDetails(t *repoTab) {
	t.detailsOpen = !t.detailsOpen
	if !t.detailsOpen {
		return
	}
	a.ensureDetails(t)
}

func (a *app) clearDetails(t *repoTab) {
	t.detailsGen++
	if t.detailsCancel != nil {
		t.detailsCancel()
		t.detailsCancel = nil
	}
	t.details, t.detailsErr = nil, ""
	t.detailsOpen, t.detailsShown, t.detailsLoading = false, false, false
}

func (a *app) commitDetailsInline(c *ui.Context, t *repoTab) {
	if !t.detailsOpen && !t.detailsShown {
		return
	}
	theme := c.Theme()
	// MyGo's collapsible drives the card: it clips and shrinks the text while
	// the details close, instead of taking the whole card away between frames.
	acc := ui.AccordionBase(c)
	item := acc.Item(&t.detailsOpen)
	progress := item.Progress()
	ui.Column(c.Key(fmt.Sprintf("commit-details-inline-%d", t.id))).FillWidth().Padding(6, 8).Gap(6).Radius(theme.Radius).Background(theme.Surface).Border(1, theme.Border).Children(func() {
		switch {
		case t.detailsLoading:
			busyRow(c, "details-loading", tr("正在读取提交信息…"), "")
		case t.detailsErr != "":
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, trMessage(t.detailsErr)).TextColor(theme.Danger)
				if ui.Button(c.Key("details-retry"), tr("重试")).Clicked() {
					t.detailsOpen = false
					a.toggleDetails(t)
				}
			})
		case t.details != nil:
			t.details.updateViewLanguage()
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr("完整提交信息")).Bold().FontSize(12).SingleLine().Grow(1).MinWidth(0)
				copyIconButton(c, "copy-commit-info", "复制完整信息", t.details.text())
				closeBtn := ui.ButtonBase(c.Key(fmt.Sprintf("close-commit-details-%d", t.id))).Padding(2).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Tooltip(tr("收起信息")).Transition(colorMotion())
				closeBtn.Children(func() {
					ui.Icon(c, iconClose).Size(12, 12).TextColor(theme.TextMuted)
				})
				if closeBtn.Clicked() {
					t.detailsOpen = false
				}
			})
			item.Panel(func() {
				editor.View(c.Key(fmt.Sprintf("commit-info-%d", t.id)), &t.details.view).FillWidth().Height(160).MinHeight(0)
			})
		}
	})
	t.detailsShown = t.detailsOpen || progress > 0.01
}
