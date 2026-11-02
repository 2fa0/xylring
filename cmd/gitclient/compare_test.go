package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestParseCompareRange(t *testing.T) {
	from, to, three, err := parseCompareRange("main", "feature")
	if err != nil || from != "main" || to != "feature" || three {
		t.Fatalf("two fields: %q %q three=%v %v", from, to, three, err)
	}
	from, to, three, err = parseCompareRange("HEAD", "abc..def")
	if err != nil || from != "abc" || to != "def" || three {
		t.Fatalf("two-dot: %q %q three=%v %v", from, to, three, err)
	}
	from, to, three, err = parseCompareRange("HEAD", "abc...def")
	if err != nil || from != "abc" || to != "def" || !three {
		t.Fatalf("three-dot: %q %q three=%v %v", from, to, three, err)
	}
	if _, _, _, err = parseCompareRange("", ""); err == nil {
		t.Fatal("empty accepted")
	}
	if _, _, _, err = parseCompareRange("", "main..."); err == nil {
		t.Fatal("empty three-dot accepted")
	}
}

func TestParseNameStatusZ(t *testing.T) {
	files := parseNameStatusZ([]byte("R050\x00说明.txt\x00文档.txt\x00A\x00sub/a.go\x00"))
	if len(files) != 2 || files[0].status != "R" || files[0].oldPath != "说明.txt" || files[0].path != "文档.txt" {
		t.Fatalf("name-status %+v", files)
	}
	if files[1].status != "A" || files[1].path != "sub/a.go" {
		t.Fatalf("add %+v", files[1])
	}
}

func TestCompareFilesAcrossBranches(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", "a.txt")
	git("commit", "-m", "base")
	git("checkout", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o600)
	git("add", "-A")
	git("commit", "-m", "change")

	ctx := context.Background()
	mainHash, err := resolveCommit(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	featHash, err := resolveCommit(ctx, dir, "feature")
	if err != nil {
		t.Fatal(err)
	}
	files, err := compareFiles(ctx, dir, hashHex(mainHash), hashHex(featHash))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files %+v", files)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.path] = f.status
	}
	if got["a.txt"] != "M" || got["b.txt"] != "A" {
		t.Fatalf("status %+v", files)
	}
	oldb, note, err := readBlob(ctx, dir, hashHex(mainHash), "a.txt")
	if err != nil || note != "" || string(oldb) != "one\n" {
		t.Fatalf("old blob %q note %q err %v", oldb, note, err)
	}
}

