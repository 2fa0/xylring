package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// partialFiles 是同一个文件两侧都有改动时的状态：索引侧 M，工作区侧 M。
func partialFiles() []fileChange {
	return []fileChange{
		{status: "M", path: "both.txt", extra: "已暂存并继续改", indexStatus: 'M', workStatus: 'M'},
		{status: "M", path: "staged.txt", extra: "已暂存", indexStatus: 'M', workStatus: ' '},
		{status: "M", path: "worktree.txt", extra: "未暂存", indexStatus: ' ', workStatus: 'M'},
		{status: "A", path: "new.txt", extra: "未跟踪", indexStatus: '?', workStatus: '?'},
		{status: "U", path: "conflict.txt", extra: "冲突", indexStatus: 'U', workStatus: 'U'},
	}
}

func TestExpandWorktreeRowsSplitsSides(t *testing.T) {
	rows := expandWorktreeRows(partialFiles())
	want := []struct {
		path   string
		side   fileSide
		status string
	}{
		{"both.txt", sideStaged, "M"},
		{"staged.txt", sideStaged, "M"},
		{"both.txt", sideUnstaged, "M"},
		{"worktree.txt", sideUnstaged, "M"},
		{"new.txt", sideUnstaged, "A"},
		{"conflict.txt", sideUnstaged, "U"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows=%d want=%d: %+v", len(rows), len(want), rows)
	}
	for i, w := range want {
		got := rows[i]
		if got.path != w.path || got.side != w.side || got.status != w.status {
			t.Fatalf("row %d = %+v, want %+v", i, got, w)
		}
	}
	// 一行只属于一侧：另一侧的状态要清掉，否则行上的暂存按钮会两头都认。
	if rows[0].hasUnstaged() || !rows[0].hasStaged() {
		t.Fatalf("暂存行还有工作区状态: %+v", rows[0])
	}
	if rows[2].hasStaged() || !rows[2].hasUnstaged() {
		t.Fatalf("工作区行还有索引状态: %+v", rows[2])
	}
	if rows[0].rowKey() == rows[2].rowKey() {
		t.Fatal("同一文件两侧的行身份必须不同")
	}
}

func TestFileListRowsInsertSectionHeaders(t *testing.T) {
	files := expandWorktreeRows(partialFiles())
	indices := make([]int, len(files))
	for i := range files {
		indices[i] = i
	}
	rows := fileListRows(files, indices)
	var got []string
	for _, r := range rows {
		if r.file < 0 {
			got = append(got, "["+r.section.key()+"]")
			continue
		}
		got = append(got, files[r.file].side.key()+":"+files[r.file].path)
	}
	want := []string{
		"[staged]", "staged:both.txt", "staged:staged.txt",
		"[unstaged]", "unstaged:both.txt", "unstaged:worktree.txt", "unstaged:new.txt", "unstaged:conflict.txt",
	}
	if len(got) != len(want) {
		t.Fatalf("rows=%q want=%q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows=%q want=%q", got, want)
		}
	}

	// 过滤掉一组之后就不要再留空标题。
	only := fileListRows(files, []int{1})
	if len(only) != 2 || only[0].file != -1 || only[0].section != sideStaged {
		t.Fatalf("过滤后的行不对: %+v", only)
	}

	// 提交之间的对比不分侧，不加标题。
	plain := fileListRows([]fileChange{{path: "a.go"}, {path: "b.go"}}, []int{0, 1})
	if len(plain) != 2 || plain[0].file != 0 {
		t.Fatalf("不分侧的列表不该有标题: %+v", plain)
	}
}

