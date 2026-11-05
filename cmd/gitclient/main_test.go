package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEmptyView(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("导入仓库") || !tt.HasText("Xylring") {
		t.Fatalf("空窗口文本：%q", tt.Texts())
	}
}

func TestEmptyRecentRepos(t *testing.T) {
	a := &app{recent: []string{"/tmp/work/alpha", "/tmp/网关代码/gateway-source"}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"Xylring", "导入仓库", "最近的仓库", "alpha", "gateway-source", "/tmp/work/alpha"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	title, ok := tt.Find("Xylring")
	if !ok {
		t.Fatal("没有标题")
	}
	importBtn, ok := tt.Find("导入仓库")
	if !ok {
		t.Fatal("没有导入仓库")
	}
	alpha, ok := tt.Find("alpha")
	if !ok {
		t.Fatal("没有最近仓库")
	}
	if importBtn.Y < title.Y || alpha.Y < importBtn.Y {
		t.Fatalf("导入页顺序不对：title=%+v import=%+v alpha=%+v", title, importBtn, alpha)
	}
	if alpha.X+8 < title.X {
		t.Fatalf("最近仓库没有跟标题左对齐：title.x=%.0f alpha.x=%.0f", title.X, alpha.X)
	}
}

func TestRemoveRecentRepo(t *testing.T) {
	alpha := "/tmp/work/alpha"
	beta := "/tmp/work/beta"
	a := &app{recent: []string{alpha, beta}, banner: "找不到这个仓库", bannerPath: alpha}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	remove, ok := tt.Find("关闭 alpha")
	if !ok {
		t.Fatalf("没有移除按钮：%q", tt.Texts())
	}
	name, ok := tt.Find("alpha")
	if !ok {
		t.Fatal("没有仓库名")
	}
	if remove.X < name.X {
		t.Fatalf("移除按钮不在右侧：remove=%+v name=%+v", remove, name)
	}
	if err := tt.Click("关闭 alpha"); err != nil {
		t.Fatal(err)
	}
	if len(a.recent) != 1 || a.recent[0] != beta {
		t.Fatalf("recent=%v", a.recent)
	}
	if a.banner != "" || a.bannerPath != "" {
		t.Fatalf("banner=%q path=%q", a.banner, a.bannerPath)
	}
	if len(a.tabs) != 0 {
		t.Fatalf("点移除却打开了仓库：tabs=%d", len(a.tabs))
	}
	if tt.HasText("/tmp/work/alpha") {
		t.Fatalf("移除后还在：%q", tt.Texts())
	}
	if !tt.HasText("beta") {
		t.Fatalf("另一个仓库不见了：%q", tt.Texts())
	}
}

func TestRecentRepoMissingError(t *testing.T) {
	missing := "/tmp/gone-repo"
	a := &app{
		recent:     []string{"/tmp/work/alpha", missing},
		banner:     "找不到这个仓库",
		bannerPath: missing,
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("找不到这个仓库") || !tt.HasText("gone-repo") {
		t.Fatalf("缺少错误或仓库名：%q", tt.Texts())
	}
	errText, ok := tt.Find("找不到这个仓库")
	if !ok {
		t.Fatal("没有错误文案")
	}
	gone, ok := tt.Find("gone-repo")
	if !ok {
		t.Fatal("没有失效仓库")
	}
	importBtn, ok := tt.Find("导入仓库")
	if !ok {
		t.Fatal("没有导入仓库")
	}
	alpha, ok := tt.Find("alpha")
	if !ok {
		t.Fatal("没有正常仓库")
	}
	if errText.Y < importBtn.Y {
		t.Fatalf("错误还在导入按钮上面：err=%+v import=%+v", errText, importBtn)
	}
	if errText.Y < alpha.Y {
		t.Fatalf("错误没有跟失效仓库在一起：err=%+v alpha=%+v gone=%+v", errText, alpha, gone)
	}
	dy := errText.Y - gone.Y
	if dy < 0 {
		dy = -dy
	}
	if dy > 48 {
		t.Fatalf("错误离失效仓库太远：err=%+v gone=%+v", errText, gone)
	}
}

func TestCommitGraphView(t *testing.T) {
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
	tab.refs = map[[20]byte][]refInfo{tab.head: {{name: "main", kind: refBranch}}}
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"画出提交图谱", "图谱", "main", "HEAD", "导入仓库"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("拉取") || tt.HasText("Stash") {
		t.Fatal("图谱视图还留着已删除的操作")
	}
	if err := tt.Click("返回首页"); err != nil {
		t.Fatal(err)
	}
	if !a.showHome || len(a.tabs) != 1 || !tt.HasText("导入仓库") || !tt.HasText("返回当前仓库") {
		t.Fatalf("返回首页后没有回到导入页或误关了已打开仓库：showHome=%v tabs=%d texts=%q", a.showHome, len(a.tabs), tt.Texts())
	}
	if err := tt.Click("返回当前仓库"); err != nil {
		t.Fatal(err)
	}
	if a.showHome || !tt.HasText("画出提交图谱") {
		t.Fatalf("未能返回已打开仓库：showHome=%v texts=%q", a.showHome, tt.Texts())
	}
}

func TestCopyHashFromGraph(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.commits = []commit{
		{hash: hashN(1), author: "Ada", subject: "画出提交图谱"},
	}
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click(shortHash(hashN(1))); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != hashHex(hashN(1)) {
		t.Fatalf("copied %q", tt.Clipboard())
	}
}

func TestRepoListShows(t *testing.T) {
	tab := newTab(1, "/tmp/work/alpha/lib/nested")
	tab.loaded = true
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/alpha", "/tmp/work/alpha/lib/nested", "/tmp/work/beta"},
		foundFrom: "/tmp/work",
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"alpha", "lib/nested", "beta"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("切换仓库") || tt.HasText("选择要打开的仓库") {
		t.Fatalf("还在用独立选择页：%q", tt.Texts())
	}
	parent, ok := tt.Find("alpha")
	if !ok {
		t.Fatal("没有父仓库")
	}
	child, ok := tt.Find("lib/nested")
	if !ok {
		t.Fatal("没有子仓库")
	}
	sibling, ok := tt.Find("beta")
	if !ok {
		t.Fatal("没有并列仓库")
	}
	if child.X <= parent.X {
		t.Fatalf("子仓库没有缩进：parent=%+v child=%+v", parent, child)
	}
	if sibling.X != parent.X {
		t.Fatalf("并列仓库不该缩进：parent=%+v sibling=%+v", parent, sibling)
	}
}

func TestRepoListLabelNestsUnderParent(t *testing.T) {
	parent := "/tmp/网关代码"
	items := []string{parent, parent + "/nginx_engine", parent + "/flow_engine", parent + "/gateway-source"}
	if got := repoListLabel(parent, items, parent); got != "网关代码" {
		t.Fatalf("parent label=%q", got)
	}
	if got := repoListLabel(parent, items, parent+"/flow_engine"); got != "flow_engine" {
		t.Fatalf("child label=%q", got)
	}
	if repoNestLevel(items, parent) != 0 {
		t.Fatalf("parent level=%d", repoNestLevel(items, parent))
	}
	if repoNestLevel(items, parent+"/flow_engine") != 1 {
		t.Fatalf("child level=%d", repoNestLevel(items, parent+"/flow_engine"))
	}
}

func TestRepoSidebarResizes(t *testing.T) {
	tab := newTab(1, "/tmp/work/alpha")
	tab.loaded = true
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/alpha", "/tmp/work/beta"},
		foundFrom: "/tmp/work",
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if a.repoSplit != 320 {
		t.Fatalf("默认宽度=%v", a.repoSplit)
	}
	label, ok := tt.Find("仓库")
	if !ok {
		t.Fatal("没有仓库侧栏")
	}
	x, y := a.repoSplit, label.Y+200
	tt.Press(x, y)
	tt.Move(x+80, y)
	tt.Release(x+80, y)
	if a.repoSplit <= 320 {
		t.Fatalf("拖拽后宽度没变：%v", a.repoSplit)
	}
}

func TestRepoListSwitches(t *testing.T) {
	root := t.TempDir()
	alpha := root + "/alpha"
	beta := root + "/beta"
	initGitDir(t, alpha)
	initGitDir(t, beta)
	tab := newTab(1, alpha)
	tab.loaded = true
	updates := make(chan func(), 16)
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{alpha, beta},
		foundFrom: root,
		persist:   false,
		dispatch:  func(fn func()) { updates <- fn },
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("beta"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, item := range a.tabs {
			item.cancel()
		}
	}()
	if len(a.found) != 2 || len(a.tabs) != 2 || a.tabs[a.active].path != beta {
		t.Fatalf("found=%v tabs=%d active=%q", a.found, len(a.tabs), a.tabs[a.active].path)
	}
	if !tt.HasText("beta") || !tt.HasText("alpha") {
		t.Fatalf("切换后列表不见了：%q", tt.Texts())
	}
}

func TestChromeClearsTitleBarControls(t *testing.T) {
	tab := newTab(1, "/tmp/prototype-repo")
	tab.loaded = true
	tab.branch = "main"
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 72, Right: 10})
	tt.Frame()
	r, ok := tt.Find("导入仓库")
	if !ok {
		t.Fatalf("没有导入仓库：%q", tt.Texts())
	}
	if r.X < 72 {
		t.Fatalf("导入仓库还在红绿灯下面：x=%.0f", r.X)
	}
	toggle, ok := tt.Find("切换侧边栏")
	if !ok {
		t.Fatalf("没有切换侧边栏按钮：%q", tt.Texts())
	}
	if toggle.X < 72 {
		t.Fatalf("切换侧边栏按钮还在红绿灯下面：x=%.0f", toggle.X)
	}
}

