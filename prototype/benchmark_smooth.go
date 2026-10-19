//go:build p0bench

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type latencySeries struct {
	N               int
	MinMS, MedianMS float64
	P95MS, MaxMS    float64
	SamplesMS       []float64
	Missed          int
}

type scrollSeries struct {
	Seconds       float64
	Frames        int
	IntervalP95MS float64
	IntervalMaxMS float64
	Over100MS     int
	GapsOver20MS  int
}

type smoothReport struct {
	Method         string
	InputMethod    string
	ScrollMethod   string
	IdleMethod     string
	Platform       string
	GoVersion      string
	ReduceMotion   bool
	AppReadsMotion bool
	AppTransitions string
	Selection      latencySeries
	TabSwitch      latencySeries
	DiffFirst      latencySeries
	DiffRepeat     latencySeries
	Scroll         scrollSeries
	IdleSeconds    float64
	IdleFrames     int
	IdleCPUPercent []string
	Error          string
}

func init() {
	if len(os.Args) < 5 || os.Args[1] != "--smooth-probe" {
		return
	}
	repoA, repoB, out := os.Args[2], os.Args[3], os.Args[4]
	onAppReady = func(a *app) {
		a.persist = false
		a.rendered = &renderStats{}
		a.prefs = settings{View: defaultViewSettings()}
		go func() {
			defer mygo.App.Quit()
			report := runSmoothProbe(a, repoA, repoB)
			if report.Error != "" {
				fmt.Fprintln(os.Stderr, report.Error)
				benchExit = 1
			}
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				benchExit = 1
				return
			}
			if err := os.WriteFile(out, append(data, '\n'), 0600); err != nil {
				fmt.Fprintln(os.Stderr, err)
				benchExit = 1
			}
		}()
	}
}

func runSmoothProbe(a *app, repoA, repoB string) smoothReport {
	report := smoothReport{
		Method:         "view-build proxy: time from Window.Update dispatch until the next view build that shows the new state. Not display scanout, not a GPU present timestamp.",
		InputMethod:    "handler dispatch on the same functions a click or list selection calls. Pointer and key events were not injected, so click and keyboard are not separate series.",
		ScrollMethod:   "programmatic list.ScrollTo requested at 60Hz. Intervals are successive view-build times, not display frame intervals.",
		IdleMethod:     "details closed, no text field focused by this probe. Frame count is view builds, not window-server redraws.",
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		GoVersion:      runtime.Version(),
		AppReadsMotion: false,
		AppTransitions: "none; no entrance, route, or row animation in this app",
	}
	do := func(fn func()) {
		done := make(chan struct{})
		a.win.Update(func() { fn(); close(done) })
		<-done
	}
	wait := func(ready func() bool, limit time.Duration) bool {
		deadline := time.Now().Add(limit)
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
	do(func() {
		report.ReduceMotion = mygo.Theme.Preferences().ReduceMotion
		a.openRoot(repoA)
	})
	if !wait(func() bool { return a.activeTab() != nil && a.activeTab().loaded && a.activeTab().rows() >= 31 }, 20*time.Second) {
		report.Error = "测量仓库没有加载出足够的提交"
		return report
	}
	do(func() { a.openRoot(repoB) })
	if !wait(func() bool {
		t := a.activeTab()
		return t != nil && t.path == repoB && t.loaded
	}, 20*time.Second) {
		report.Error = "第二个仓库没有加载"
		return report
	}

	report.TabSwitch = measureSeries(a, do, 30, func(i int) (func(), func() bool) {
		want := i % 2
		return func() { a.activate(want) }, func() bool { return a.active == want }
	})
	do(func() { a.activate(0) })
	if !wait(func() bool {
		t := a.activeTab()
		return t != nil && t.path == repoA && t.loaded && !t.loading
	}, 20*time.Second) {
		report.Error = "切回测量仓库后没有恢复"
		return report
	}

	report.Selection = measureSeries(a, do, 30, func(i int) (func(), func() bool) {
		row := 1 + (i % 30)
		return func() {
				t := a.activeTab()
				t.selected = row
				t.commitApplied = row
				a.onCommitSelected(t)
			}, func() bool {
				t := a.activeTab()
				return t != nil && t.selected == row
			}
	})

	foundDiffCommit := false
	do(func() {
		t := a.activeTab()
		for i := 0; i < t.rows(); i++ {
			if c := t.rowData(i); c != nil && c.subject == "看清这次改动，主题保持单行" {
				t.selected = i
				t.commitApplied = i
				a.onCommitSelected(t)
				foundDiffCommit = true
				return
			}
		}
	})
	if !foundDiffCommit {
		report.Error = "没有找到包含普通文本 diff 的提交"
		return report
	}
	if !wait(func() bool { return !a.activeTab().detailLoading && len(a.activeTab().files) >= 30 }, 20*time.Second) {
		report.Error = "没有足够的普通文本文件可测 diff"
		return report
	}
	report.DiffFirst = measureDiffs(a, do, wait, false)
	report.DiffRepeat = measureDiffs(a, do, wait, true)
	if report.DiffFirst.N < 30 || report.DiffRepeat.N < 30 {
		report.Error = "普通 diff 样本不足 30"
		return report
	}

	report.Scroll = measureScroll(a, do)
	report.IdleSeconds, report.IdleFrames, report.IdleCPUPercent = measureIdle(a, do)
	return report
}

func measureSeries(a *app, do func(func()), n int, step func(int) (func(), func() bool)) latencySeries {
	samples := make([]float64, 0, n)
	missed := 0
	for i := 0; i < n; i++ {
		apply, ready := step(i)
		ms, ok := waitFrame(a, do, apply, ready, time.Second)
		if !ok {
			missed++
			continue
		}
		samples = append(samples, ms)
	}
	return summarize(samples, missed)
}

func measureDiffs(a *app, do func(func()), wait func(func() bool, time.Duration) bool, repeat bool) latencySeries {
	var indexes []int
	do(func() {
		t := a.activeTab()
		for i, f := range t.files {
			if len(indexes) == 30 {
				break
			}
			if f.path == "src/history.go" {
				continue
			}
			indexes = append(indexes, i)
		}
	})
	samples := make([]float64, 0, len(indexes))
	missed := 0
	for _, index := range indexes {
		start := time.Now()
		do(func() {
			t := a.activeTab()
			t.fileSel = index
			t.fileApplied = index
			a.onFileSelected(t)
		})
		ok := wait(func() bool {
			t := a.activeTab()
			return t.fileSel == index && !t.diffLoading && t.diff != nil && a.rendered.Diff
		}, 5*time.Second)
		if !ok {
			missed++
			continue
		}
		samples = append(samples, float64(time.Since(start).Microseconds())/1000)
		if !repeat {
			do(func() {
				t := a.activeTab()
				t.diff = nil
				a.rendered.Diff = false
			})
		}
	}
	return summarize(samples, missed)
}

func waitFrame(a *app, do func(func()), apply func(), ready func() bool, limit time.Duration) (float64, bool) {
	start := time.Now()
	got := make(chan struct{}, 1)
	setFrameHook(func(app *app, _ time.Time) {
		if ready() {
			select {
			case got <- struct{}{}:
			default:
			}
		}
	})
	do(apply)
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-got:
		setFrameHook(nil)
		return float64(time.Since(start).Microseconds()) / 1000, true
	case <-timer.C:
		setFrameHook(nil)
		return 0, false
	}
}

