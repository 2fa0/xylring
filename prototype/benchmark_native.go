//go:build p0bench

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type nativeSample struct {
	ViewBuilds                                      uint64
	Scenario                                        string
	RSSMiB, HeapMiB, FootprintMiB, PeakFootprintMiB float64
	Commits, Tabs                                   int
	At                                              string
}
type nativeReport struct {
	ScrollFrames                    uint64
	ScrollSeconds                   float64
	Platform, GoVersion, Repository string
	FirstPageMS                     float64
	Samples                         []nativeSample
	Error                           string
	SoakMinutes                     int
}

func init() {
	if len(os.Args) < 4 || os.Args[1] != "--p0-bench" {
		return
	}
	repo, out := os.Args[2], os.Args[3]
	onAppReady = func(a *app) {
		a.rendered = &renderStats{}
		a.persist = false
		a.prefs = settings{View: defaultViewSettings()}
		go nativeBenchmark(a, repo, out)
	}
}
func nativeBenchmark(a *app, repo, out string) {
	report := nativeReport{Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Repository: repo}
	uiDo := func(fn func()) { done := make(chan struct{}); a.win.Update(func() { fn(); close(done) }); <-done }
	save := func() { data, _ := json.MarshalIndent(report, "", "  "); os.WriteFile(out, data, 0600) }
	sample := func(name string) {
		debug.FreeOSMemory()
		time.Sleep(2500 * time.Millisecond)
		raw, _ := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
		rss, _ := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
		memory, _ := exec.Command("vmmap", "-summary", strconv.Itoa(os.Getpid())).Output()
		footprint := func(pattern string) float64 {
			matches := regexp.MustCompile(pattern).FindSubmatch(memory)
			if len(matches) < 3 {
				return 0
			}
			v, _ := strconv.ParseFloat(string(matches[1]), 64)
			switch string(matches[2]) {
			case "K":
				return v / 1024
			case "G":
				return v * 1024
			}
			return v
		}
		os.WriteFile(strings.TrimSuffix(out, ".json")+"-"+name+".vmmap.txt", memory, 0600)
		s := nativeSample{FootprintMiB: footprint(`Physical footprint:\s+([0-9.]+)([KMG])`), PeakFootprintMiB: footprint(`Physical footprint \(peak\):\s+([0-9.]+)([KMG])`), Scenario: name, RSSMiB: rss / 1024, At: time.Now().Format(time.RFC3339)}
		uiDo(func() {
			s.ViewBuilds = a.rendered.Builds
			s.HeapMiB = float64(readHeap()) / (1 << 20)
			s.Tabs = len(a.tabs)
			if t := a.activeTab(); t != nil {
				s.Commits = len(t.commits)
			}
		})
		report.Samples = append(report.Samples, s)
		save()
	}
	wait := func(ready func() bool) error {
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			var ok bool
			var failure string
			uiDo(func() {
				ok = ready()
				if t := a.activeTab(); t != nil {
					failure = t.err + " " + t.detailErr
				}
			})
			if strings.TrimSpace(failure) != "" {
				return fmt.Errorf("%s", failure)
			}
			if ok {
				return nil
			}
			time.Sleep(5 * time.Millisecond)
		}
		return fmt.Errorf("benchmark wait timed out")
	}
	finish := func(err error) {
		if err != nil {
			report.Error = err.Error()
		}
		save()
		mygo.App.Quit()
	}
	time.Sleep(1500 * time.Millisecond)
	sample("empty-window")
	start := time.Now()
	uiDo(func() { a.win.Show(); a.openRoot(repo) })
	if err := wait(func() bool {
		t := a.activeTab()
		return t.loaded && !t.loading && a.rendered.TabID == t.id && a.rendered.Commits == len(t.commits)
	}); err != nil {
		finish(err)
		return
	}
	report.FirstPageMS = float64(time.Since(start).Microseconds()) / 1000
	sample("100k-first-300")
	uiDo(func() { a.activeTab().options.More = 5000 })
	for {
		var n int
		uiDo(func() {
			n = len(a.activeTab().commits)
			if n < 50000 {
				a.loadMore(a.activeTab())
			}
		})
		if n >= 50000 {
			break
		}
		if err := wait(func() bool { return !a.activeTab().loading }); err != nil {
			finish(err)
			return
		}
	}
	sample("loaded-50k")
	// Exercise distinct visible ranges through the normal native table.
	var frames uint64
	uiDo(func() { frames = a.rendered.Frames })
	scrollStart := time.Now()
	ticker := time.NewTicker(time.Second / 60)
	for i := 0; i < 240; i++ {
		i := i
		uiDo(func() { t := a.activeTab(); t.list.ScrollTo((i*197)%49000, ui.Start) })
		<-ticker.C
	}
	ticker.Stop()
	report.ScrollSeconds = time.Since(scrollStart).Seconds()
	uiDo(func() { report.ScrollFrames = a.rendered.Frames - frames })
	capture := func(suffix string) {
		if os.Getenv("P0_CAPTURE") == "" {
			return
		}
		png, err := a.win.CapturePage()
		if err == nil {
			os.WriteFile(strings.TrimSuffix(out, ".json")+suffix+".png", png, 0600)
		}
	}
	capture("")
	sample("after-native-scroll")
	uiDo(func() {
		a.activeTab().selected = a.activeTab().commitRow(0)
		a.activeTab().commitApplied = a.activeTab().selected
		a.onCommitSelected(a.activeTab())
	})
	if err := wait(func() bool { return !a.activeTab().detailLoading }); err != nil {
		finish(err)
		return
	}
	uiDo(func() { t := a.activeTab(); t.fileSel = 0; t.fileApplied = 0; a.onFileSelected(t) })
	if err := wait(func() bool { return !a.activeTab().diffLoading && a.rendered.Diff }); err != nil {
		finish(err)
		return
	}
	sample("10k-line-diff")
	capture("-diff")
	sample("10k-line-diff-rendered")
	uiDo(func() { a.closeDetails(a.activeTab()) })
	sample("diff-closed")
	for i := 0; i < 9; i++ {
		uiDo(func() {
			t := a.activeTab()
			t.detailHidden = false
			t.selected = t.commitRow(0)
			t.commitApplied = t.selected
			a.onCommitSelected(t)
		})
		if err := wait(func() bool { return !a.activeTab().detailLoading }); err != nil {
			finish(err)
			return
		}
		uiDo(func() { t := a.activeTab(); t.fileSel = 0; t.fileApplied = 0; a.onFileSelected(t) })
		if err := wait(func() bool { return !a.activeTab().diffLoading && a.rendered.Diff }); err != nil {
			finish(err)
			return
		}
		uiDo(func() { a.closeDetails(a.activeTab()) })
		if err := wait(func() bool { return !a.rendered.Diff }); err != nil {
			finish(err)
			return
		}
	}
	sample("after-ten-diffs")

	// Separate clone directories exercise real repository tabs while sharing object storage.
	cloneRoot, err := os.MkdirTemp("", "gitclient-bench-tabs-")
	if err != nil {
		finish(err)
		return
	}
	defer os.RemoveAll(cloneRoot)
	for i := 0; i < 4; i++ {
		path := filepath.Join(cloneRoot, strconv.Itoa(i))
		if _, err := runGit(context.Background(), repo, "clone", "--shared", "--quiet", repo, path); err != nil {
			finish(err)
			return
		}
		uiDo(func() { a.openRoot(path) })
		if err := wait(func() bool { return a.activeTab().loaded }); err != nil {
			finish(err)
			return
		}
	}
	sample("five-tabs-four-background")
	minutes, _ := strconv.Atoi(os.Getenv("P0_SOAK_MINUTES"))
	if minutes > 0 {
		uiDo(func() {
			for _, t := range a.tabs {
				t.resumeCount = pageFirst
			}
		})
		report.SoakMinutes = minutes
		sample("soak-start")
		deadline := time.Now().Add(time.Duration(minutes) * time.Minute)
		for tick := 0; time.Now().Before(deadline); tick++ {
			uiDo(func() {
				if tick%4 == 0 {
					a.activate((a.active + 1) % len(a.tabs))
				} else {
					t := a.activeTab()
					if t.loaded {
						a.reload(t)
					}
				}
			})
			if err := wait(func() bool { return a.activeTab().loaded && !a.activeTab().loading }); err != nil {
				finish(err)
				return
			}
			time.Sleep(5 * time.Second)
			if tick%12 == 11 {
				sample("soak-minute-" + strconv.Itoa(tick/12+1))
			}
		}
		uiDo(func() { a.activate(len(a.tabs) - 1) })
		if err := wait(func() bool { return a.activeTab().loaded && !a.activeTab().loading }); err != nil {
			finish(err)
			return
		}
		sample("soak-end")
	}
	// Compare identical foreground data after closing only the background tabs.
	uiDo(func() {
		for len(a.tabs) > 1 {
			if a.active == 0 {
				a.closeTab(1)
			} else {
				a.closeTab(0)
			}
		}
	})
	sample("one-tab-after-background-close")
	finish(nil)
}
