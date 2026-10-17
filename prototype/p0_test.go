package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func writeFile(t *testing.T, dir, path, text string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func executeOp(t *testing.T, dir string, o operation) {
	t.Helper()
	cmds, err := operationCommands(&o)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range cmds {
		if _, err := runWriteGit(context.Background(), dir, cmd, nil); err != nil {
			t.Fatalf("%v: %v", cmd, err)
		}
	}
}
func TestFullHistorySearchAndRenameFollow(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "old.txt", "one\n")
	git("add", ".")
	git("commit", "-m", "initial", "-m", "needle only in body")
	git("mv", "old.txt", "new.txt")
	git("commit", "-m", "rename")
	writeFile(t, dir, "new.txt", "two\n")
	git("commit", "-am", "edit")
	git("tag", "searchable-tag", "HEAD~2")
	meta, err := loadMeta(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	read := func(skip, n int, o logOptions) []commit {
		t.Helper()
		var out []commit
		if err := streamLog(context.Background(), dir, skip, n, o, func(c commit) error { out = append(out, c); return nil }); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if c := read(0, 10, logOptions{Query: "needle", Names: meta.refs}); len(c) != 1 || c[0].subject != "initial" {
		t.Fatalf("body search %v", c)
	}
	if c := read(0, 10, logOptions{Query: "searchable-tag", Names: meta.refs}); len(c) != 1 || c[0].subject != "initial" {
		t.Fatalf("tag search %v", c)
	}
	c := read(0, 10, logOptions{Path: "new.txt", Refs: []string{"HEAD"}})
	if len(c) != 3 || c[0].historyPath != "new.txt" || c[2].historyPath != "old.txt" {
		t.Fatalf("rename paths %+v", c)
	}
	c = read(2, 1, logOptions{Path: "new.txt", Refs: []string{"HEAD"}})
	if len(c) != 1 || c[0].historyPath != "old.txt" {
		t.Fatalf("paged rename %+v", c)
	}
	dir, _ = initTestRepo(t)
	importTestHistory(t, dir, 650)
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	tab.query = "commit 0"
	a.searchNext(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded && !tab.loading && !tab.detailLoading })
	if tab.err != "" || len(tab.commits) != 1 || tab.selectedCommit().subject != "commit 0" {
		t.Fatalf("full history search %v %s", tab.commits, tab.err)
	}
}
func TestStashIncludesIndexAndUntracked(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "tracked", "base\n")
	git("add", ".")
	git("commit", "-m", "base")
	writeFile(t, dir, "tracked", "staged\n")
	git("add", "tracked")
	writeFile(t, dir, "untracked", "new\n")
	executeOp(t, dir, operation{Kind: "新建 Stash", Message: "save", IncludeUntracked: true})
	s, err := loadStashes(context.Background(), dir)
	if err != nil || len(s) != 1 {
		t.Fatalf("stashes %v %v", s, err)
	}
	files, err := stashFiles(context.Background(), dir, hashHex(s[0].hash))
	if err != nil || len(files) != 3 {
		t.Fatalf("stash files %v %v", files, err)
	}
	executeOp(t, dir, operation{Kind: "Apply Stash", Target: s[0].stash, RestoreIndex: true})
	if out := string(git("diff", "--cached", "--name-only")); !strings.Contains(out, "tracked") {
		t.Fatalf("index was not restored: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "untracked")); err != nil {
		t.Fatal(err)
	}
	executeOp(t, dir, operation{Kind: "Drop Stash", Target: s[0].stash})
	s, err = loadStashes(context.Background(), dir)
	if err != nil || len(s) != 0 {
		t.Fatal(s, err)
	}
}
func TestWriteOperationsLocalAndRemotes(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "a", "base\n")
	git("add", ".")
	git("commit", "-m", "base")
	base := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	executeOp(t, dir, operation{Kind: "新建分支", Target: base, Name: "feature", Checkout: true})
	writeFile(t, dir, "b", "feature\n")
	git("add", ".")
	git("commit", "-m", "feature")
	feature := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	executeOp(t, dir, operation{Kind: "切换分支", Target: "main"})
	executeOp(t, dir, operation{Kind: "合并", Target: "feature", NoFF: true})
	if parents := strings.Fields(string(git("rev-list", "--parents", "-n", "1", "HEAD"))); len(parents) != 3 {
		t.Fatalf("no-ff: %v", parents)
	}
	executeOp(t, dir, operation{Kind: "添加标签", Target: "HEAD", Name: "v1", Annotated: true, Message: "release"})
	if typ := strings.TrimSpace(string(git("cat-file", "-t", "refs/tags/v1"))); typ != "tag" {
		t.Fatal(typ)
	}
	remotes := []string{"one", "two"}
	for _, r := range remotes {
		remote := filepath.Join(t.TempDir(), "remote.git")
		if _, err := runGit(context.Background(), dir, "init", "--bare", remote); err != nil {
			t.Fatal(err)
		}
		git("remote", "add", r, remote)
		if _, err := runGit(context.Background(), remote, "config", "receive.denyDeleteCurrent", "ignore"); err != nil {
			t.Fatal(err)
		}
	}
	selected := map[string]bool{"one": true, "two": true}
	executeOp(t, dir, operation{Kind: "推送", Name: "main", Remotes: remotes, Selected: selected, Upstream: true})
	executeOp(t, dir, operation{Kind: "推送标签", Target: "v1", Remotes: remotes, Selected: selected})
	for _, r := range remotes {
		if out := string(git("ls-remote", r, "refs/tags/v1")); out == "" {
			t.Fatal("tag not pushed", r)
		}
	}
	executeOp(t, dir, operation{Kind: "Fetch", Prune: true})
	executeOp(t, dir, operation{Kind: "删除标签", Target: "v1", RemoteDelete: true, Remotes: remotes, Selected: selected})
	executeOp(t, dir, operation{Kind: "删除远程分支", Target: "two/main"})
	if out := string(git("ls-remote", "two", "refs/heads/main")); out != "" {
		t.Fatal("remote branch survived")
	}
	executeOp(t, dir, operation{Kind: "Reset", Target: base, Mode: "hard"})
	executeOp(t, dir, operation{Kind: "Cherry-pick", Target: feature, RecordOrigin: true})
	if msg := string(git("log", "-1", "--format=%B")); !strings.Contains(msg, "cherry picked from commit") {
		t.Fatal(msg)
	}
	executeOp(t, dir, operation{Kind: "Revert", Target: "HEAD"})
	if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
		t.Fatal("revert retained b")
	}
	executeOp(t, dir, operation{Kind: "删除本地分支", Target: "feature", Overwrite: true})
}
func TestWriteCancelAndBoundedOutput(t *testing.T) {
	dir, git := initTestRepo(t)
	git("config", "alias.wait", "!sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runWriteGit(ctx, dir, []string{"wait"}, nil)
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancel %v %v", time.Since(start), err)
	}
	b := &boundedOutput{}
	text := strings.Repeat("x", 100000)
	b.Write([]byte(text))
	if b.Len() != 64<<10 {
		t.Fatal(b.Len())
	}
}
func TestHardResetNeedsSecondConfirmation(t *testing.T) {
	tab := newTab(1, t.TempDir())
	a, _ := newTestApp(t, tab)
	o := &operation{Kind: "Reset", Repo: tab.path, Target: "HEAD", Mode: "hard"}
	a.runOperation(o)
	if !o.ConfirmHard || o.Busy || a.writeBusy[tab.path] {
		t.Fatal("hard reset ran before second confirmation")
	}
	a.op = o
	a.showOperation = true
	tt := ui.NewTester(a.view, 1100, 800)
	if !tt.HasText("再次确认") || !tt.HasText("丢弃改动并执行 hard reset") {
		t.Fatal(tt.Texts())
	}
}

