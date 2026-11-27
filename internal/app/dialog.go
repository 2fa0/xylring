package app

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

func openWSLRepoDialog(win *mygo.Window, defaultPath string) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      win,
		Title:       tr("导入 WSL 仓库"),
		DefaultPath: defaultPath,
		Directory:   true,
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

func openBoardDirDialog(win *mygo.Window, defaultDir string) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:            win,
		Title:             tr("选择看板数据目录"),
		DefaultPath:       defaultDir,
		Directory:         true,
		CreateDirectories: true,
		ShowHiddenFiles:   true,
	})
}

func saveBoardExportDialog(win *mygo.Window, defaultName string) (string, error) {
	return mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:            win,
		Title:             tr("导出看板备份"),
		DefaultPath:       defaultName,
		CreateDirectories: true,
		Filters: []mygo.FileFilter{
			{Name: "JSON", Extensions: []string{"json"}},
		},
	})
}

func openBoardImportDialog(win *mygo.Window) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:          win,
		Title:           tr("选择看板备份文件"),
		ShowHiddenFiles: true,
		Filters: []mygo.FileFilter{
			{Name: "JSON", Extensions: []string{"json"}},
		},
	})
}
