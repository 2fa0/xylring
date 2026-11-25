package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestUnstageRenameRestoresBothIndexPaths(t *testing.T) {
	dir, git := initTestRepo(t)
	git("config", "status.renames", "true")
	const oldPath, newPath = "old[1].txt", "new[1].txt"
	const base, edited = "original\n", "original\nworktree edit\n"
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(oldPath, base)
	git("add", "--", oldPath)
	git("commit", "-m", "base")
	git("mv", "--", oldPath, newPath)
	write(newPath, edited)
	write("other.txt", "unrelated\n")
	git("add", "--", "other.txt")

	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	tab := newTab(1, dir)
	tab.compareFiles = expandWorktreeRows(files)
	idx := slices.IndexFunc(tab.compareFiles, func(f fileChange) bool {
		return f.path == newPath && f.side == sideStaged && f.status == "R"
	})
	if idx < 0 {
		t.Fatalf("missing staged rename: %+v", tab.compareFiles)
	}
	rename := tab.compareFiles[idx]
	updates := make(chan func(), 64)
	a := &app{dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	tt := ui.NewTester(func(c *ui.Context) { a.worktreeStageCheckbox(c, tab, rename, "rename") }, 600, 500)
	tt.Frame()
	if err := tt.Click(tr("取消暂存")); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return !tab.commitBusy })
	if a.banner != "" {
		t.Fatal(a.banner)
	}
	if got := git("diff", "--cached", "--name-only", "-z"); string(got) != "other.txt\x00" {
		t.Fatalf("unexpected staged paths: %q", got)
	}
	if got := git("show", ":"+oldPath); string(got) != base {
		t.Fatalf("original contents were not restored to the index: %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(dir, newPath)); err != nil || string(got) != edited {
		t.Fatalf("renamed worktree file changed: %q (%v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, oldPath)); !os.IsNotExist(err) {
		t.Fatalf("original worktree path was recreated or could not be checked: %v", err)
	}
}

func TestParsePorcelainZ(t *testing.T) {
	files := parsePorcelainZ([]byte(" M file.txt\x00?? new.txt\x00R  renamed.txt\x00old.txt\x00"))
	if len(files) != 3 {
		t.Fatalf("files %+v", files)
	}
	if files[0].path != "file.txt" || files[0].extra != "未暂存" || files[0].status != "M" {
		t.Fatalf("modified %+v", files[0])
	}
	if files[1].path != "new.txt" || files[1].extra != "未跟踪" || files[1].status != "A" {
		t.Fatalf("untracked %+v", files[1])
	}
	if files[2].oldPath != "old.txt" || files[2].path != "renamed.txt" || files[2].extra != "重命名" {
		t.Fatalf("rename %+v", files[2])
	}
}

func TestWorktreeBadges(t *testing.T) {
	got := worktreeBadges([]fileChange{
		{extra: "未暂存"},
		{extra: "未跟踪"},
		{extra: "已暂存"},
		{extra: "已暂存并继续改"},
		{extra: "冲突"},
	})
	want := []string{"冲突 1", "已暂存 2", "未暂存 2", "未跟踪 1"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWorktreeRowOffsetsCommitIndex(t *testing.T) {
	tab := newTab(1, "/tmp/worktree-offset")
	tab.commits = []commit{{hash: hashN(1), subject: "head"}}
	tab.head = tab.commits[0].hash
	if tab.rows() != 1 || tab.rowData(0).hash != hashN(1) {
		t.Fatal("干净时不应插入工作区行")
	}
	tab.worktreeCount = 1
	tab.worktreeFiles = []fileChange{{path: "a.txt", extra: "未暂存"}}
	if tab.rows() != 2 || tab.rowData(0).hash != worktreeHash || tab.rowData(1).hash != hashN(1) {
		t.Fatalf("rows=%d first=%x", tab.rows(), tab.rowData(0).hash)
	}
	if tab.rowIndex(tab.head) != 1 || tab.rowIndex(worktreeHash) != 0 {
		t.Fatalf("rowIndex head=%d worktree=%d", tab.rowIndex(tab.head), tab.rowIndex(worktreeHash))
	}
	tab.query.term = "x"
	if tab.hasWorktreeRow() || tab.rows() != 1 {
		t.Fatal("搜索时仍显示工作区行")
	}
}

func TestWorktreeShowsUncommittedAndKeepsGraph(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o600)
	tab := newTab(1, dir)
	updates := make(chan func(), 64)
	a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
	defer tab.cancel()
	a.reload(tab)
	waitUpdates(t, updates, func() bool { return !tab.loading && tab.worktreeCount > 0 })
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"未提交改动", "未暂存 1", "未跟踪 1", "图谱", "base"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tab.rows() != 2 || tab.rowData(0).hash != worktreeHash {
		t.Fatalf("工作区不是图谱首行 rows=%d", tab.rows())
	}
	if err := tt.Click("未提交改动"); err != nil {
		t.Fatal(err)
	}
	waitUpdates(t, updates, func() bool { return tab.worktree && !tab.compareLoading && !tab.diffLoading })
	tt.Frame()
	for _, text := range []string{"未提交改动", "提交改动", "a.txt", "b.txt", "+1", "-1", "-0", "图谱", "base", "并排", "内联"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tab.worktree || tt.HasText("并排") {
		t.Fatalf("Esc 没有离开工作区：%q", tt.Texts())
	}
	if !tt.HasText("未提交改动") {
		t.Fatalf("离开后工作区行消失了：%q", tt.Texts())
	}
	if err := tt.Click("base"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.worktree || tab.inspecting == zeroHash {
		t.Fatalf("点提交没有打开详情 worktree=%t inspecting=%x", tab.worktree, tab.inspecting)
	}
}

func TestWorktreeRowConnectsToHead(t *testing.T) {
	tab := newTab(1, "/tmp/worktree-graph")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2)}, subject: "head", author: "A", when: 1700000000},
		{hash: hashN(2), subject: "base", author: "A", when: 1700000000},
	}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.head = tab.commits[0].hash
	tab.worktreeCount = 1
	tab.worktreeFiles = []fileChange{{path: "a.txt", extra: "未暂存", status: "M"}}
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 900, 500)
	tt.Frame()
	header, ok := tt.Find("图谱")
	if !ok {
		t.Fatal("图谱列没有显示")
	}
	row, ok := tt.Find("未提交改动")
	if !ok {
		t.Fatal("未提交改动没有显示")
	}
	next, ok := tt.Find("head")
	if !ok {
		t.Fatal("HEAD 没有显示")
	}
	tab.syncWorktreeCommit()
	img := tt.Image()
	boundary := int((row.Y + row.H/2 + next.Y + next.H/2) / 2)
	x := header.X + laneGap/2 + float32(tab.worktreeRow.nodeCol)*laneGap
	ink := false
	for y := boundary - 1; y <= boundary; y++ {
		for px := int(x) - 2; px <= int(x)+2; px++ {
			r, g, b, _ := img.At(px, y).RGBA()
			if max(r, g, b)-min(r, g, b) > 0x1800 {
				ink = true
			}
		}
	}
	if !ink {
		t.Fatalf("工作区节点没有接到 HEAD，boundary=%d x=%.1f", boundary, x)
	}
}

