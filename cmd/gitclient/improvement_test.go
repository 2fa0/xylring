package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	editor "github.com/egoist/mygo/gitclient-editor"
	"github.com/egoist/mygo/ui"
)

func readingFixture() (*app, *repoTab) {
	tab := newTab(1, "/tmp/reading-fixture")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{{hash: hashN(1), author: "Ada", subject: "Read changes", when: 1700000000}}
	tab.graph.add(&tab.commits[0])
	tab.selected, tab.commitApplied = 0, 0
	tab.files = []fileChange{{status: "M", path: "file.go"}}
	tab.fileSel, tab.fileApplied = 0, 0
	tab.leftPane = "改动"
	tab.diffOpen = true
	tab.shownKey = tab.selectionKey()
	var before, after strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&before, "before %03d\n", i)
		fmt.Fprintf(&after, "after %03d\n", i)
	}
	tab.diff = &editor.DiffState{Old: editor.NewBuffer(before.String()), New: editor.NewBuffer(after.String())}
	tab.diff.Font.Family, tab.diff.Font.Size = "monospace", 13
	return &app{tabs: []*repoTab{tab}}, tab
}

func TestReadingSpaceAndFocusReturn(t *testing.T) {
	for _, size := range []struct {
		w, h, lines int
		focus       bool
	}{{1200, 800, 16, false}, {860, 560, 8, false}, {1200, 800, 28, true}} {
		t.Run(fmt.Sprintf("%dx%d-focus-%v", size.w, size.h, size.focus), func(t *testing.T) {
			a, tab := readingFixture()
			tab.focusDiff = size.focus
			tt := ui.NewTester(a.view, size.w, size.h)
			// The toolbar ends where the editor viewport begins; use its geometry,
			// not the number of model lines, to guard against losing reading space.
			bar, ok := tt.Find("并排")
			if !ok {
				t.Fatal("missing diff toolbar")
			}
			available := float32(size.h) - 23 - (bar.Y + bar.H + 4)
			// MyGo's default editor line height for 13 DIP text is 20 DIPs.
			if available < float32(size.lines*20) {
				t.Fatalf("diff viewport %.1f, need %d", available, size.lines*20)
			}
			original := tab.diff
			if !tt.HasText("main · Ada") {
				t.Fatal("reading header lost the branch")
			}
			if !size.focus {
				click(t, tt, "放大 diff")
			}
			click(t, tt, "返回")
			if tab.focusDiff || tab.diff != original || tab.fileSel != 0 || tab.selected != 0 {
				t.Fatal("focus return lost reading context")
			}
			click(t, tt, "关闭详情")
			if !tab.detailHidden || tab.diff != nil || tab.selected != 0 || tt.HasText("并排") {
				t.Fatal("closing details did not release data and space")
			}
		})
	}
}

func TestOperationFailureRecovery(t *testing.T) {
	for _, test := range []struct {
		err, errorContext error
		pending           []string
		want              string
	}{
		{errors.New("Authentication failed"), nil, nil, "认证未通过"},
		{errors.New("Failed to connect: Connection refused"), nil, nil, "无法连接"},
		{errors.New("failed"), context.Canceled, nil, "已取消"},
		{errors.New("failed"), nil, []string{"中止合并"}, "进行中的 Git 操作"},
		{errors.New("unrecognized"), nil, nil, "查看 Git 原始输出"},
	} {
		got := operationFailure(test.err, test.errorContext, test.pending)
		if !strings.Contains(got, test.want) {
			t.Fatalf("%q missing %q", got, test.want)
		}
		if len(test.pending) == 0 && strings.Contains(got, "冲突") {
			t.Fatalf("spurious conflict advice: %s", got)
		}
	}
	raw := "fatal: https://alice:secret@example.invalid/repo and http://token@example.invalid/repo"
	clean := redactGitOutput(raw)
	if strings.Contains(clean, "secret") || strings.Contains(clean, "token@") || strings.Contains(clean, "alice") {
		t.Fatal(clean)
	}
}