func TestP0SessionSettingsAndTabOrder(t *testing.T) {
	root := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return root, nil }
	defer func() { userConfigDir = old }()
	dir, _ := initTestRepo(t)
	importTestHistory(t, dir, 12)
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.prefs = settings{View: defaultViewSettings()}
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	tab.selected = 6
	tab.query = "message"
	want := tab.selectedCommit().hash
	other := newTab(2, t.TempDir())
	other.loaded = true
	a.tabs = append(a.tabs, other)
	a.moveTab(1, 1)
	if a.active != 1 || a.tabs[1] != tab {
		t.Fatal("reorder lost active tab")
	}
	tab.tableLayout = ui.TableLayout{Widths: map[string]float32{"author": 173}}
	a.persist = true
	a.saveSession()
	a.persist = false
	b := &app{prefs: a.prefs, dispatch: func(fn func()) { updates <- fn }}
	b.restoreSession()
	t.Cleanup(func() {
		for _, t := range b.tabs {
			t.releaseHeavy()
		}
	})
	awaitUpdates(t, updates, func() bool { return len(b.tabs) == 2 && b.activeTab().loaded })
	if b.active != 1 || b.activeTab().selectedCommit() == nil || b.activeTab().selectedCommit().hash != want || b.activeTab().query != "message" || b.activeTab().tableLayout.Widths["author"] != 173 {
		t.Fatal("session mismatch")
	}
	a.prefs.View.First = 45
	if a.options(dir).First != 45 {
		t.Fatal("column widths froze global settings")
	}
	v := defaultViewSettings()
	v.First = 7
	a.prefs.Repos = map[string]viewSettings{dir: v}
	if a.options(dir).First != 7 || a.options("").First != 45 {
		t.Fatal("repository settings override failed")
	}
	a.recent = []string{"/z/b", "/a/a"}
	a.prefs.RecentSort = "完整路径"
	if a.sortedRecent()[0] != "/a/a" {
		t.Fatal("sort")
	}
}
func TestP0FileAndStashUI(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "src/nested/file.go", "package sample\nvar value = 1\n")
	git("add", ".")
	git("commit", "-m", "first file")
	writeFile(t, dir, "src/nested/file.go", "package sample\nvar value = 2\n")
	git("commit", "-am", "update file")
	writeFile(t, dir, "src/nested/file.go", "package sample\nvar value = 3\n")
	executeOp(t, dir, operation{Kind: "新建 Stash"})
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	if tab.stashRows() != 1 || tab.rowData(0).stash != "stash@{0}" {
		t.Fatal("stash node missing")
	}
	tt := ui.NewTester(a.view, 1200, 860)
	if !tt.HasText("stash@{0}") {
		t.Fatal(tt.Texts())
	}
	tab.selected = 0
	tt.Frame()
	awaitUpdates(t, updates, func() bool { return !tab.detailLoading && len(tab.files) > 0 })
	tt.Frame()
	awaitUpdates(t, updates, func() bool { return !tab.diffLoading })
	if tab.diff == nil {
		t.Fatal("stash diff missing", tab.detailErr)
	}
	a.openFileHistory(tab, "src/nested/file.go")
	awaitUpdates(t, updates, func() bool { return tab.loaded && !tab.detailLoading })
	tt.Frame()
	if len(tab.commits) != 2 || tab.stashRows() != 0 || tab.selectedCommit().subject != "update file" {
		t.Fatal("file history", tab.err)
	}
	a.openFile(tab, hashHex(tab.commits[1].hash), "src/nested/file.go")
	awaitUpdates(t, updates, func() bool { return tab.viewer != nil && !tab.viewer.loading })
	tt.Frame()
	if tab.viewer.state == nil || !tab.viewer.state.ReadOnly || !tt.HasText("关闭文件") {
		t.Fatal("read-only viewer missing", tt.Texts())
	}
	click(t, tt, "关闭文件")
	tt.Frame()
	if tab.viewer != nil {
		t.Fatal("viewer retained")
	}
	tab.sidebarSection = "文件"
	tt.Frame()
	awaitUpdates(t, updates, func() bool { return !tab.dirBusy[""] })
	tt.Frame()
	if !tt.HasText("src") {
		t.Fatal("workspace tree", tt.Texts())
	}
	rows := changedRows([]fileChange{{path: "a/b/c.txt"}, {path: "a/b/d.txt"}}, nil)
	if len(rows) != 3 || rows[0].label != "a/b" || rows[1].depth != 1 {
		t.Fatalf("compact tree %+v", rows)
	}
}
func TestP0SearchDoesNotOpenDetails(t *testing.T) {
	dir, _ := initTestRepo(t)
	importTestHistory(t, dir, 10)
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.prefs.View = defaultViewSettings()
	a.prefs.View.OpenDetails = false
	tab.query = "commit 3"
	a.searchNext(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	tt := ui.NewTester(a.view, 1200, 860)
	if tab.info != nil || !tab.detailHidden {
		t.Fatal("search auto-opened detail")
	}
	click(t, tt, "commit 3")
	awaitUpdates(t, updates, func() bool { return !tab.detailLoading && tab.info != nil })
	if tab.detailHidden {
		t.Fatal("click did not open detail")
	}
}
func TestMergeConflictAbortAndMainline(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "a", "base\n")
	git("add", ".")
	git("commit", "-m", "base")
	git("checkout", "-b", "side")
	writeFile(t, dir, "a", "side\n")
	git("commit", "-am", "side")
	git("checkout", "main")
	writeFile(t, dir, "a", "main\n")
	git("commit", "-am", "main")
	cmds, _ := operationCommands(&operation{Kind: "合并", Target: "side", NoFF: true})
	if _, err := runWriteGit(context.Background(), dir, cmds[0], nil); err == nil {
		t.Fatal("expected merge conflict")
	}
	executeOp(t, dir, operation{Kind: "中止合并"})
	if out := strings.TrimSpace(string(git("status", "--porcelain"))); out != "" {
		t.Fatal("abort retained conflicts", out)
	}
	if _, err := operationCommands(&operation{Kind: "Revert", Target: "HEAD", Mainline: "invalid"}); err == nil {
		t.Fatal("invalid mainline accepted")
	}
}

