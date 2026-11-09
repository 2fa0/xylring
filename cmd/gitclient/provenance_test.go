package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestProvenanceVerifiedSignatures(t *testing.T) {
	b, err := os.ReadFile("testdata/provenance/cindy.txt")
	if err != nil {
		t.Fatal(err)
	}
	if p := parseProvenance(string(b)); p.agent != "Cindy" || p.model != "" {
		t.Fatalf("%+v", p)
	}
	for _, s := range []string{"Co-authored-by: Claude Opus 4.6 <human@example.com>", "Co-authored-by: Cindy <human@example.com>", "Assisted-by: Cindy"} {
		if parseProvenance(s).agent != "" {
			t.Fatalf("false match %q", s)
		}
	}
	p := parseProvenance("AI-Agent: Codex\x1dAI-Model: gpt-5-codex\x1dAI-Attribution: stamp\x1dAssisted-by: Cindy (https://cindy.app)")
	if p.agent != "Codex" || normalizeModel(p.model) != "GPT-5 Codex" || p.attribution != "stamp" {
		t.Fatalf("%+v", p)
	}
	if normalizeModel("unknown-model") != "unknown-model" {
		t.Fatal("unknown model lost")
	}
}
func TestProvenanceRecordAndPool(t *testing.T) {
	pool := &provenanceTable{}
	rec := "0123456789abcdef0123456789abcdef01234567\x1f\x1fAda\x1f1700000000\x1fsubject\x1fseparator\x1fAI-Agent: Cindy"
	c, err := parseCommitRecord(rec, pool)
	if err != nil {
		t.Fatal(err)
	}
	if c.subject != "subject\x1fseparator" || pool.get(c.prov).agent != "Cindy" {
		t.Fatalf("%+v", c)
	}
	if pool.intern(provenance{agent: "Cindy"}) != c.prov {
		t.Fatal("not interned")
	}
	for i := 0; i < 66000; i++ {
		pool.intern(provenance{agent: fmt.Sprint(i)})
	}
	if len(pool.snapshot()) != 65536 || pool.get(pool.intern(provenance{agent: "overflow"})).agent != "其他" {
		t.Fatal("unbounded pool")
	}
	// Mirrors the original layout solely to guard the per-commit memory budget.
	type original struct {
		hash               [20]byte
		parents            [][20]byte
		author             string
		when               int64
		subject            string
		nodeCol, nodeColor int
		segs               []seg
		file               *fileChange
	}
	if unsafe.Sizeof(commit{}) > unsafe.Sizeof(original{})+8 {
		t.Fatal("commit grew more than eight bytes")
	}
}
func TestApplyProvenanceTrailers(t *testing.T) {
	dir, _ := initTestRepo(t)
	ctx := context.Background()
	p := provenance{agent: "Cindy", model: "exact-model"}
	for _, msg := range []string{"subject\n\nbody\n\nSigned-off-by: Human <h@example.com>\n", "subject\n\n# comment\n", "subject\n\n# ------------------------ >8 ------------------------\n# ignored\n"} {
		out, err := applyProvenanceTrailers(ctx, dir, msg, p)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := applyProvenanceTrailers(ctx, dir, out, p)
		if err != nil || twice != out {
			t.Fatalf("not idempotent: %q / %q %v", out, twice, err)
		}
		clean, err := applyProvenanceTrailers(ctx, dir, out, provenance{})
		if err != nil || strings.Contains(clean, "AI-Agent:") || !strings.Contains(clean, "subject") {
			t.Fatalf("clear: %q %v", clean, err)
		}
	}
	out, err := applyProvenanceTrailers(ctx, dir, "subject\n\nAI-Agent: old\nAI-Agent: older\n", p)
	if err != nil || strings.Count(out, "AI-Agent:") != 1 {
		t.Fatalf("duplicate replacement: %q %v", out, err)
	}
	if _, err := applyProvenanceTrailers(ctx, dir, "s", provenance{agent: "bad\nInjected: x"}); err == nil {
		t.Fatal("newline allowed")
	}
}
func TestProvenanceSearchPagination(t *testing.T) {
	dir, git := initTestRepo(t)
	for i := 0; i < 12; i++ {
		msg := fmt.Sprintf("subject %d\n\nCo-authored-by: Cindy <human@example.com>", i)
		if i%3 == 0 {
			msg = fmt.Sprintf("subject %d\n\nAI-Agent: Cindy\nAI-Model: gpt-5-codex", i)
		}
		git("commit", "--allow-empty", "-m", msg)
	}
	pool := &provenanceTable{}
	q := historyQuery{kind: "Agent", term: "cindy", provs: pool}
	first := readQuery(t, dir, q, 0, 2)
	next := readQuery(t, dir, q, 2, 2)
	if len(first) != 2 || len(next) != 2 || first[0].hash == next[0].hash {
		t.Fatalf("paging: %d %d", len(first), len(next))
	}
	q.kind = "模型"
	q.term = "GPT-5 Codex"
	if got := readQuery(t, dir, q, 0, 10); len(got) != 4 {
		t.Fatalf("alias search: %d", len(got))
	}
	q.term = ".*"
	if got := readQuery(t, dir, q, 0, 10); len(got) != 0 {
		t.Fatal("regex treated as literal incorrectly")
	}
}
func ringTestRepo(t *testing.T) (string, func(...string) []byte) {
	dir, git := initTestRepo(t)
	git("config", "--unset", "core.hooksPath")
	return dir, git
}
func TestRingHookStampLifecycle(t *testing.T) {
	ctx := context.Background()
	dir, git := ringTestRepo(t)
	if err := installRingHook(ctx, dir); err != nil {
		t.Fatal(err)
	}
	stamp := ringStamp{p: provenance{agent: "Cindy", model: "model"}, expires: time.Now().Add(time.Minute).Unix()}
	if err := writeStamp(ctx, dir, stamp); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		git("commit", "--allow-empty", "-m", "stamped")
		p := parseProvenance(string(git("log", "-1", "--format=%B")))
		if p.agent != "Cindy" || p.attribution != "stamp" {
			t.Fatalf("%+v", p)
		}
	}
	stamp.once = true
	if err := writeStamp(ctx, dir, stamp); err != nil {
		t.Fatal(err)
	}
	git("commit", "--allow-empty", "-m", "once")
	git("commit", "--allow-empty", "-m", "human")
	if strings.Contains(string(git("log", "-1", "--format=%B")), "AI-Agent:") {
		t.Fatal("once reused")
	}
	stamp.expires = time.Now().Add(-time.Minute).Unix()
	writeStamp(ctx, dir, stamp)
	git("commit", "--allow-empty", "-m", "expired")
	if strings.Contains(string(git("log", "-1", "--format=%B")), "AI-Agent:") {
		t.Fatal("expired used")
	}
	stamp.expires = time.Now().Add(time.Minute).Unix()
	stamp.once = false
	writeStamp(ctx, dir, stamp)
	git("commit", "--amend", "--allow-empty", "--no-edit")
	if strings.Contains(string(git("log", "-1", "--format=%B")), "AI-Agent:") {
		t.Fatal("amend stamped")
	}
	for _, env := range []string{"XYLRING_GUI=1", "XYLRING_RING=0"} {
		if _, err := runGitExtra(ctx, dir, 1<<20, []string{env}, "commit", "--allow-empty", "-m", "human"); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(git("log", "-1", "--format=%B")), "AI-Agent:") {
			t.Fatal("override ignored")
		}
	}
	for _, native := range []string{"Assisted-by: Cindy (https://cindy.app)", "AI-Agent: Explicit"} {
		git("commit", "--allow-empty", "-m", "native\n\n"+native)
		if strings.Contains(string(git("log", "-1", "--format=%B")), "AI-Attribution: stamp") {
			t.Fatal("explicit signature overwritten")
		}
	}
}
func TestRingHookChainAndManagedPath(t *testing.T) {
	ctx := context.Background()
	dir, git := ringTestRepo(t)
	path, _ := gitFile(ctx, dir, "hooks/prepare-commit-msg")
	original := []byte("#!/bin/sh\nexit 23\n")
	if err := os.WriteFile(path, original, 0755); err != nil {
		t.Fatal(err)
	}
	if err := installRingHook(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := installRingHook(ctx, dir); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", path, "unused", "message")
	cmd.Dir = dir
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 23 {
		t.Fatalf("exit: %v", err)
	}
	if err := uninstallRingHook(ctx, dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Equal(b, original) {
		t.Fatal("original not restored")
	}
	git("config", "core.hooksPath", ".husky")
	if err := installRingHook(ctx, dir); err == nil {
		t.Fatal("managed path changed")
	}
	if !strings.Contains(ringHookStatus(ctx, dir), "需手动集成") {
		t.Fatal("managed status")
	}
}
func TestRingLinkedWorktree(t *testing.T) {
	ctx := context.Background()
	dir, git := ringTestRepo(t)
	git("commit", "--allow-empty", "-m", "initial")
	other := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "-b", "linked", other)
	if err := installRingHook(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := writeStamp(ctx, other, ringStamp{p: provenance{agent: "Cindy"}, expires: time.Now().Add(time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, other, "commit", "--allow-empty", "-m", "linked"); err != nil {
		t.Fatal(err)
	}
	out, _ := runGit(ctx, other, "log", "-1", "--format=%B")
	if !strings.Contains(string(out), "AI-Agent: Cindy") {
		t.Fatalf("%s", out)
	}
	s, _ := readStamp(ctx, dir)
	if s.p.agent != "" {
		t.Fatal("stamp leaked across worktrees")
	}
}
func TestRulePreviewAndRecentProvenance(t *testing.T) {
	dir := t.TempDir()
	p, err := previewRuleSnippet(dir, "AGENTS.md", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.path); !os.IsNotExist(err) {
		t.Fatal("preview wrote file")
	}
	if err := saveRulePreview(p); err != nil {
		t.Fatal(err)
	}
	again, err := previewRuleSnippet(dir, "AGENTS.md", false)
	if err != nil || again.diff != "" {
		t.Fatalf("not idempotent %v %q", err, again.diff)
	}
	removed, _ := previewRuleSnippet(dir, "AGENTS.md", true)
	if strings.Contains(string(removed.after), "xylring:provenance:") {
		t.Fatal("not removed")
	}
	os.WriteFile(p.path, []byte("concurrent edit"), 0644)
	if saveRulePreview(removed) == nil {
		t.Fatal("clobbered edit")
	}
	a := &app{}
	for i := 0; i < 12; i++ {
		a.rememberProvenance(provenance{agent: fmt.Sprint(i)})
	}
	if len(a.recentProvenance) != 8 {
		t.Fatal("recent cap")
	}
	tab := newTab(1, dir)
	if tab.commitProv.agent != "" {
		t.Fatal("auto selected")
	}
}
func TestRingUIControls(t *testing.T) {
	dir := t.TempDir()
	tab := newTab(1, dir)
	tab.ring.checked = time.Now()
	tab.ring.status = "未开启"
	tab.commitRingOpen = true
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { a.commitRingControl(c, tab) }) }, 500, 350)
	tt.Frame()
	if _, ok := tt.Find("AI Agent"); !ok {
		t.Fatal("Agent input absent")
	}
	if err := tt.Click("AI Agent"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Cindy")
	tt.Frame()
	if tab.commitProv.agent != "Cindy" || !tab.commitProvEdited {
		t.Fatal("input not committed")
	}
	if path := os.Getenv("XYLRING_TEST_SCREENSHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, tt.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.Click("清空年轮"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if tab.commitProv.agent != "" || !tab.commitProvEdited {
		t.Fatal("clear not applied")
	}

}

func TestRingHookSourceSkipsAndFailure(t *testing.T) {
	dir, _ := ringTestRepo(t)
	ctx := context.Background()
	path, _ := gitFile(ctx, dir, "hooks/prepare-commit-msg")
	if err := installRingHook(ctx, dir); err != nil {
		t.Fatal(err)
	}
	writeStamp(ctx, dir, ringStamp{p: provenance{agent: "Cindy"}, expires: time.Now().Add(time.Minute).Unix()})
	msg := filepath.Join(dir, "message")
	for _, source := range []string{"commit", "merge", "squash"} {
		os.WriteFile(msg, []byte("human\n"), 0600)
		cmd := exec.Command("sh", path, msg, source)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
		b, _ := os.ReadFile(msg)
		if string(b) != "human\n" {
			t.Fatalf("%s modified message", source)
		}
	}
	// Missing message makes interpret-trailers fail; the hook must still succeed.
	cmd := exec.Command("sh", path, filepath.Join(dir, "missing"), "message")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatal("hook failure blocked commit", err)
	}
}
func TestRingCLIAndLauncher(t *testing.T) {
	dir, _ := ringTestRepo(t)
	ctx := context.Background()
	var out bytes.Buffer
	for _, args := range [][]string{{"ring", "install"}, {"stamp", "--agent", "Cindy", "--model", "known", "--once"}, {"stamp", "--show"}, {"stamp", "--clear"}, {"ring", "uninstall"}} {
		if err := runRingCLI(ctx, dir, args, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if !strings.Contains(out.String(), "known") {
		t.Fatal(out.String())
	}
	bin := filepath.Join(t.TempDir(), "fake-xylring")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(t.TempDir(), "xylring")
	os.WriteFile(launcher, []byte(generateCLIScriptContent("/missing/app", bin)), 0755)
	for _, sub := range []string{"ring", "stamp"} {
		cmd := exec.Command("bash", launcher, sub, "--show")
		got, err := cmd.CombinedOutput()
		if err != nil || string(got) != sub+"\n--show\n" {
			t.Fatalf("launcher %q %v", got, err)
		}
	}
	if !strings.Contains(generateWindowsCmdContent("binary"), "goto cli") {
		t.Fatal("Windows has no direct route")
	}
}

func TestProvenanceHistoryPerformance(t *testing.T) {
	dir, _ := initTestRepo(t)
	var input strings.Builder
	for i := 0; i < 10000; i++ {
		msg := fmt.Sprintf("commit %d\n", i)
		if i%2 == 0 {
			msg += "\nAI-Agent: Cindy\nAI-Model: exact-model\n"
		}
		fmt.Fprintf(&input, "commit refs/heads/main\ncommitter Test <test@example.com> %d +0000\ndata %d\n%s\n", 1700000000+i, len(msg), msg)
	}
	cmd := exec.Command("git", "-C", dir, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(input.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	ctx := context.Background()
	read := func(withProvenance bool) (time.Duration, int64) {
		pool := &provenanceTable{}
		format := commitFormat
		if withProvenance {
			format += "%x1f" + provenanceFormat
		}
		rows := make([]commit, 0, 10000)
		start := time.Now()
		err := streamLog(ctx, dir, []string{"log", "--format=" + format, "HEAD", "--"}, "", splitByte(0x1e), func(sc *bufio.Scanner, _ *gitLease) error {
			for sc.Scan() {
				rec := strings.TrimRight(sc.Text(), "\r\n")
				if rec == "" {
					continue
				}
				var c commit
				var err error
				if withProvenance {
					c, err = parseCommitRecord(rec, pool)
				} else {
					c, err = parseCommitRecord(rec)
				}
				if err != nil {
					return err
				}
				rows = append(rows, c)
			}
			return sc.Err()
		})
		if err != nil || len(rows) != 10000 {
			t.Fatalf("history %d %v", len(rows), err)
		}
		elapsed := time.Since(start)
		var size int64
		for i := range rows {
			size += commitBytes(&rows[i])
		}
		return elapsed, size
	}
	read(false)
	read(true)
	var base, newTime time.Duration
	var baseBytes, newBytes int64
	for i := 0; i < 5; i++ {
		d, n := read(false)
		base += d
		baseBytes = n
		d, n = read(true)
		newTime += d
		newBytes = n
	}
	t.Logf("10,000 commits, 50%% provenance: baseline=%s ring=%s overhead=%.1f%%; commit estimate delta=%d bytes", base/5, newTime/5, (float64(newTime)/float64(base)-1)*100, newBytes-baseBytes)
	if newBytes-baseBytes > 200000 {
		t.Fatal("commit memory budget exceeded")
	}
}
