package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	motionColor = 120 * time.Millisecond
	motionPane  = 180 * time.Millisecond
)

func colorMotion() ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Duration: motionColor, Ease: ui.EaseOut}
}

func paneMotion() ui.ElementTransition {
	return ui.ElementTransition{Duration: motionPane, Ease: ui.EaseOut}
}

func fadeEnter() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: motionPane,
		Ease:     ui.EaseOut,
		Enter:    &ui.Motion{Opacity: 0},
	}
}

func contentFade() ui.ContentTransition {
	a := ui.Animation{Duration: motionColor, Ease: ui.EaseOut}
	return ui.ContentTransition{Animation: &a}
}

func workspaceMainMotion() ui.ElementTransition {
	t := paneMotion()
	t.Match = workspaceMainMatch{}
	return t
}

func commitGraphMotion(id int) ui.ElementTransition {
	t := paneMotion()
	t.Match = commitGraphMatch{id: id}
	return t
}

type workspaceMainMatch struct{}

type commitGraphMatch struct{ id int }