func TestRemoteSelectionUI(t *testing.T) {
	tab := newTab(1, t.TempDir())
	tab.loaded = true
	a, _ := newTestApp(t, tab)
	a.op = &operation{Kind: "推送", Repo: tab.path, Name: "main", Remotes: []string{"origin", "backup"}, Selected: map[string]bool{"origin": true}}
	a.showOperation = true
	tt := ui.NewTester(a.view, 1200, 860)
	click(t, tt, "backup")
	if !a.op.Selected["backup"] {
		t.Fatal("remote checkbox did not persist")
	}
	click(t, tt, "origin")
	if a.op.Selected["origin"] {
		t.Fatal("remote could not be deselected")
	}
	tt.Frame()
	cmds, err := operationCommands(a.op)
	if err != nil || len(cmds) != 1 || cmds[0][1] != "backup" {
		t.Fatalf("selected remotes %v %v", cmds, err)
	}
}
func TestStashDifferentIndexAndWorktree(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "file", "base\n")
	git("add", ".")
	git("commit", "-m", "base")
	writeFile(t, dir, "file", "index\n")
	git("add", "file")
	writeFile(t, dir, "file", "base\n")
	git("stash", "push")
	stashes, _ := loadStashes(context.Background(), dir)
	files, err := stashFiles(context.Background(), dir, hashHex(stashes[0].hash))
	if err != nil || len(files) != 1 || files[0].layer != "暂存区" {
		t.Fatalf("lost index-only changes %+v %v", files, err)
	}
	old, next, note, err := loadSides(context.Background(), dir, files[0], false, files[0].newRev, files[0].oldRev)
	if err != nil || note != "" || old != "base\n" || next != "index\n" {
		t.Fatal(old, next, note, err)
	}
}