func TestSingleRepoShowsSidebarByDefault(t *testing.T) {
	tab := newTab(1, "/tmp/work/myrepo")
	tab.loaded = true
	tab.branch = "main"
	a := &app{tabs: []*repoTab{tab}}
	if !a.showRepoSidebar() {
		t.Fatal("单个仓库导入默认应展示侧边栏")
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("仓库") || !tt.HasText("工作区代码") {
		t.Fatalf("侧边栏未展示仓库或工作区代码：%q", tt.Texts())
	}
}

func TestToggleSidebarButtonAndShortcut(t *testing.T) {
	tab := newTab(1, "/tmp/work/myrepo")
	tab.loaded = true
	tab.branch = "main"
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	if !a.showRepoSidebar() {
		t.Fatal("单个仓库默认应展示侧边栏")
	}
	if !tt.HasText("仓库") || !tt.HasText("工作区代码") {
		t.Fatalf("初始时应展示侧边栏：%q", tt.Texts())
	}

	// 1. 点击切换按钮隐藏侧边栏
	if err := tt.Click("切换侧边栏"); err != nil {
		t.Fatalf("点击切换侧边栏失败：%v", err)
	}
	tt.Frame()
	if a.showRepoSidebar() {
		t.Fatal("点击后侧边栏应被隐藏")
	}
	if _, ok := tt.Find("工作区代码"); ok {
		t.Fatalf("隐藏后不应再展示工作区代码：%q", tt.Texts())
	}

	// 2. 再次点击切换按钮恢复显示侧边栏
	if err := tt.Click("切换侧边栏"); err != nil {
		t.Fatalf("再次点击切换侧边栏失败：%v", err)
	}
	tt.Frame()
	if !a.showRepoSidebar() {
		t.Fatal("再次点击后侧边栏应恢复显示")
	}
	if _, ok := tt.Find("工作区代码"); !ok {
		t.Fatalf("恢复后应展示工作区代码：%q", tt.Texts())
	}

	// 3. 快捷键 CmdOrCtrl+B 隐藏
	tt.Key(commandModifier(), ui.KeyB)
	tt.Frame()
	if a.showRepoSidebar() {
		t.Fatal("快捷键后侧边栏应被隐藏")
	}
	if _, ok := tt.Find("工作区代码"); ok {
		t.Fatalf("快捷键隐藏后不应展示工作区代码：%q", tt.Texts())
	}

	// 4. 快捷键 CmdOrCtrl+B 恢复
	tt.Key(commandModifier(), ui.KeyB)
	tt.Frame()
	if !a.showRepoSidebar() {
		t.Fatal("快捷键后侧边栏应恢复显示")
	}
	if _, ok := tt.Find("工作区代码"); !ok {
		t.Fatalf("快捷键恢复后应展示工作区代码：%q", tt.Texts())
	}
}

func TestToggleSidebarEmptyState(t *testing.T) {
	a := &app{}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if a.showRepoSidebar() {
		t.Fatal("空状态下不应显示侧边栏")
	}
	if _, ok := tt.Find("切换侧边栏"); ok {
		t.Fatal("空状态下不应展示切换侧边栏按钮")
	}
}

func TestChromeCentersTitle(t *testing.T) {
	tab := newTab(1, "/tmp/网关代码/gateway-source")
	tab.loaded = true
	tab.branch = "dev"
	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 72, Right: 10})
	tt.Frame()
	label := "gateway-source  ·  dev"
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("没有标题：%q", tt.Texts())
	}
	mid := r.X + r.W/2
	if mid < 500 || mid > 600 {
		t.Fatalf("标题没有居中：mid=%.0f box=%+v", mid, r)
	}
	if r.X < 80 {
		t.Fatalf("标题还在红绿灯下面：x=%.0f", r.X)
	}
}

