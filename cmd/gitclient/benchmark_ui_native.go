//go:build p0bench

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

type captureShot struct {
	Name                   string
	RequestedW, RequestedH int
	ContentW, ContentH     int
	PixelsW, PixelsH       int
	Bytes                  int
	Error                  string
}

type captureManifest struct {
	Method     string
	Repository string
	Commit     string
	Shots      []captureShot
	AXQuery    string
	AXError    string
	Error      string
}

// Captures a real repository, separately from memory measurements.
func init() {
	if len(os.Args) < 4 || os.Args[1] != "--ui-capture" {
		return
	}
	repo, out := os.Args[2], os.Args[3]
	onAppReady = func(a *app) {
		a.persist = false
		a.rendered = &renderStats{}
		a.prefs = settings{View: defaultViewSettings()}
		go func() {
			defer mygo.App.Quit()
			manifest := captureManifest{
				Method:     "Window.CapturePage content render; not a window-server screenshot and not the headless tester",
				Repository: repo,
			}
			fail := func(err error) {
				fmt.Fprintln(os.Stderr, err)
				manifest.Error = err.Error()
				writeCaptureManifest(out, manifest)
				benchExit = 1
			}
			do := func(fn func()) { done := make(chan struct{}); a.win.Update(func() { fn(); close(done) }); <-done }
			wait := func(ready func() bool) bool {
				deadline := time.Now().Add(20 * time.Second)
				for time.Now().Before(deadline) {
					ok := false
					do(func() { ok = ready() })
					if ok {
						return true
					}
					time.Sleep(10 * time.Millisecond)
				}
				return false
			}
			if err := os.MkdirAll(out, 0700); err != nil {
				fail(err)
				return
			}
			do(func() { a.openRoot(repo) })
			if !wait(func() bool { return a.activeTab().loaded && len(a.activeTab().commits) > 0 }) {
				fail(fmt.Errorf("仓库历史没有在时限内出现"))
				return
			}
			do(func() {
				t := a.activeTab()
				t.selected = t.commitRow(0)
				for i := 0; i < t.rows(); i++ {
					if c := t.rowData(i); c != nil && c.subject == "看清这次改动，主题保持单行" {
						t.selected = i
						break
					}
				}
				t.commitApplied = t.selected
				a.onCommitSelected(t)
			})
			if !wait(func() bool { return !a.activeTab().detailLoading && len(a.activeTab().files) > 0 }) {
				fail(fmt.Errorf("提交详情没有在时限内出现"))
				return
			}
			missingFile := false
			do(func() {
				t := a.activeTab()
				t.fileSel = -1
				for i, f := range t.files {
					if f.path == "src/history.go" {
						t.fileSel = i
						break
					}
				}
				if t.fileSel < 0 {
					missingFile = true
					return
				}
				t.fileApplied = t.fileSel
				a.onFileSelected(t)
				if c := t.selectedCommit(); c != nil {
					manifest.Commit = hashHex(c.hash)
				}
			})
			if missingFile {
				fail(fmt.Errorf("目标提交里没有 src/history.go"))
				return
			}
			if !wait(func() bool { return a.activeTab().diff != nil && a.rendered.Diff && !a.activeTab().diffLoading }) {
				fail(fmt.Errorf("diff 没有在时限内出现"))
				return
			}
			capture := func(name string, w, h int, focus, closed bool) {
				shot := captureShot{Name: name, RequestedW: w, RequestedH: h}
				do(func() {
					tab := a.activeTab()
					if closed {
						a.closeDetails(tab)
					} else if tab.detailHidden {
						tab.detailHidden = false
						tab.selected = tab.commitRow(0)
						tab.commitApplied = tab.selected
						a.onCommitSelected(tab)
					}
					tab.focusDiff = focus
					a.win.SetContentSize(w, h)
				})
				if !closed {
					if !wait(func() bool { return a.activeTab().diff != nil && !a.activeTab().diffLoading }) {
						shot.Error = "resize 后 diff 没有恢复"
						manifest.Shots = append(manifest.Shots, shot)
						return
					}
				}
				time.Sleep(300 * time.Millisecond)
				data, err := a.win.CapturePage()
				if err != nil {
					shot.Error = err.Error()
				} else if len(data) == 0 {
					shot.Error = "截图为空"
				} else {
					cfg, derr := png.DecodeConfig(bytes.NewReader(data))
					if derr != nil {
						shot.Error = derr.Error()
					} else {
						shot.PixelsW, shot.PixelsH = cfg.Width, cfg.Height
					}
					path := filepath.Join(out, name+".png")
					if werr := os.WriteFile(path, data, 0600); werr != nil && shot.Error == "" {
						shot.Error = werr.Error()
					}
					shot.Bytes = len(data)
				}
				do(func() {
					shot.ContentW, shot.ContentH = a.win.ContentSize()
				})
				manifest.Shots = append(manifest.Shots, shot)
				if shot.Error != "" {
					fmt.Fprintln(os.Stderr, name, shot.Error)
				}
			}
			capture("native-wide", 1200, 800, false, false)
			capture("native-narrow", 860, 560, false, false)
			capture("native-focused", 1200, 800, true, false)
			capture("native-wide-closed", 1200, 800, false, true)
			manifest.AXQuery, manifest.AXError = queryAccessibility()
			for _, shot := range manifest.Shots {
				if shot.Error != "" || shot.Bytes == 0 {
					fail(fmt.Errorf("原生截图不完整：%s", shot.Name))
					return
				}
			}
			writeCaptureManifest(out, manifest)
		}()
	}
}

func writeCaptureManifest(out string, manifest captureManifest) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), append(data, '\n'), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func queryAccessibility() (string, string) {
	script := `tell application "System Events"
	if not (exists process "gitclient-trial") then
		return "process gitclient-trial missing"
	end if
	tell process "gitclient-trial"
		set frontmost to true
		return description of every UI element of window 1
	end tell
end tell`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err.Error()
	}
	return string(out), ""
}
