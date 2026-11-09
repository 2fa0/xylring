package main

import (
	"context"
	"fmt"
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

func TestParseNumStatZ(t *testing.T) {
	stats := parseNumStatZ([]byte("381\t545\tDisperse.md\x001\t0\tb c.txt\x00-\t-\tbin.dat\x000\t0\t\x00旧.txt\x00新.txt\x00"))
	if len(stats) != 4 {
		t.Fatalf("stats %+v", stats)
	}
	if stats[0].path != "Disperse.md" || stats[0].added != 381 || stats[0].deleted != 545 || stats[0].binary {
		t.Fatalf("text %+v", stats[0])
	}
	if stats[1].path != "b c.txt" || stats[1].added != 1 || stats[1].deleted != 0 {
		t.Fatalf("space %+v", stats[1])
	}
	if stats[2].path != "bin.dat" || !stats[2].binary {
		t.Fatalf("binary %+v", stats[2])
	}
	if stats[3].oldPath != "旧.txt" || stats[3].path != "新.txt" || stats[3].added != 0 || stats[3].deleted != 0 {
		t.Fatalf("rename %+v", stats[3])
	}
	files := []fileChange{
		{status: "M", path: "Disperse.md"},
		{status: "A", path: "b c.txt"},
		{status: "A", path: "bin.dat"},
		{status: "R", path: "新.txt", oldPath: "旧.txt"},
	}
	applyNumStat(files, stats)
	if !files[0].hasStat || files[0].added != 381 || files[0].deleted != 545 {
		t.Fatalf("apply text %+v", files[0])
	}
	if !files[2].hasStat || !files[2].binary {
		t.Fatalf("apply binary %+v", files[2])
	}
	if !files[3].hasStat || files[3].path != "新.txt" {
		t.Fatalf("apply rename %+v", files[3])
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
	byPath := map[string]fileChange{}
	for _, f := range files {
		byPath[f.path] = f
	}
	if !byPath["a.txt"].hasStat || byPath["a.txt"].added != 1 || byPath["a.txt"].deleted != 1 {
		t.Fatalf("a.txt stats %+v", byPath["a.txt"])
	}
	if !byPath["b.txt"].hasStat || byPath["b.txt"].added != 1 || byPath["b.txt"].deleted != 0 {
		t.Fatalf("b.txt stats %+v", byPath["b.txt"])
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
	if !tt.HasText("比较") || !tt.HasText("从") || !tt.HasText("到") {
		t.Fatalf("缺少核心字段：texts=%q", tt.Texts())
	}
	a.compareFrom, a.compareTo = "main", "feature"
	tt.Frame()
	if err := tt.Click("交换"); err != nil {
		t.Fatal(err)
	}
	if a.compareFrom != "feature" || a.compareTo != "main" {
		t.Fatalf("交换失败：from=%q to=%q", a.compareFrom, a.compareTo)
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
	for _, text := range []string{"main", "...", "feature", "M  a.txt", "A  b.txt", "返回", "没有载入内容", "画出提交图谱", "图谱"} {
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

func TestRefSuggestions(t *testing.T) {
	tab := newTab(1, "/tmp/repo")
	tab.branch = "feature"
	tab.refs = map[[20]byte][]refInfo{
		hashN(1): {
			{name: "main", kind: refBranch},
			{name: "v1.0.0", kind: refTag},
		},
		hashN(2): {
			{name: "origin/main", kind: refRemote},
			{name: "feature", kind: refBranch},
		},
	}
	tab.commits = []commit{
		{hash: hashN(3), subject: "commit three"},
	}
	sugs := tab.refSuggestions()
	has := func(list []string, item string) bool {
		for _, s := range list {
			if s == item {
				return true
			}
		}
		return false
	}
	if !has(sugs, "feature") || !has(sugs, "main") || !has(sugs, "HEAD") || !has(sugs, "origin/main") || !has(sugs, "v1.0.0") {
		t.Fatalf("suggestions missing refs: %v", sugs)
	}
	if !has(sugs, shortHash(hashN(3))) {
		t.Fatalf("suggestions missing commit hash: %v", sugs)
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
	for _, text := range []string{"画出提交图谱", "图谱", "M  graph.go", "Ada", "搜索"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)
	if tab.inspecting != zeroHash || tt.HasText("M  graph.go") {
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
	waitText(t, tt, updates, "M  a.txt")
	if tab.inspecting == zeroHash || !tt.HasText("图谱") {
		t.Fatalf("单击后没有留在图谱：inspect=%v texts=%q", tab.inspecting != zeroHash, tt.Texts())
	}

	tab.selected = -1
	tab.selection.Clear()
	a.closeInspection(tab)
	tt.Frame()
	if err := tt.Click("搜索"); err != nil {
		t.Fatal(err)
	}
	waitText(t, tt, updates, "M  a.txt")
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

func TestFileLineStats(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "画出提交图谱", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{
		{status: "M", path: "docs/a.md", added: 381, deleted: 545, hasStat: true},
		{status: "A", path: "bin.dat", binary: true, hasStat: true},
	}
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "没有载入内容"
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"+381", "-545", "a.md", "bin.dat"} {
		if !tt.HasText(text) {
			t.Fatalf("列表缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("+0") || tt.HasText("-0") {
		t.Fatalf("二进制文件不该显示行数：%q", tt.Texts())
	}
	if err := tt.Click("树"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("+381") || !tt.HasText("-545") {
		t.Fatalf("树状缺少行数：%q", tt.Texts())
	}
	r, ok := tt.Find("a.md")
	if !ok {
		t.Fatal("missing a.md")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	if tt.HasText("+381") || tt.HasText("-545") {
		t.Fatalf("树状悬停后行数还露在菜单外：%q", tt.Texts())
	}
}

func TestTakeScrollMove(t *testing.T) {
	var s ui.ScrollState
	var last float32
	if takeScrollMove(&s, &last) {
		t.Fatal("zero should not count as scrolling")
	}
	s.Y = 24
	if !takeScrollMove(&s, &last) {
		t.Fatal("Y change should count as scrolling")
	}
	if takeScrollMove(&s, &last) {
		t.Fatal("unchanged Y should not count as scrolling")
	}
	if filePathTip("a.go", true) != "" {
		t.Fatal("scrolling should hide the path tip")
	}
	if filePathTip("a.go", false) != "a.go" {
		t.Fatal("resting should keep the path tip")
	}
}

func TestFilePathTooltipHidesWhileScrolling(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "画出提交图谱", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)
	files := make([]fileChange, 40)
	for i := range files {
		files[i] = fileChange{status: "M", path: fmt.Sprintf("pkg/file-%02d.go", i), added: i + 1, hasStat: true}
	}
	tab.compareFiles = files
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "没有载入内容"
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	check := func(view string) {
		t.Helper()
		tt.Frame()
		r, ok := tt.Find("file-00.go")
		if !ok {
			t.Fatalf("%s missing file-00.go: %q", view, tt.Texts())
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Advance(time.Second)
		if _, ok := tt.Find("pkg/file-00.go"); !ok {
			t.Fatalf("%s 悬停后没有路径提示：%q", view, tt.Texts())
		}
		tt.Wheel(r.X+r.W/2, r.Y+r.H/2, 0, 80)
		tt.Advance(50 * time.Millisecond)
		if _, ok := tt.Find("pkg/file-00.go"); ok {
			t.Fatalf("%s 滚动时路径提示还在：%q", view, tt.Texts())
		}
		tt.Move(8, 8)
		tt.Advance(time.Second)
	}
	check("列表")
	if err := tt.Click("树"); err != nil {
		t.Fatal(err)
	}
	check("树状")
}

func TestComparisonBannerAndStability(t *testing.T) {
	tab := newTab(1, "/tmp/compare-repo")
	tab.loaded = true
	tab.branch = "main"
	h1 := hashN(1)
	h2 := hashN(2)
	h3 := hashN(3)
	tab.commits = []commit{
		{hash: h1, author: "Ada", subject: "commit 1", when: 1700000000},
		{hash: h2, author: "Bob", subject: "commit 2", when: 1700000100},
		{hash: h3, author: "Charlie", subject: "commit 3", when: 1700000200},
	}
	tab.refs[h1] = []refInfo{{name: "main", kind: refBranch}}
	tab.refs[h2] = []refInfo{{name: "feature", kind: refBranch}}
	for i := range tab.commits {
		tab.graph.add(&tab.commits[i])
	}
	tab.comparison = &commitComparison{
		from:      h2,
		to:        h1,
		fromLabel: "feature",
		toLabel:   "main",
	}
	tab.compareFiles = []fileChange{{status: "M", path: "a.txt"}}
	tab.fileSel = 0
	tab.fileSplit = 28
	tab.diffNote = "没有载入内容"
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	// 1. Verify top comparison banner contents
	for _, text := range []string{"对比模式", "退出对比", "Esc", "⇄"} {
		if !tt.HasText(text) {
			t.Fatalf("横幅缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	// Check that formatted refs are present in banner
	h2Short := shortHash(h2)
	h1Short := shortHash(h1)
	wantFrom := fmt.Sprintf("%s (feature)", h2Short)
	wantTo := fmt.Sprintf("%s (main)", h1Short)
	if !tt.HasText(wantFrom) || !tt.HasText(wantTo) {
		t.Fatalf("横幅缺少对比分支格式：%q 或 %q，当前文本：%q", wantFrom, wantTo, tt.Texts())
	}

	// 2. Multi-selection stability: selecting 3 items does NOT exit comparison mode
	tab.selection.Add(h1)
	tab.selection.Add(h2)
	tab.selection.Add(h3)
	tt.Frame()
	if tab.comparison == nil {
		t.Fatal("选中第 3 个提交后比较模式被意外退出")
	}

	// 3. Selection stability: empty selection does NOT exit comparison mode
	tab.selection.Clear()
	tt.Frame()
	if tab.comparison == nil {
		t.Fatal("清空选择或点击空白后比较模式被意外退出")
	}

	// 4. Test clicking "退出对比" exits comparison mode
	if err := tt.Click("退出对比"); err != nil {
		t.Fatalf("点击退出对比失败: %v", err)
	}
	tt.Frame()
	if tab.comparison != nil {
		t.Fatal("点击退出对比后仍处于比较模式")
	}

	// 5. Test Esc shortcut exits comparison mode
	tab.comparison = &commitComparison{
		from:      h2,
		to:        h1,
		fromLabel: "feature",
		toLabel:   "main",
	}
	tt.Frame()
	if !tt.HasText("退出对比") {
		t.Fatal("重新进入比较模式后未显示退出对比")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tab.comparison != nil {
		t.Fatal("按 Esc 后仍处于比较模式")
	}
}
