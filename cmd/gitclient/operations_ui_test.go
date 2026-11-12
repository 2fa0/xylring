package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestOperationDialogCanCloseDuringPreparationAndReopen(t *testing.T) {
	dir, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	updates := make(chan func(), 8)
	a := &app{tabs: []*repoTab{newTab(1, dir)}, dispatch: func(f func()) { updates <- f }}
	a.openOperation("保存 Stash", "", "")
	a.showOperation = false
	nextDialogUpdate(t, updates)()
	if a.op.busy {
		t.Fatal("dismissed preparation stayed busy")
	}
	a.openOperation("设置身份", "", "")
	nextDialogUpdate(t, updates)()
	tt := ui.NewTester(a.operationModal, 860, 560)
	tt.Frame()
	tt.Frame()
	if !a.op.ready || a.op.busy || a.op.err != "" {
		t.Fatalf("dialog: %+v", a.op)
	}
	if e := tt.Click("关闭"); e != nil {
		t.Fatal(e)
	}
	tt.Frame()
	tt.Frame()
	if a.showOperation {
		t.Fatal("dialog stayed open")
	}
}

func TestStashHubShowsActions(t *testing.T) {
	a := &app{showOperation: true, op: repoOperation{repo: "/tmp/example", action: "Stash", hub: "Stash", ready: true, stashes: []stashEntry{{hash: "abc", label: "stash@{0}: On main: wip"}}}}
	tt := ui.NewTester(a.operationModal, 860, 560)
	tt.Frame()
	tt.Frame()
	for _, text := range []string{"Stash", "保存", "查看", "应用", "弹出", "删除"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in %q", text, tt.Texts())
		}
	}
	if tt.HasText("执行") {
		t.Fatal("stash hub should not show a generic run button")
	}
}

func TestRemoteHubShowsActions(t *testing.T) {
	a := &app{showOperation: true, op: repoOperation{repo: "/tmp/example", action: "远程", hub: "远程", ready: true, remotes: []string{"origin"}, remote: "origin", remoteURLs: map[string]string{"origin": "git@example.com:repo.git"}}}
	tt := ui.NewTester(a.operationModal, 860, 560)
	tt.Frame()
	tt.Frame()
	for _, text := range []string{"远程", "保存地址", "删除远程", "添加", "推送分支", "推送标签"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q in %q", text, tt.Texts())
		}
	}
}

func TestNonDestructiveOperationsSkipConfirmationCheckbox(t *testing.T) {
	a := &app{showOperation: true, op: repoOperation{repo: "/tmp/example", action: "设置身份", name: "Ada", value: "ada@example.com", ready: true}}
	tt := ui.NewTester(a.operationModal, 860, 560)
	tt.Frame()
	tt.Frame()
	if tt.HasText("确认对上述仓库和目标执行操作") {
		t.Fatal("non-destructive identity setup should not require a confirmation checkbox")
	}
}

func TestParseLineRangeAndDeriveClonePath(t *testing.T) {
	for _, tc := range []struct {
		in   string
		a, b int
	}{
		{"10,20", 10, 20},
		{"10-20", 10, 20},
		{"10:20", 10, 20},
		{"10..20", 10, 20},
		{"15", 15, 15},
	} {
		a, b, err := parseLineRange(tc.in)
		if err != nil || a != tc.a || b != tc.b {
			t.Fatalf("parseLineRange(%q) = %d, %d, %v", tc.in, a, b, err)
		}
	}
	nonEmpty := t.TempDir()
	os.WriteFile(nonEmpty+"/existing.txt", []byte("x"), 0o600)
	if got := deriveClonePath(nonEmpty, "https://github.com/org/xylring.git"); got != filepath.Join(nonEmpty, "xylring") {
		t.Fatalf("deriveClonePath on non-empty dir = %q", got)
	}
	emptyDir := t.TempDir()
	if got := deriveClonePath(emptyDir, "https://github.com/org/xylring.git"); got != emptyDir {
		t.Fatalf("deriveClonePath on empty dir = %q", got)
	}
}