func TestRemoteTrackingAndPull(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "a", "base\n")
	git("add", ".")
	git("commit", "-m", "base")
	remote := filepath.Join(t.TempDir(), "remote.git")
	git("init", "--bare", remote)
	git("remote", "add", "origin", remote)
	git("push", "origin", "main")
	git("branch", "feature")
	git("push", "origin", "feature")
	executeOp(t, dir, operation{Kind: "检出远程分支", Target: "origin/feature", Name: "feature-local"})
	if upstream := strings.TrimSpace(string(git("rev-parse", "--abbrev-ref", "@{upstream}"))); upstream != "origin/feature" {
		t.Fatal(upstream)
	}
	peer := filepath.Join(t.TempDir(), "peer")
	git("clone", remote, peer)
	if _, err := runGit(context.Background(), peer, "config", "user.name", "Peer"); err != nil {
		t.Fatal(err)
	}
	runGit(context.Background(), peer, "config", "user.email", "peer@example.com")
	runGit(context.Background(), peer, "config", "commit.gpgsign", "false")
	runGit(context.Background(), peer, "config", "core.hooksPath", filepath.Join(peer, "no-hooks"))
	writeFile(t, peer, "b", "peer\n")
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "peer"}, {"push", "origin", "HEAD:feature"}} {
		if _, err := runGit(context.Background(), peer, args...); err != nil {
			t.Fatal(err)
		}
	}
	executeOp(t, dir, operation{Kind: "检出远程分支", Target: "origin/feature", Name: "feature-local", Mode: "检出已有分支并拉取"})
	if _, err := os.Stat(filepath.Join(dir, "b")); err != nil {
		t.Fatal("checkout+pull", err)
	}
	executeOp(t, dir, operation{Kind: "切换分支", Target: "main"})
	executeOp(t, dir, operation{Kind: "拉取", Name: "feature", Remotes: []string{"origin"}, Selected: map[string]bool{"origin": true}, Squash: true})
	if _, err := os.Stat(filepath.Join(dir, "b")); err != nil {
		t.Fatal("squash pull", err)
	}
	if message := strings.TrimSpace(string(git("log", "-1", "--format=%s"))); message != "base" {
		t.Fatal("squash pull created commit")
	}
}
func TestResetModesCherryPickNoCommitAndMergeMainline(t *testing.T) {
	dir, git := initTestRepo(t)
	writeFile(t, dir, "a", "base\n")
	git("add", ".")
	git("commit", "-m", "base")
	base := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	writeFile(t, dir, "a", "next\n")
	git("commit", "-am", "next")
	tip := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	executeOp(t, dir, operation{Kind: "Reset", Target: base, Mode: "soft"})
	if out := string(git("diff", "--cached", "--name-only")); !strings.Contains(out, "a") {
		t.Fatal("soft lost index")
	}
	executeOp(t, dir, operation{Kind: "Reset", Target: base, Mode: "mixed"})
	if out := string(git("diff", "--cached", "--name-only")); out != "" {
		t.Fatal("mixed retained index")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a")); string(data) != "next\n" {
		t.Fatal("mixed lost worktree")
	}
	executeOp(t, dir, operation{Kind: "Reset", Target: base, Mode: "hard"})
	executeOp(t, dir, operation{Kind: "Cherry-pick", Target: tip, NoCommit: true})
	if out := strings.TrimSpace(string(git("rev-parse", "HEAD"))); out != base {
		t.Fatal("no-commit created commit")
	}
	executeOp(t, dir, operation{Kind: "Reset", Target: base, Mode: "hard"})
	git("branch", "side", tip)
	executeOp(t, dir, operation{Kind: "合并", Target: "side", NoFF: true})
	merge := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	executeOp(t, dir, operation{Kind: "Revert", Target: merge, Mainline: "1"})
	if data, _ := os.ReadFile(filepath.Join(dir, "a")); string(data) != "base\n" {
		t.Fatal("merge revert wrong mainline")
	}
}