// TestWorktreeSideDiffBases 是这次改造的核心：暂存过的内容只看 HEAD→索引，
// 暂存之后的改动只看索引→工作区，两边不再混在同一份 diff 里。
func TestWorktreeSideDiffBases(t *testing.T) {
	dir, git := initTestRepo(t)
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "Tester")
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("one\n")
	git("add", "a.txt")
	git("commit", "-m", "base")
	head := mustResolve(t, dir, "HEAD")

	write("two\n")
	git("add", "a.txt")
	write("three\n")

	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	rows := expandWorktreeRows(files)
	if len(rows) != 2 {
		t.Fatalf("两侧都有改动应拆成两行: %+v", rows)
	}
	diff := func(f fileChange) (string, string) {
		source, err := readFileDiffSource(context.Background(), dir, hashHex(head), "", f, true)
		if err != nil || source == nil {
			t.Fatalf("read source: %v", err)
		}
		return string(source.old), string(source.new)
	}
	if old, new := diff(rows[0]); old != "one\n" || new != "two\n" {
		t.Fatalf("已暂存行应只看 HEAD→索引: old=%q new=%q", old, new)
	}
	if old, new := diff(rows[1]); old != "two\n" || new != "three\n" {
		t.Fatalf("未暂存行应只看索引→工作区: old=%q new=%q", old, new)
	}
}

func TestWorktreeSideDiffBasesAfterStagedRename(t *testing.T) {
	dir, git := initTestRepo(t)
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "Tester")
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := strings.Repeat("line\n", 20)
	write("old.txt", base)
	git("add", "old.txt")
	git("commit", "-m", "base")
	git("mv", "old.txt", "new.txt")
	staged := base + "staged\n"
	write("new.txt", staged)
	git("add", "new.txt")
	unstaged := staged + "unstaged\n"
	write("new.txt", unstaged)

	ctx := context.Background()
	files, err := worktreeStatus(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	rows := expandWorktreeRows(files)
	if len(rows) != 2 {
		t.Fatalf("expected a staged rename and an unstaged edit: %+v", rows)
	}
	if rows[0].side != sideStaged || rows[0].oldPath != "old.txt" ||
		rows[1].side != sideUnstaged || rows[1].oldPath != "" {
		t.Fatalf("wrong paths for staged rename and unstaged edit: %+v", rows)
	}
	for i, want := range [][2]string{{base, staged}, {staged, unstaged}} {
		source, err := readFileDiffSource(ctx, dir, "HEAD", "", rows[i], true)
		if err != nil {
			t.Fatal(err)
		}
		if string(source.old) != want[0] || string(source.new) != want[1] {
			t.Fatalf("row %d: old=%q new=%q", i, source.old, source.new)
		}
	}
}

// TestWorktreeSideDiffBasesNewAndDeleted 覆盖新增、删除和未跟踪三种边界。
func TestWorktreeSideDiffBasesNewAndDeleted(t *testing.T) {
	dir, git := initTestRepo(t)
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "Tester")
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.txt", "keep\n")
	write("gone.txt", "bye\n")
	git("add", ".")
	git("commit", "-m", "base")
	head := mustResolve(t, dir, "HEAD")

	write("added.txt", "new\n")
	git("add", "added.txt")
	write("untracked.txt", "loose\n")
	git("rm", "-q", "gone.txt")

	source := func(f fileChange) (string, string) {
		s, err := readFileDiffSource(context.Background(), dir, hashHex(head), "", f, true)
		if err != nil || s == nil {
			t.Fatalf("read source: %v", err)
		}
		return string(s.old), string(s.new)
	}
	rows := expandWorktreeRows([]fileChange{
		{status: "A", path: "added.txt", extra: "已暂存", indexStatus: 'A', workStatus: ' '},
		{status: "D", path: "gone.txt", extra: "已暂存", indexStatus: 'D', workStatus: ' '},
		{status: "A", path: "untracked.txt", extra: "未跟踪", indexStatus: '?', workStatus: '?'},
	})
	if old, new := source(rows[0]); old != "" || new != "new\n" {
		t.Fatalf("新增文件: old=%q new=%q", old, new)
	}
	if old, new := source(rows[1]); old != "bye\n" || new != "" {
		t.Fatalf("删除文件: old=%q new=%q", old, new)
	}
	if old, new := source(rows[2]); old != "" || new != "loose\n" {
		t.Fatalf("未跟踪文件: old=%q new=%q", old, new)
	}
}

