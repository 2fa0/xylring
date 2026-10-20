package main

import "github.com/egoist/mygo"

func openRepoDialog(win *mygo.Window) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:    win,
		Title:     "打开 Git 仓库",
		Directory: true,
		Message:   "选择仓库目录",
	})
}
