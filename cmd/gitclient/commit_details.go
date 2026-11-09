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
	hash, parents                 string
	author, authorEmail, authored string
	committer, commitEmail, at    string
	message                       string
	formatted                     string
	view                          editor.State
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
	d.formatted = d.text()
	d.view = editor.State{Buffer: editor.NewBuffer(d.formatted), ReadOnly: true, Label: "完整提交信息"}
	return d, nil
}

func (d *commitDetails) text() string {
	if d.formatted != "" {
		return d.formatted
	}
	parents := d.parents
	if parents == "" {
		parents = "无（初始提交）"
	}
	return fmt.Sprintf("提交：%s\n父提交：%s\n作者：%s <%s>\n创作时间：%s\n提交者：%s <%s>\n提交时间：%s\n\n%s", d.hash, parents, d.author, d.authorEmail, d.authored, d.committer, d.commitEmail, d.at, d.message)
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
	t.detailsOpen, t.detailsLoading = false, false
}

func (a *app) commitDetailsInline(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	ui.Column(c.Key(fmt.Sprintf("commit-details-inline-%d", t.id))).FillWidth().Padding(6, 8).Gap(6).Radius(theme.Radius).Background(theme.Surface).Border(1, theme.Border).Children(func() {
		switch {
		case t.detailsLoading:
			ui.Text(c, "正在读取…").TextColor(theme.TextMuted)
		case t.detailsErr != "":
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, t.detailsErr).TextColor(theme.Danger)
				if ui.Button(c.Key("details-retry"), "重试").Clicked() {
					t.detailsOpen = false
					a.toggleDetails(t)
				}
			})
		case t.details != nil:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "完整提交信息").Bold().FontSize(12).SingleLine().Grow(1).MinWidth(0)
				if ui.Button(c.Key("copy-commit-info"), "复制完整信息").Shrink(0).Clicked() {
					c.Services().WriteClipboard(t.details.text())
					c.Toast("已复制提交信息")
				}
				closeBtn := ui.ButtonBase(c.Key(fmt.Sprintf("close-commit-details-%d", t.id))).Padding(2).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Tooltip("收起信息").Transition(colorMotion())
				closeBtn.Children(func() {
					ui.Icon(c, iconClose).Size(12, 12).TextColor(theme.TextMuted)
				})
				if closeBtn.Clicked() {
					t.detailsOpen = false
				}
			})
			editor.View(c.Key(fmt.Sprintf("commit-info-%d", t.id)), &t.details.view).FillWidth().Height(160).MinHeight(80)
		}
	})
}