// TestWorktreeListShowsTwoSections 走一遍界面：两组标题各出现一次，
// 同一文件在两组里各有一行。
func TestWorktreeListShowsTwoSections(t *testing.T) {
	tab := newTab(1, "/tmp/work/sections")
	tab.loaded = true
	tab.branch = "main"
	tab.worktreeFiles = []fileChange{
		{status: "M", path: "both.txt", extra: "已暂存并继续改", indexStatus: 'M', workStatus: 'M', hasStat: true, added: 1, deleted: 1},
		{status: "A", path: "loose.txt", extra: "未跟踪", indexStatus: '?', workStatus: '?'},
	}
	tab.worktreeCount = 2
	tab.commits = []commit{{hash: hashN(1), subject: "init"}}

	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/sections"},
		foundFrom: "/tmp/work",
		dispatch:  func(func()) {},
	}
	activateWorktreeInspect(tab)
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	texts := tt.Texts()
	stagedAt, unstagedAt := -1, -1
	for i, text := range texts {
		switch text {
		case fileSideTitle(sideStaged):
			if stagedAt < 0 {
				stagedAt = i
			}
		case fileSideTitle(sideUnstaged):
			if unstagedAt < 0 {
				unstagedAt = i
			}
		}
	}
	if stagedAt < 0 || unstagedAt < 0 {
		t.Fatalf("两组标题都应出现：%q", texts)
	}
	if stagedAt > unstagedAt {
		t.Fatalf("已暂存应在未暂存之前：%q", texts)
	}
	both := 0
	for _, text := range tt.Texts() {
		if text == "both.txt" {
			both++
		}
	}
	if both != 2 {
		t.Fatalf("两侧都有改动的文件应出现两行，实际 %d：%q", both, tt.Texts())
	}
	// 分组标题右侧就是这一组的批量操作。
	if !tt.HasText("全部暂存") || !tt.HasText("全部取消暂存") {
		t.Fatalf("两组标题应各带自己的批量操作：%q", tt.Texts())
	}
}