func measureScroll(a *app, do func(func())) scrollSeries {
	var stamps []time.Time
	setFrameHook(func(_ *app, now time.Time) {
		if len(stamps) == 0 || !now.Equal(stamps[len(stamps)-1]) {
			stamps = append(stamps, now)
		}
	})
	start := time.Now()
	ticker := time.NewTicker(time.Second / 60)
	for i := 0; i < 1800; i++ {
		i := i
		do(func() {
			t := a.activeTab()
			if t.rows() > 1 {
				t.list.ScrollTo((i*3)%(t.rows()-1), ui.Start)
			}
		})
		<-ticker.C
	}
	ticker.Stop()
	do(func() { setFrameHook(nil) })
	series := scrollSeries{Seconds: time.Since(start).Seconds(), Frames: len(stamps)}
	if len(stamps) < 2 {
		return series
	}
	gaps := make([]float64, 0, len(stamps)-1)
	for i := 1; i < len(stamps); i++ {
		ms := float64(stamps[i].Sub(stamps[i-1]).Microseconds()) / 1000
		gaps = append(gaps, ms)
		if ms > 20 {
			series.GapsOver20MS++
		}
		if ms > 100 {
			series.Over100MS++
		}
	}
	sort.Float64s(gaps)
	series.IntervalP95MS = percentile(gaps, 0.95)
	series.IntervalMaxMS = gaps[len(gaps)-1]
	return series
}

func measureIdle(a *app, do func(func())) (float64, int, []string) {
	do(func() {
		t := a.activeTab()
		a.closeDetails(t)
		t.focusDiff = false
		t.showRef = false
	})
	frames := 0
	setFrameHook(func(_ *app, _ time.Time) { frames++ })
	start := time.Now()
	var cpu []string
	deadline := start.Add(60 * time.Second)
	for time.Now().Before(deadline) {
		cpu = append(cpu, processCPU())
		time.Sleep(5 * time.Second)
	}
	do(func() { setFrameHook(nil) })
	return time.Since(start).Seconds(), frames, cpu
}

func processCPU() string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(os.Getpid()), "-o", "%cpu=").Output()
	if err != nil {
		return "unavailable"
	}
	return string(bytesTrim(out))
}

func bytesTrim(b []byte) string {
	return string(trimSpace(b))
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\n' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}

func summarize(samples []float64, missed int) latencySeries {
	series := latencySeries{N: len(samples), Missed: missed, SamplesMS: samples}
	if len(samples) == 0 {
		return series
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	series.MinMS = sorted[0]
	series.MaxMS = sorted[len(sorted)-1]
	series.MedianMS = percentile(sorted, 0.50)
	series.P95MS = percentile(sorted, 0.95)
	return series
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
