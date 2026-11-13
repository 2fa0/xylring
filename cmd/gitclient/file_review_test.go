package main

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
)

func reviewTestTab(paths ...string) *repoTab {
	tab := newTab(1, "/tmp/file-review-repo")
	tab.loaded, tab.branch = true, "main"
	for _, path := range paths {
		tab.compareFiles = append(tab.compareFiles, fileChange{status: "M", path: path})
	}
	tab.fileSel = 0
	return tab
}

func TestFileReviewMarkAndCount(t *testing.T) {
	tab := reviewTestTab("a.go", "b.go", "c.go")
	if viewed, total := tab.reviewedCount(); viewed != 0 || total != 3 {
		t.Fatalf("没标记时计数应为 0/3，实为 %d/%d", viewed, total)
	}
	tab.toggleFileReviewed("a.go")
	tab.toggleFileReviewed("b.go")
	if !tab.fileViewed("a.go") || tab.fileViewed("c.go") {
		t.Fatal("只标记了两个文件，已读状态不对")
	}
	if viewed, total := tab.reviewedCount(); viewed != 2 || total != 3 {
		t.Fatalf("标记两个后计数应为 2/3，实为 %d/%d", viewed, total)
	}
	// 已经不在文件列表里的路径不计入计数。
	tab.toggleFileReviewed("gone.go")
	if viewed, _ := tab.reviewedCount(); viewed != 2 {
		t.Fatalf("列表外的文件不该计入已读，实为 %d", viewed)
	}
	tab.toggleFileReviewed("a.go")
	if tab.fileViewed("a.go") {
		t.Fatal("再点一次应回到未读")
	}
	if viewed, _ := tab.reviewedCount(); viewed != 1 {
		t.Fatalf("取消一个后计数应为 1，实为 %d", viewed)
	}
}

// TestFileReviewInvalidatesWhenContentChanges 钉住失效规则：判定失效靠内容指纹，
// 不靠 status——这两个文件从头到尾都是 M。
func TestFileReviewInvalidatesWhenContentChanges(t *testing.T) {
	tab := reviewTestTab("a.go", "b.go")
	source := &fileDiffSource{old: []byte("a\n"), new: []byte("b\n")}
	changed := &fileDiffSource{old: []byte("a\n"), new: []byte("c\n")}
	stamp, next := fileContentStamp(source), fileContentStamp(changed)
	if stamp == "" || stamp == next {
		t.Fatalf("内容不同，指纹必须不同: %q %q", stamp, next)
	}

	tab.applyFileStamp("a.go", stamp)
	tab.toggleFileReviewed("a.go")
	tab.applyFileStamp("a.go", stamp)
	if !tab.fileViewed("a.go") {
		t.Fatal("内容没变，已读不该被清掉")
	}
	tab.applyFileStamp("a.go", next)
	if tab.fileViewed("a.go") {
		t.Fatal("内容变了，已读必须作废")
	}

	// 没装载过就标记的文件，第一次装载补上指纹，不能反过来把标记清掉。
	tab.toggleFileReviewed("b.go")
	tab.applyFileStamp("b.go", stamp)
	if !tab.fileViewed("b.go") {
		t.Fatal("第一次装载不该清掉刚标记的已读")
	}
}

func TestFileReviewContextSwitchClears(t *testing.T) {
	tab := reviewTestTab("a.go")
	tab.worktree = true
	tab.reviewCtx = "worktree"
	tab.reviewed = map[string]fileReview{"a.go": {viewed: true, stamp: "s"}}
	dropped := func(func()) {}
	a := &app{dispatch: dropped}
	empty := func(context.Context, string) ([]fileChange, error) { return nil, nil }

	a.startDiff(tab, tab.head, zeroHash, empty)
	defer tab.cancel()
	if !tab.fileViewed("a.go") {
		t.Fatal("同一个对比里刷新，已读不该被清掉")
	}

	from, to := hashN(1), hashN(2)
	tab.worktree = false // 从工作区切到两个提交之间。
	a.startDiff(tab, from, to, empty)
	if tab.reviewed != nil {
		t.Fatalf("换了对比，已读必须清空: %v", tab.reviewed)
	}
	if want := hashHex(from) + ":" + hashHex(to); tab.reviewCtx != want {
		t.Fatalf("reviewCtx 应为 %q，实为 %q", want, tab.reviewCtx)
	}
}

func TestFileListHeaderShowsReviewedCount(t *testing.T) {
	tab := reviewTestTab("a.go", "b.go")
	a := &app{tabs: []*repoTab{tab}, dispatch: func(func()) {}}
	tt := ui.NewTester(func(c *ui.Context) { a.inspectFilesPane(c, tab) }, 900, 500)
	defer retireTester(tt)
	tt.Frame()
	if tt.HasText("已读") {
		t.Fatalf("没读过文件就不该出现已读计数: %q", tt.Texts())
	}
	tab.toggleFileReviewed("a.go")
	tt.Frame()
	if !tt.HasText("1/2 已读") {
		t.Fatalf("头部少了已读计数: %q", tt.Texts())
	}
	tab.toggleFileReviewed("b.go")
	tt.Frame()
	if !tt.HasText("2/2 已读") {
		t.Fatalf("头部计数没跟着变: %q", tt.Texts())
	}
}