func TestWorktreeListArrowKeysSkipSectionHeaders(t *testing.T) {
	dir, _ := initTestRepo(t)
	tab := newTab(1, dir)
	tab.worktree = true
	tab.compareFiles = []fileChange{
		{path: "staged.txt", side: sideStaged, status: "D"},
		{path: "unstaged.txt", side: sideUnstaged, status: "A"},
	}
	tab.fileSel = 0
	a := &app{dispatch: func(func()) {}}
	tt := ui.NewTester(func(c *ui.Context) { a.fileFlatList(c, tab) }, 600, 500)
	tt.Frame()
	if err := tt.Click("staged.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if tab.fileSel != 1 {
		t.Fatalf("Down selected file %d, want 1", tab.fileSel)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if tab.fileSel != 0 {
		t.Fatalf("Up selected file %d, want 0", tab.fileSel)
	}
}

func TestWorktreeNoteDraftFollowsSelectedRow(t *testing.T) {
	dir, _ := initTestRepo(t)
	tab := newTab(1, dir)
	tab.worktree = true
	tab.compareFiles = []fileChange{
		{path: "both.txt", side: sideStaged, status: "D"},
		{path: "both.txt", side: sideUnstaged, status: "A"},
	}
	draft := &diffNote{Path: "both.txt", Section: sideStaged, Line: 1, Text: "draft"}
	tab.noteEdit = draft
	a := &app{dispatch: func(func()) {}}

	tab.fileSel = 0
	a.loadFileDiff(tab, tab.fileSel)
	if tab.noteEdit != draft {
		t.Fatal("reloading the same row cleared the draft")
	}
	tab.fileSel = 1
	a.loadFileDiff(tab, tab.fileSel)
	if tab.noteEdit != nil {
		t.Fatal("switching to the other side left the previous row's editor visible")
	}
}

func TestWorktreeHunkReviewAppliesOnlySelectedBlock(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		side         fileSide
	}{
		{"stage", "审阅并暂存", sideUnstaged},
		{"unstage", "审阅并取消暂存", sideStaged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, git := initTestRepo(t)
			git("config", "diff.context", "3")
			git("config", "diff.interHunkContext", "0")
			const path = "a.txt"
			lines := make([]string, 50)
			for i := range lines {
				lines[i] = "line " + strconv.Itoa(i+1) + "\n"
			}
			base := strings.Join(lines, "")
			write := func(content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(base)
			git("add", path)
			git("commit", "-m", "base")

			first := strings.Replace(base, "line 5\n", "first block\nextra line\n", 1)
			both := strings.Replace(first, "line 25\n", "second block\n", 1)
			withOtherSide := func(content string) string {
				return strings.Replace(content, "line 45\n", "other side\n", 1)
			}
			index := withOtherSide(base)
			worktree := withOtherSide(both)
			wantIndex := strings.Replace(index, "line 25\n", "second block\n", 1)
			if tc.side == sideStaged {
				index, wantIndex = both, first
			}
			write(index)
			git("add", path)
			write(worktree)

			tab := newTab(1, dir)
			updates := make(chan func(), 64)
			a := &app{tabs: []*repoTab{tab}, dispatch: func(fn func()) { updates <- fn }}
			defer func() {
				tab.cancel()
				if a.scanCancel != nil {
					a.scanCancel()
				}
			}()
			a.openOperation(tc.action, "", path)
			waitUpdates(t, updates, func() bool { return !a.op.busy })
			if !a.op.ready || len(a.op.hunks) != 2 || !strings.Contains(a.op.output, "+first block") {
				t.Fatalf("expected first of two blocks, got %d blocks, preview %q, error %q", len(a.op.hunks), a.op.output, a.op.err)
			}

			tt := ui.NewTester(a.operationModal, 900, 700)
			tt.Frame()
			if err := tt.Click(tr("下一块")); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !strings.Contains(a.op.output, "+second block") || strings.Contains(a.op.output, "+first block") {
				t.Fatalf("wrong selected block preview: %s", a.op.output)
			}
			if err := tt.Click(tr("执行")); err != nil {
				t.Fatal(err)
			}
			waitUpdates(t, updates, func() bool { return !a.op.busy })
			if a.showOperation || a.op.err != "" {
				t.Fatalf("selected block was not applied: %q", a.op.err)
			}
			if got := git("show", ":"+path); string(got) != wantIndex {
				t.Fatalf("wrong index content: %q", got)
			}
			onDisk, err := os.ReadFile(filepath.Join(dir, path))
			if err != nil || string(onDisk) != worktree {
				t.Fatalf("worktree content changed: %q (%v)", onDisk, err)
			}
		})
	}
}

// TestConflictDiffFallsBackToWorktree 冲突文件的索引里没有 stage 0，
// 两侧基准都取不到内容，退回 HEAD→工作区，而不是报错。
func TestConflictDiffFallsBackToWorktree(t *testing.T) {
	dir, git := initTestRepo(t)
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "Tester")
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("base\n")
	git("add", "a.txt")
	git("commit", "-m", "base")
	head := mustResolve(t, dir, "HEAD")
	git("checkout", "-q", "-b", "other")
	write("other\n")
	git("commit", "-qam", "other")
	git("checkout", "-q", "-")
	write("main\n")
	git("commit", "-qam", "main")
	merge := exec.Command("git", "merge", "other")
	merge.Dir = dir
	if err := merge.Run(); err == nil {
		t.Fatal("这次合并应该冲突")
	}

	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	rows := expandWorktreeRows(files)
	if len(rows) != 1 || rows[0].extra != "冲突" {
		t.Fatalf("应是一行冲突: %+v", rows)
	}
	source, err := readFileDiffSource(context.Background(), dir, hashHex(head), "", rows[0], true)
	if err != nil || source == nil {
		t.Fatalf("冲突文件不应读失败: %v", err)
	}
	if !strings.Contains(string(source.new), "<<<<<<<") {
		t.Fatalf("工作区应当带着冲突标记: %q", source.new)
	}
}