func TestWorktreeFingerprintCountsLinesAcrossChunks(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct {
		content string
		lines   int
	}{
		{"", 0}, {"a\n", 1}, {"ab\ncd", 2}, {"ab\ncd\n", 2},
	} {
		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(test.content), 0600); err != nil {
			t.Fatal(err)
		}
		stamp, note, lines, err := fingerprintWorktreeFile(dir, "file.txt", make([]byte, 2), true)
		if err != nil || note != "" || lines != test.lines || stamp == "" {
			t.Fatalf("%q: stamp=%q note=%q lines=%d err=%v", test.content, stamp, note, lines, err)
		}
	}
}

func TestWorktreeStatusLineStats(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2}, 0o600)
	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]fileChange{}
	for _, f := range files {
		got[f.path] = f
	}
	if st := got["a.txt"].unstagedStat; !st.has || st.added != 1 || st.deleted != 1 {
		t.Fatalf("modified %+v", got["a.txt"])
	}
	if st := got["b.txt"].unstagedStat; !st.has || st.binary || st.added != 1 || st.deleted != 0 {
		t.Fatalf("untracked %+v", got["b.txt"])
	}
	if st := got["bin.dat"].unstagedStat; !st.has || !st.binary {
		t.Fatalf("binary %+v", got["bin.dat"])
	}
}

