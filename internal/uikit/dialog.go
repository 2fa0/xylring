package uikit

import (
	"runtime"

	"github.com/2fa0/xylring/internal/i18n"
	"github.com/egoist/mygo"
)

func OpenRepoDialog(win *mygo.Window) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:    win,
		Title:     i18n.Tr("导入仓库"),
		Directory: true,
	})
}

func OpenWSLRepoDialog(win *mygo.Window, defaultPath string) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      win,
		Title:       i18n.Tr("导入 WSL 仓库"),
		DefaultPath: defaultPath,
		Directory:   true,
	})
}

func OpenEditorDialog(win *mygo.Window) ([]string, error) {
	opts := mygo.OpenDialogOptions{
		Parent: win,
		Title:  i18n.Tr("选择默认编辑器"),
	}
	if runtime.GOOS == "darwin" {
		opts.DefaultPath = "/Applications"
	}
	return mygo.Dialog.Open(opts)
}

func OpenBoardDirDialog(win *mygo.Window, defaultDir string) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:            win,
		Title:             i18n.Tr("选择看板数据目录"),
		DefaultPath:       defaultDir,
		Directory:         true,
		CreateDirectories: true,
		ShowHiddenFiles:   true,
	})
}

func SaveBoardExportDialog(win *mygo.Window, defaultName string) (string, error) {
	return mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:            win,
		Title:             i18n.Tr("导出看板备份"),
		DefaultPath:       defaultName,
		CreateDirectories: true,
		Filters: []mygo.FileFilter{
			{Name: "JSON", Extensions: []string{"json"}},
		},
	})
}

func OpenBoardImportDialog(win *mygo.Window) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:          win,
		Title:           i18n.Tr("选择看板备份文件"),
		ShowHiddenFiles: true,
		Filters: []mygo.FileFilter{
			{Name: "JSON", Extensions: []string{"json"}},
		},
	})
}
