package main

import (
	"runtime"

	"github.com/egoist/mygo"
)

func openRepoDialog(win *mygo.Window) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:    win,
		Title:     tr("导入仓库"),
		Directory: true,
	})
}

func openEditorDialog(win *mygo.Window) ([]string, error) {
	opts := mygo.OpenDialogOptions{
		Parent: win,
		Title:  tr("选择默认编辑器"),
	}
	if runtime.GOOS == "darwin" {
		opts.DefaultPath = "/Applications"
	}
	return mygo.Dialog.Open(opts)
}
