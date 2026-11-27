package app

import (
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

func configureWindowRendering(win *mygo.Window) {
	// MyGo creates its Metal layer during the first native frame.
	win.OnReadyToShow(func() {
		metalClass := objc.GetClass("CAMetalLayer")
		if metalClass == 0 {
			return
		}
		contentView := objc.ID(win.NativeHandle()).Send(objc.RegisterName("contentView"))
		if layer := findMetalLayer(contentView, metalClass); layer != 0 {
			layer.Send(objc.RegisterName("setMaximumDrawableCount:"), uintptr(2))
		}
	})
}

func findMetalLayer(view objc.ID, metalClass objc.Class) objc.ID {
	sublayers := view.Send(objc.RegisterName("layer")).Send(objc.RegisterName("sublayers"))
	count, at := objc.RegisterName("count"), objc.RegisterName("objectAtIndex:")
	isKind := objc.RegisterName("isKindOfClass:")
	for i := uintptr(0); i < uintptr(sublayers.Send(count)); i++ {
		layer := sublayers.Send(at, i)
		if layer.Send(isKind, metalClass)&0xff != 0 {
			return layer
		}
	}
	subviews := view.Send(objc.RegisterName("subviews"))
	for i := uintptr(0); i < uintptr(subviews.Send(count)); i++ {
		if layer := findMetalLayer(subviews.Send(at, i), metalClass); layer != 0 {
			return layer
		}
	}
	return 0
}