func TestOpenReposShareSidebar(t *testing.T) {
	one := newTab(1, "/tmp/work/alpha")
	two := newTab(2, "/tmp/other/beta")
	one.loaded, two.loaded = true, true
	a := &app{tabs: []*repoTab{one, two}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"alpha", "beta"} {
		if !tt.HasText(text) {
			t.Fatalf("缺少 %q，当前文本：%q", text, tt.Texts())
		}
	}
	if tt.HasText("● ") {
		t.Fatalf("还在用顶部标签：%q", tt.Texts())
	}
}

func TestRepoListCloseButton(t *testing.T) {
	tab := newTab(1, "/tmp/work/alpha")
	tab.loaded = true
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/alpha", "/tmp/work/beta"},
		foundFrom: "/tmp/work",
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	for _, text := range []string{"关闭 alpha", "关闭 beta"} {
		if !tt.HasText(text) {
			t.Fatalf("仓库列表缺少 %q：%q", text, tt.Texts())
		}
	}
	if err := tt.Click("关闭 beta"); err != nil {
		t.Fatal(err)
	}
	if len(a.found) != 1 || a.found[0] != "/tmp/work/alpha" {
		t.Fatalf("未打开的仓库还在列表里：%v", a.found)
	}
	if len(a.tabs) != 1 || a.tabs[0].path != "/tmp/work/alpha" {
		t.Fatalf("关掉未打开的仓库影响了当前仓库：tabs=%d", len(a.tabs))
	}
}