func TestActualConflictRecoveryState(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "file", "base\n")
	git("add", ".")
	git("commit", "-m", "base")
	git("checkout", "-b", "other")
	writeFile(t, dir, "file", "other\n")
	git("commit", "-am", "other")
	git("checkout", "main")
	writeFile(t, dir, "file", "main\n")
	git("commit", "-am", "main")
	_, err := runWriteGit(context.Background(), dir, []string{"merge", "other"}, nil)
	if err == nil {
		t.Fatal("expected merge conflict")
	}
	pending := pendingOperations(context.Background(), dir)
	if len(pending) != 1 || pending[0] != "中止合并" {
		t.Fatalf("pending: %v", pending)
	}
	git("merge", "--abort")
	if len(pendingOperations(context.Background(), dir)) != 0 {
		t.Fatal("stale abort action")
	}
}

func TestPartialOperationReceipt(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "file", "base\n")
	git("add", ".")
	git("commit", "-m", "base")
	git("checkout", "-b", "target")
	writeFile(t, dir, "file", "target\n")
	git("commit", "-am", "target")
	git("checkout", "main")
	writeFile(t, dir, "file", "local change\n")
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	op := &operation{Repo: dir, Kind: "新建分支", Name: "created", Target: "target", Checkout: true}
	a.runOperation(op)
	awaitUpdates(t, updates, func() bool { return !op.Busy })
	if op.Error == "" || !strings.Contains(op.Output, "步骤 1/2 · 已完成") || !strings.Contains(op.Output, "步骤 2/2 · 未完成") {
		t.Fatalf("receipt: %s %s", op.Error, op.Output)
	}
	git("rev-parse", "--verify", "created")
	if strings.TrimSpace(string(git("branch", "--show-current"))) != "main" {
		t.Fatal("checkout unexpectedly succeeded")
	}
}

func TestLoadingThreshold(t *testing.T) {
	started := time.Now()
	tt := ui.NewTester(func(c *ui.Context) { loadingMessage(c, started, "正在读取文件…") }, 400, 200)
	if tt.HasText("读取中") {
		t.Fatal("fast path flashed")
	}
	tt.Advance(200 * time.Millisecond)
	if !tt.HasText("读取中") {
		t.Fatal("slow path has no feedback")
	}
	tt.Advance(time.Second)
	if !tt.HasText("正在读取文件") {
		t.Fatal("long loading lacks context")
	}
}

func TestTabRestoresSelectedFileAndReadingPosition(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "a.txt", strings.Repeat("first\n", 150))
	writeFile(t, dir, "b.txt", strings.Repeat("second\n", 150))
	git("add", ".")
	git("commit", "-m", "two files")
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	tab.selected, tab.commitApplied = 0, 0
	a.onCommitSelected(tab)
	awaitUpdates(t, updates, func() bool { return !tab.detailLoading })
	tab.fileSel, tab.fileApplied = 1, 1
	a.onFileSelected(tab)
	awaitUpdates(t, updates, func() bool { return !tab.diffLoading })
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Scroll(1000, 650, 0, 600)
	tt.Frame()
	want := tab.diff.ReadingPosition()
	tab.focusDiff = true
	tab.detailSplit = 42
	other := newTab(2, "/tmp/other")
	other.loaded = true
	a.tabs = append(a.tabs, other)
	a.activate(1)
	tt.Frame()
	if tab.diff != nil || tab.files != nil {
		t.Fatal("background retained heavy data")
	}
	a.activate(0)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	tt.Frame()
	awaitUpdates(t, updates, func() bool { return !tab.detailLoading })
	tt.Frame()
	awaitUpdates(t, updates, func() bool { return !tab.diffLoading })
	tt.Frame()
	if tab.fileSel != 1 || !tab.focusDiff || tab.detailSplit != 42 || tab.diff == nil {
		t.Fatal("lost reading context")
	}
	// Compare the opaque position by formatting its scalar/range contents.
	if fmt.Sprint(tab.diff.ReadingPosition()) != fmt.Sprint(want) {
		t.Fatalf("reading position changed: %v -> %v", want, tab.diff.ReadingPosition())
	}
}

func TestSearchReturnsToOriginalSelection(t *testing.T) {
	dir, _ := initTestRepo(t)
	importTestHistory(t, dir, 20)
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	tab.selected, tab.commitApplied = 10, 10
	want := tab.selectedCommit().hash
	a.searchHistory(tab, "commit 19")
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	if len(tab.commits) != 1 {
		t.Fatal("search fixture")
	}
	a.searchHistory(tab, "")
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	if tab.selectedCommit() == nil || tab.selectedCommit().hash != want || tab.scope != "全部分支" {
		t.Fatal("search return lost original history")
	}
}