func TestTabDragAndShortcuts(t *testing.T) {
	first, second := newTab(1, "/tmp/one"), newTab(2, "/tmp/two")
	first.loaded = true
	second.loaded = true
	a, _ := newTestApp(t, first)
	a.tabs = append(a.tabs, second)
	tt := ui.NewTester(a.view, 1000, 800)
	from, _ := tt.Find("● tmp/one")
	to, _ := tt.Find("tmp/two")
	tt.Press(from.X+10, from.Y+10)
	tt.Move(from.X+30, from.Y+10)
	tt.Move(to.X+10, to.Y+10)
	tt.Release(to.X+10, to.Y+10)
	tt.Frame()
	if a.tabs[1] != first || a.active != 1 {
		t.Fatal("drag did not reorder tabs")
	}
	tt.Key(ui.Cmd, ui.Key1)
	if a.active != 0 {
		t.Fatal("Cmd+1 did not switch tab")
	}
	tt.Key(ui.Ctrl, ui.KeyTab)
	if a.active != 1 {
		t.Fatal("Ctrl+Tab did not switch tab")
	}
	tt.Key(ui.Cmd, ui.KeyW)
	if len(a.tabs) != 1 || a.tabs[0] != second {
		t.Fatal("Cmd+W closed wrong tab")
	}
}
func TestCreateBranchFromCommitMenu(t *testing.T) {
	dir, git := initTestRepo(t)
	importTestHistory(t, dir, 3)
	tab := newTab(1, dir)
	a, updates := newTestApp(t, tab)
	a.reload(tab)
	awaitUpdates(t, updates, func() bool { return tab.loaded })
	tt := ui.NewTester(a.view, 1200, 860)
	if err := tt.RightClick("commit 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("在此提交新建分支…"); err != nil {
		t.Fatal(err)
	}
	if a.op == nil || a.op.Kind != "新建分支" {
		t.Fatal("new branch dialog did not open")
	}
	a.op.Name = "from-ui"
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	if !a.op.Busy {
		t.Fatalf("Enter did not start operation: error=%s name=%s texts=%q", a.op.Error, a.op.Name, tt.Texts())
	}
	awaitUpdates(t, updates, func() bool { return !a.op.Busy })
	if a.op.Error != "" {
		t.Fatal(a.op.Error)
	}
	if a.showOperation {
		t.Fatal("successful operation did not close")
	}
	awaitUpdates(t, updates, func() bool { return tab.loaded && !tab.loading })
	if ref := strings.TrimSpace(string(git("log", "-1", "--format=%s", "from-ui"))); ref != "commit 1" {
		t.Fatal(ref)
	}
	if branch := strings.TrimSpace(string(git("symbolic-ref", "--short", "HEAD"))); branch != "main" {
		t.Fatal("creation checked out unexpectedly")
	}
}