func TestRepoListClosesInactive(t *testing.T) {
	one := newTab(1, "/tmp/work/alpha")
	two := newTab(2, "/tmp/other/beta")
	one.loaded, two.loaded = true, true
	a := &app{tabs: []*repoTab{one, two}, dispatch: func(func()) {}}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("关闭 beta"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 1 {
		t.Fatalf("关闭后 tabs=%d", len(a.tabs))
	}
	if a.tabs[0].path != "/tmp/work/alpha" {
		t.Fatalf("留下的是 %q", a.tabs[0].path)
	}
}

func TestHomeReturnsToWelcomeFromFoundRepos(t *testing.T) {
	tab := newTab(1, "/tmp/work/alpha")
	tab.loaded = true
	a := &app{
		tabs:      []*repoTab{tab},
		found:     []string{"/tmp/work/alpha", "/tmp/work/beta"},
		foundFrom: "/tmp/work",
		recent:    []string{"/tmp/work/alpha", "/tmp/work/beta"},
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if err := tt.Click("返回首页"); err != nil {
		t.Fatal(err)
	}
	if !a.showHome || len(a.tabs) != 1 || !tt.HasText("最近的仓库") || !tt.HasText("导入仓库") || !tt.HasText("返回当前仓库") {
		t.Fatalf("没有回到导入页：showHome=%v tabs=%d found=%v texts=%q", a.showHome, len(a.tabs), a.found, tt.Texts())
	}
	if tab.closed {
		t.Fatal("返回首页不应强制关闭已打开的仓库")
	}
}

func TestRecentReposSearchThreshold(t *testing.T) {
	// <= 8: 查找功能不显示
	eight := make([]string, 8)
	for i := range eight {
		eight[i] = fmt.Sprintf("/tmp/repo-%d", i+1)
	}
	a := &app{recent: eight}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if tt.HasText("查找最近仓库") {
		t.Fatal("8个或更少仓库时不应显示查找功能")
	}
	for _, p := range eight {
		if !tt.HasText(repoTitle(p)) {
			t.Fatalf("缺少仓库 %q", p)
		}
	}

	// > 8: 显示查找功能
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = fmt.Sprintf("/tmp/repo-%d", i+1)
	}
	a = &app{recent: nine}
	tt = ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("查找最近仓库") {
		t.Fatal("超过8个仓库时应显示查找功能")
	}
}

func TestRecentReposSearchFilter(t *testing.T) {
	repos := []string{
		"/tmp/company/前端代码",
		"/tmp/company/服务代码",
		"/tmp/company/服务代码/svc-fapi-front",
		"/tmp/company/服务代码/svc-fapi",
		"/tmp/company/服务代码/svc-bapi-front",
		"/tmp/company/服务代码/svc-bapi",
		"/tmp/company/编解码代码/codec-go-sdk",
		"/tmp/company/编解码代码/codec-core",
		"/tmp/company/编解码代码/codec-python-sdk",
		"/tmp/company/网关代码/nginx_engine",
	}
	a := &app{recent: repos}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	if err := tt.Click("查找最近仓库"); err != nil {
		t.Fatal(err)
	}
	tt.Type("fapi")
	if !tt.HasText("svc-fapi-front") || !tt.HasText("svc-fapi") {
		t.Fatalf("未能过滤出 fapi 仓库：%q", tt.Texts())
	}
	if tt.HasText("svc-bapi") || tt.HasText("nginx_engine") || tt.HasText("前端代码") {
		t.Fatalf("包含了不匹配的仓库：%q", tt.Texts())
	}

	// 搜索路径关键词
	if err := tt.Click("Clear"); err != nil {
		t.Fatal(err)
	}
	tt.Type("编解码")
	if !tt.HasText("codec-go-sdk") || !tt.HasText("codec-core") || !tt.HasText("codec-python-sdk") {
		t.Fatalf("按路径过滤失败：%q", tt.Texts())
	}
	if tt.HasText("svc-fapi") {
		t.Fatalf("包含了其他路径仓库：%q", tt.Texts())
	}

	// 搜索无匹配关键词
	if err := tt.Click("Clear"); err != nil {
		t.Fatal(err)
	}
	tt.Type("nonexistent_repo")
	if !tt.HasText("未找到匹配的仓库") {
		t.Fatalf("无匹配时应显示提示文案：%q", tt.Texts())
	}

	// 清空恢复所有仓库
	if err := tt.Click("Clear"); err != nil {
		t.Fatal(err)
	}
	for _, p := range repos {
		if !tt.HasText(repoTitle(p)) {
			t.Fatalf("清空搜索后缺少 %q", p)
		}
	}
}

func TestRecentReposSearchDeleteToThreshold(t *testing.T) {
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = fmt.Sprintf("/tmp/repo-%d", i+1)
	}
	a := &app{recent: nine}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()
	if !tt.HasText("查找最近仓库") {
		t.Fatal("9个仓库应显示查找")
	}

	// 删除一个仓库后降到8个
	if err := tt.Click("关闭 repo-9"); err != nil {
		t.Fatal(err)
	}
	if len(a.recent) != 8 {
		t.Fatalf("删除后应为8个仓库，实际：%d", len(a.recent))
	}
	if tt.HasText("查找最近仓库") {
		t.Fatal("降到8个后不应再显示查找功能")
	}
}