func TestNarrowWindowKeepsTheNextStepVisible(t *testing.T) {
	for _, width := range []int{860, 1200} {
		a, _ := readingFixture()
		tt := ui.NewTester(a.view, width, 560)
		want := []string{"定位提交…", "搜索", "比较…", "仓库操作 ▾", "file.go", "放大 diff"}
		if width >= 1050 {
			want = append(want, "获取…", "拉取…", "推送…")
		}
		var boxes []ui.Rect
		for _, label := range want {
			r, ok := tt.Find(label)
			if !ok {
				t.Fatalf("%d missing %s in %q", width, label, tt.Texts())
			}
			if r.X < -1 || r.Y < -1 || r.X+r.W > float32(width)+1 || r.Y+r.H > 561 || r.W < 8 || r.H < 8 {
				t.Fatalf("%d clipped %s %+v", width, label, r)
			}
			boxes = append(boxes, r)
		}
		for i := range boxes {
			for j := i + 1; j < len(boxes); j++ {
				if rectsOverlap(boxes[i], boxes[j]) {
					t.Fatalf("%d overlap %s and %s", width, want[i], want[j])
				}
			}
		}
		if width < 1050 && (tt.HasText("拉取…") || tt.HasText("获取…")) {
			t.Fatal("narrow window repeated sync actions outside the menu")
		}
	}
	a, _ := readingFixture()
	narrow := ui.NewTester(a.view, 860, 560)
	click(t, narrow, "仓库操作 ▾")
	menu := strings.Join(narrow.Menu(), "\n")
	for _, label := range []string{"获取…", "拉取…", "推送…", "在终端打开仓库"} {
		if !strings.Contains(menu, label) {
			t.Fatalf("narrow menu missing %s: %s", label, menu)
		}
	}
}

func TestFetchDialogUsesEverydayLanguage(t *testing.T) {
	a, _ := readingFixture()
	tt := ui.NewTester(a.view, 1200, 700)
	click(t, tt, "获取…")
	if a.op == nil || a.op.Kind != "Fetch" {
		t.Fatal("fetch dialog did not keep its command kind")
	}
	tt.Frame()
	if tt.HasText("Fetch") || !tt.HasText("获取") {
		t.Fatalf("dialog language: %q", tt.Texts())
	}
}

func TestFailureSummaryLeadsTheDialog(t *testing.T) {
	tab := newTab(1, t.TempDir())
	a, _ := newTestApp(t, tab)
	a.showOperation = true
	a.op = &operation{
		Kind: "拉取", Repo: tab.path,
		Error:    "无法连接远程仓库。请检查网络、远程地址和访问权限，再重新发起操作。",
		Recovery: []string{"中止合并"},
	}
	tt := ui.NewTester(a.view, 860, 560)
	errBox, ok := tt.Find(a.op.Error)
	if !ok {
		t.Fatalf("missing failure summary: %q", tt.Texts())
	}
	action, ok := tt.Find("重新执行")
	if !ok {
		t.Fatal(tt.Texts())
	}
	if errBox.Y > action.Y || tt.HasText("执行前请核对") || tt.HasText("解决冲突") {
		t.Fatal("failure summary is not the next step")
	}
	if _, ok := tt.Find("中止合并…"); !ok {
		t.Fatal("missing the actual recovery action")
	}
}

func TestDismissRunningOperationDoesNotCancel(t *testing.T) {
	tab := newTab(1, t.TempDir())
	a, _ := newTestApp(t, tab)
	cancelled := false
	a.op = &operation{Kind: "拉取", Repo: tab.path, Busy: true, Cancel: func() { cancelled = true }}
	a.showOperation = true
	tt := ui.NewTester(a.view, 860, 560)
	tt.Key(0, ui.KeyEscape)
	if a.showOperation || cancelled || !strings.Contains(a.flash, "没有取消") {
		t.Fatalf("open=%v cancelled=%v flash=%q", a.showOperation, cancelled, a.flash)
	}
}

func rectsOverlap(a, b ui.Rect) bool {
	return a.X < b.X+b.W-1 && b.X < a.X+a.W-1 && a.Y < b.Y+b.H-1 && b.Y < a.Y+a.H-1
}
