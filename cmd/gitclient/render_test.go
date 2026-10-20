package main

import (
	"fmt"
	editor "github.com/egoist/mygo/gitclient-editor"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestP0RenderedLayouts(t *testing.T) {
	out := os.Getenv("P0_ARTIFACT_DIR")
	if out == "" {
		t.Skip("set P0_ARTIFACT_DIR to save UI frames")
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	tab := newTab(1, "/tmp/git-client")
	tab.loaded = true
	tab.branch = "main"
	tab.options = defaultViewSettings()
	tab.commits = []commit{{hash: hashN(1), author: "Ada", subject: "支持文件历史和分支操作", when: 1700000000}}
	tab.graph.add(&tab.commits[0])
	tab.refs = map[[20]byte][]refInfo{hashN(1): {{name: "main", kind: refBranch}, {name: "origin/main", kind: refRemote}, {name: "v1.0", kind: refTag}}}
	tab.selected, tab.commitApplied = 0, 0
	tab.shownKey = tab.selectionKey()
	tab.files = []fileChange{{status: "M", path: "src/history.go"}, {status: "R", path: "docs/阅读说明.md", oldPath: "docs/旧说明.md"}}
	tab.fileSel, tab.fileApplied = 0, 0
	var before, after strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&before, "line %03d: previous value\n", i)
		fmt.Fprintf(&after, "line %03d: updated value\n", i)
	}
	tab.diff = &editor.DiffState{Old: editor.NewBuffer(before.String()), New: editor.NewBuffer(after.String())}
	tab.diff.Font.Family, tab.diff.Font.Size = "monospace", 13
	tab.info = &commitInfo{message: "支持文件历史和分支操作\n\n完整说明保留在展开摘要中。", author: "Ada", authorEmail: "ada@example.invalid"}
	a := &app{tabs: []*repoTab{tab}, split: 55, filePane: 260}
	save := func(name string, tt *ui.Tester) {
		t.Helper()
		tt.Frame()
		f, err := os.Create(filepath.Join(out, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	save("wide", ui.NewTester(a.view, 1200, 800))
	save("narrow", ui.NewTester(a.view, 860, 560))
	tab.focusDiff = true
	save("focused", ui.NewTester(a.view, 1200, 800))
	tab.focusDiff = false
	a.op = &operation{Kind: "Reset", Repo: tab.path, Target: "HEAD~1", Mode: "hard", ConfirmHard: true}
	a.showOperation = true
	save("hard-reset", ui.NewTester(a.view, 1200, 800))
	a.showOperation = false
	a.beginSettings()
	save("settings", ui.NewTester(a.view, 1200, 800))
}
