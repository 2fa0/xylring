package main

import "github.com/egoist/mygo"

func openRepoDialog(win *mygo.Window) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:    win,
		Title:     "导入仓库",
		Directory: true,
	})
}