func TestRecentReposSearchShortcutFocus(t *testing.T) {
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = fmt.Sprintf("/tmp/repo-%d", i+1)
	}
	a := &app{recent: nine}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	tt.Key(commandModifier(), ui.KeyF)
	if !tt.Focused("查找最近仓库") {
		t.Fatal("Cmd/Ctrl+F 未聚焦到查找输入框")
	}
}

func TestRecentReposSearchSubmitOpensRepo(t *testing.T) {
	root := t.TempDir()
	target := root + "/my-target-repo"
	initGitDir(t, target)

	nine := []string{target}
	for i := 1; i < 9; i++ {
		nine = append(nine, fmt.Sprintf("/tmp/other-%d", i))
	}
	updates := make(chan func(), 64)
	a := &app{
		recent:   nine,
		persist:  false,
		dispatch: func(fn func()) { updates <- fn },
	}
	tt := ui.NewTester(a.view, 1100, 720)
	tt.Frame()

	if err := tt.Click("查找最近仓库"); err != nil {
		t.Fatal(err)
	}
	tt.Type("target")
	tt.Key(0, ui.KeyEnter)

	// 等待 openRepo 协程回调并在主线程执行
	waitUpdates(t, updates, func() bool { return len(a.tabs) > 0 })
	defer func() {
		for _, tab := range a.tabs {
			tab.cancel()
		}
	}()
	tt.Frame()
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		want = target
	}
	if len(a.tabs) != 1 || a.tabs[0].path != want {
		t.Fatalf("回车未打开匹配的目标仓库：tabs=%d got=%q want=%q", len(a.tabs), a.tabs[0].path, want)
	}
}