func TestUnmergedStatusAndSymlinkDiff(t *testing.T) {
	for _, xy := range []string{"DD", "AU", "UD", "UA", "DU", "AA", "UU"} {
		if statusLetter(xy) != "U" {
			t.Fatal("conflict not marked U", xy)
		}
	}
	dir := t.TempDir()
	writeFile(t, dir, "target", "contents\n")
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Skip(err)
	}
	text, note, err := readWorktree(dir, "link")
	if err != nil || note != "" || string(text) != "target" {
		t.Fatal("symlink was followed instead of compared", string(text), note, err)
	}
}

func TestStashGraphConnectsBase(t *testing.T) {
	tab := newTab(1, "/tmp/repo")
	tab.head = hashN(3)
	tab.dirty = 1
	tab.stashes = []commit{{hash: hashN(4), stash: "stash@{0}", parents: [][20]byte{hashN(1), hashN(5)}}}
	tab.layoutStashes()
	if len(tab.stashes[0].segs) == 0 || tab.stashes[0].nodeCol == 0 {
		t.Fatal("stash graph did not preserve HEAD lane")
	}
	for i := byte(3); i > 0; i-- {
		c := commit{hash: hashN(i)}
		if i > 1 {
			c.parents = [][20]byte{hashN(i - 1)}
		}
		tab.graph.add(&c)
		tab.commits = append(tab.commits, c)
	}
	for _, h := range tab.graph.cols {
		if h != zeroHash {
			t.Fatal("stash base did not join history")
		}
	}
}