func TestCompareThreeDot(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("base\n"), 0o600)
	git("add", "shared.txt")
	git("commit", "-m", "base")
	git("checkout", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feat\n"), 0o600)
	git("add", "feat.txt")
	git("commit", "-m", "feature change")
	git("checkout", "main")
	os.WriteFile(filepath.Join(dir, "main.txt"), []byte("main\n"), 0o600)
	git("add", "main.txt")
	git("commit", "-m", "main change")

	ctx := context.Background()
	mainHash, err := resolveCommit(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	featHash, err := resolveCommit(ctx, dir, "feature")
	if err != nil {
		t.Fatal(err)
	}
	base, err := mergeBase(ctx, dir, mainHash, featHash)
	if err != nil {
		t.Fatal(err)
	}
	two, err := compareFiles(ctx, dir, hashHex(mainHash), hashHex(featHash))
	if err != nil {
		t.Fatal(err)
	}
	three, err := compareFiles(ctx, dir, hashHex(base), hashHex(featHash))
	if err != nil {
		t.Fatal(err)
	}
	twoGot := map[string]string{}
	for _, f := range two {
		twoGot[f.path] = f.status
	}
	threeGot := map[string]string{}
	for _, f := range three {
		threeGot[f.path] = f.status
	}
	if twoGot["feat.txt"] != "A" || twoGot["main.txt"] != "D" {
		t.Fatalf("two-dot %+v", two)
	}
	if threeGot["feat.txt"] != "A" || threeGot["main.txt"] != "" {
		t.Fatalf("three-dot %+v", three)
	}
}

func TestCompareModal(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "画出提交图谱", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("比较"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("开始比较") || !tt.HasText("从分叉点起") {
		t.Fatalf("对话框文本：%q", tt.Texts())
	}
}

func TestComparisonView(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "画出提交图谱", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.comparison = &commitComparison{from: hashN(2), to: hashN(1), fromLabel: "main", toLabel: "feature", threeDot: true}
	tab.compareFiles = []fileChange{
		{status: "M", path: "a.txt"},
		{status: "A", path: "b.txt"},
	}
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "没有载入内容"
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"main", "...", "feature", "修改  a.txt", "新增  b.txt", "返回", "没有载入内容", "画出提交图谱", "图谱"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if err := tt.Click("返回"); err != nil {
		t.Fatal(err)
	}
	if tab.comparison != nil || !tt.HasText("画出提交图谱") || !tt.HasText("图谱") {
		t.Fatalf("返回后图谱丢了：comp=%v texts=%q", tab.comparison != nil, tt.Texts())
	}
}

func TestInspectPaneKeepsGraph(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), parents: [][20]byte{hashN(2)}, author: "Ada", subject: "画出提交图谱", when: 1700000000},
		{hash: hashN(2), author: "Ada", subject: "初始提交", when: 1700000000},
	}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.head = tab.commits[0].hash
	tab.inspecting = tab.commits[0].hash
	tab.detailSplit = 52
	tab.compareFiles = []fileChange{{status: "M", path: "graph.go"}}
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "没有载入内容"
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"画出提交图谱", "图谱", "修改  graph.go", "Ada", "搜索"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)
	if tab.inspecting != zeroHash || tt.HasText("修改  graph.go") {
		t.Fatalf("Esc 后仍停在详情：inspect=%v texts=%q", tab.inspecting != zeroHash, tt.Texts())
	}
}

func TestJumpToHashMissing(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.commits = []commit{{hash: hashN(1), subject: "one"}}
	a := &app{tabs: []*repoTab{tab}}
	a.jumpToHash(tab, hashN(2))
	if tab.locateErr == "" || tab.selected != -1 {
		t.Fatalf("locateErr=%q selected=%d", tab.locateErr, tab.selected)
	}
}

func TestInspectAndLocateCommit(t *testing.T) {
	dir, git := initTestRepo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", "a.txt")
	git("commit", "-m", "初始提交")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o600)
	git("add", "a.txt")
	git("commit", "-m", "画出提交图谱")

	tab := newTab(1, dir)
	ctx := context.Background()
	meta, err := loadMeta(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var commits []commit
	if err := streamCommits(ctx, dir, 0, pageFirst, func(c commit) error {
		commits = append(commits, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updates := make(chan func(), 32)
	a := &app{
		persist:  false,
		tabs:     []*repoTab{tab},
		dispatch: func(fn func()) { updates <- fn },
	}
	a.applyHistory(tab, meta, commits, 0, pageFirst)
	tab.loaded = true
	defer tab.cancel()

	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("画出提交图谱"); err != nil {
		t.Fatal(err)
	}
	waitText(t, tt, updates, "修改  a.txt")
	if tab.inspecting == zeroHash || !tt.HasText("图谱") {
		t.Fatalf("单击后没有留在图谱：inspect=%v texts=%q", tab.inspecting != zeroHash, tt.Texts())
	}

	tab.selected = -1
	tab.selection.Clear()
	a.closeInspection(tab)
	tab.searchKind = "定位"
	tt.Frame()
	if err := tt.Click("跳到"); err != nil {
		t.Fatal(err)
	}
	waitText(t, tt, updates, "修改  a.txt")
	if tab.selected != 0 || tab.commits[tab.selected].hash != tab.head {
		t.Fatalf("定位 HEAD 落在 %d hash=%x head=%x", tab.selected, tab.commits[max(tab.selected, 0)].hash, tab.head)
	}
}

func waitText(t *testing.T, tt *ui.Tester, updates <-chan func(), want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
	loop:
		for {
			select {
			case fn := <-updates:
				fn()
			default:
				break loop
			}
		}
		tt.Frame()
		if tt.HasText(want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("缺少 %q，当前文本：%q", want, tt.Texts())
}