func TestWorktreeStatusCachedWithoutHead(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o600)
	git("add", "a.txt")
	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].path != "a.txt" || !files[0].stagedStat.has || files[0].stagedStat.added != 1 || files[0].stagedStat.deleted != 0 {
		t.Fatalf("staged without HEAD %+v", files)
	}
}

func TestWorktreeRenameUsesGitPathOrder(t *testing.T) {
	dir, git := initTestRepo(t)
	oldPath, newPath := "旧名 file.txt", "新名 file.txt"
	os.WriteFile(filepath.Join(dir, oldPath), []byte("one\ntwo\nthree\nfour\nfive\n"), 0o600)
	git("add", ".")
	git("commit", "-m", "base")
	head := mustResolve(t, dir, "HEAD")
	git("mv", oldPath, newPath)
	os.WriteFile(filepath.Join(dir, newPath), []byte("one\ntwo\nthree\nfour\nchanged\n"), 0o600)
	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].path != newPath || files[0].oldPath != oldPath || files[0].status != "R" {
		t.Fatalf("rename paths: %+v", files)
	}
	if !files[0].stagedStat.has || !files[0].unstagedStat.has || files[0].unstagedStat.added != 1 || files[0].unstagedStat.deleted != 1 {
		t.Fatalf("rename stats: %+v", files[0])
	}
	source, err := readFileDiffSource(context.Background(), dir, hashHex(head), "", files[0], true)
	if err != nil || source == nil || string(source.old) != "one\ntwo\nthree\nfour\nfive\n" || string(source.new) != "one\ntwo\nthree\nfour\nchanged\n" {
		t.Fatalf("rename diff: source=%+v err=%v", source, err)
	}
}

func TestWorktreeDiffTakesFullBottomWidthWithoutInspectFilesPane(t *testing.T) {
	tab := newTab(1, "/tmp/test-wt-width")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "feat: something", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.worktreeCount = 2
	tab.worktreeFiles = []fileChange{
		{status: "M", path: "main.go", extra: "未暂存", hasStat: true, added: 10, deleted: 2},
		{status: "A", path: "new.go", extra: "未跟踪", hasStat: true, added: 20, deleted: 0},
	}
	tab.compareFiles = expandWorktreeRows(tab.worktreeFiles)
	tab.fileSel = 0
	tab.worktree = true
	tab.selected = 0
	tab.selection.Add(worktreeHash)
	tab.inspecting = zeroHash
	tab.diffNote = "测试差异"

	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	if !tt.HasText("全部暂存") || !tt.HasText("提交改动") {
		t.Fatalf("未提交场景应展示文件列表和提交框：%q", tt.Texts())
	}
	if !tt.HasText("并排") || !tt.HasText("内联") || !tt.HasText("测试差异") {
		t.Fatalf("底栏应展示 diff 视图：%q", tt.Texts())
	}

	// Now switch to normal commit inspection: Mark 1 MUST be present
	tab.worktree = false
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "committed.go", hasStat: true, added: 5, deleted: 1},
	}
	tab.fileSel = 0
	tt.Frame()

	if !tt.HasText("committed.go") {
		t.Fatalf("常规提交详情应正常显示中间文件列表：%q", tt.Texts())
	}
	if !tt.HasText("feat: something") {
		t.Fatalf("常规提交详情应正常显示提交信息面板：%q", tt.Texts())
	}
}

