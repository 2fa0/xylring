package main

import (
	_ "embed"

	"github.com/2fa0/xylring/internal/app"
)

//go:embed resources/update-window.png
var updateIcon []byte

func main() {
	app.Run(app.Assets{
		UpdateIcon: updateIcon,
	})
}