func TestWorktreeCommitCardExpandOnHoverAndPin(t *testing.T) {
	tab := newTab(1, "/tmp/test-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	tab.head = hashN(1)
	tab.graph.add(&tab.commits[0])
	tab.selected = 0
	tab.selection.Add(worktreeHash)
	tab.inspecting = zeroHash
	tab.worktree = true
	tab.worktreeFiles = []fileChange{
		{status: "M", path: "main.go"},
	}
	tab.compareFiles = []fileChange{
		{status: "M", path: "main.go"},
	}
	tab.fileSel = 0
	tab.diffNote = "测试差异"

	a := newApp()
	a.tabs = []*repoTab{tab}
	a.dispatch = func(func()) {}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// 1. Inactive state by default: only single line (header) is shown
	if !tt.HasText("提交改动") {
		t.Fatalf("单行状态应展示标题和状态：%q", tt.Texts())
	}
	if tt.HasText("修改上次提交") {
		t.Fatalf("非活动状态不应展示扩展内容：%q", tt.Texts())
	}

	// 2. Hover over the card -> expands
	cardRect, ok := tt.Find("提交改动")
	if !ok {
		t.Fatal("未找到提交改动卡片")
	}
	tt.Move(cardRect.X+2, cardRect.Y+2)
	tt.Frame()
	if !tt.HasText("修改上次提交") {
		t.Fatalf("鼠标移动到卡片上后应展开面板：%q", tt.Texts())
	}

	// 3. Move mouse away while no text -> collapses
	tt.Move(10, 10)
	tt.Frame()
	if tt.HasText("修改上次提交") {
		t.Fatalf("鼠标移开且无输入时应自动折叠：%q", tt.Texts())
	}

	// 4. If commitMsg is drafted, it stays expanded even with mouse away
	tab.commitMsg = "feat: some changes"
	tt.Frame()
	if !tt.HasText("修改上次提交") {
		t.Fatalf("有输入说明时即使鼠标移开也应保持展开：%q", tt.Texts())
	}

	// 5. Clear commitMsg -> collapses again
	tab.commitMsg = ""
	tt.Frame()
	if tt.HasText("修改上次提交") {
		t.Fatalf("清空提交说明后应自动折叠：%q", tt.Texts())
	}

	// 6. Pin via clicking header -> stays expanded
	if err := tt.Click("提交改动"); err != nil {
		t.Fatalf("点击标题栏失败: %v", err)
	}
	tt.Move(10, 10)
	tt.Frame()
	if !tab.commitPinned || !tt.HasText("修改上次提交") {
		t.Fatalf("点击固定后即使鼠标移开也应保持展开：%q", tt.Texts())
	}
}

func TestWorktreeRemovalCheckChangedDetectsNewDirtyPath(t *testing.T) {
	wt := repoWorktree{path: "/tmp/wt", head: hashN(1)}
	prev := worktreeRemoveCheck{
		dirty:        true,
		dirtyEntries: []string{"?? a.txt"},
	}
	curSame := worktreeRemoveCheck{
		dirty:        true,
		dirtyEntries: []string{"?? a.txt"},
	}
	if worktreeRemovalCheckChanged(wt, wt, prev, curSame) {
		t.Fatal("identical dirty entries should not trigger change")
	}
	curAdded := worktreeRemoveCheck{
		dirty:        true,
		dirtyEntries: []string{"?? a.txt", "?? b.txt"},
	}
	if !worktreeRemovalCheckChanged(wt, wt, prev, curAdded) {
		t.Fatal("adding a new untracked file while already dirty must trigger re-confirmation")
	}
}

func TestWorktreeRemovalDetectsInPlaceEditsAndPruneGuardsRecoverableState(t *testing.T) {
	dir, git := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")

	wtDir := filepath.Join(t.TempDir(), "linked-wt")
	git("worktree", "add", "-b", "feat/check", wtDir)

	// Edit tracked file in place after initial inspection.
	if err := os.WriteFile(filepath.Join(wtDir, "base.txt"), []byte("v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt1, check1 := inspectWorktreeRemoval(context.Background(), dir, repoWorktree{path: wtDir})
	if check1.err != "" || !check1.dirty || check1.stateDigest == "" {
		t.Fatalf("unexpected initial check: %+v", check1)
	}
	if err := os.WriteFile(filepath.Join(wtDir, "base.txt"), []byte("v3-edited-again\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt2, check2 := inspectWorktreeRemoval(context.Background(), dir, wt1)
	if !worktreeRemovalCheckChanged(wt1, wt2, check1, check2) {
		t.Fatal("in-place edit to the same dirty file must trigger re-confirmation")
	}

	// Stage a change, then remove the worktree directory to simulate an offline drive / missing dir.
	if err := os.WriteFile(filepath.Join(wtDir, "staged.txt"), []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("-C", wtDir, "add", "staged.txt")
	if err := os.RemoveAll(wtDir); err != nil {
		t.Fatal(err)
	}

	prunableWt, prunableCheck := inspectWorktreeRemoval(context.Background(), dir, repoWorktree{path: wtDir})
	if prunableWt.prunable == "" || prunableCheck.err != "" || !prunableCheck.prunableStaged {
		t.Fatalf("expected prunable staged state detected, got wt=%+v check=%+v", prunableWt, prunableCheck)
	}
	if err := pruneWorktreeRecord(context.Background(), dir, wtDir, false); err == nil {
		t.Fatal("expected non-forced prune to stop when staged changes exist in index")
	}
	if err := pruneWorktreeRecord(context.Background(), dir, wtDir, true); err != nil {
		t.Fatalf("expected forced prune to succeed: %v", err)
	}
}

func TestWorktreeRemovalDetectsReflogOnlyCommitsAndDraftAndPrunableChanges(t *testing.T) {
	dir, git := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")

	wtDir := filepath.Join(t.TempDir(), "wt-reflog")
	git("worktree", "add", "-b", "feat/reflog", wtDir)

	// Create a commit in the linked worktree, then reset back to base so HEAD is merged
	// while the abandoned commit still lives only in the worktree's HEAD reflog.
	if err := os.WriteFile(filepath.Join(wtDir, "only-in-reflog.txt"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("-C", wtDir, "add", ".")
	git("-C", wtDir, "commit", "-m", "abandoned work")
	git("-C", wtDir, "reset", "--hard", "HEAD~1")

	wt, check := inspectWorktreeRemoval(context.Background(), dir, repoWorktree{path: wtDir})
	if check.err != "" {
		t.Fatalf("unexpected check error: %s", check.err)
	}
	if check.unmerged {
		t.Fatal("expected HEAD to be merged after reset")
	}
	if !check.reflogUnreachable {
		t.Fatal("expected reflog-only unreachable commit to be flagged on normal worktree removal")
	}

	// In-app commit draft must be surfaced and require force confirmation.
	updates := make(chan func(), 16)
	a := &app{
		dispatch: func(fn func()) { updates <- fn },
		tabs:     []*repoTab{{path: wt.path, commitMsg: "wip commit message"}},
	}
	a.openWorktreeRemoveModal(dir, wt)
	waitUpdates(t, updates, func() bool { return !a.wtRemove.check.checking })
	if !a.wtRemove.check.hasDraft {
		t.Fatal("expected in-app commit draft to be detected")
	}
	tt := ui.NewTester(a.worktreeRemoveModal, 700, 500)
	defer retireTester(tt)
	if !tt.HasText("应用内仍有未提交的提交说明草稿，移除后将一并清除。") {
		t.Fatalf("expected commit draft warning in modal: %q", tt.Texts())
	}

	// Prunable worktree: force-prune must re-check index/reflog and abort if stateDigest changed.
	if err := os.RemoveAll(wtDir); err != nil {
		t.Fatal(err)
	}
	prunableWt, prunableCheck := inspectWorktreeRemoval(context.Background(), dir, repoWorktree{path: wtDir})
	if prunableWt.prunable == "" || prunableCheck.err != "" || prunableCheck.stateDigest == "" {
		t.Fatalf("unexpected prunable check: wt=%+v check=%+v", prunableWt, prunableCheck)
	}
	// Mutate the worktree's reflog after inspection to simulate a change before force-prune executes.
	commonPath, match, err := resolveWorktreeAdminDir(context.Background(), dir, wtDir)
	if err != nil {
		t.Fatal(err)
	}
	reflogPath := filepath.Join(commonPath, "worktrees", match, "logs", "HEAD")
	origReflog, err := os.ReadFile(reflogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reflogPath, append(origReflog, []byte("\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	prunableWt2, prunableCheck2 := inspectWorktreeRemoval(context.Background(), dir, prunableWt)
	if !worktreeRemovalCheckChanged(prunableWt, prunableWt2, prunableCheck, prunableCheck2) {
		t.Fatal("expected prunable reflog change to trigger re-confirmation via stateDigest")
	}
}
