package uikit

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	MotionColor = 120 * time.Millisecond
	MotionPane  = 180 * time.Millisecond
)

var UISpring = ui.Animation{Spring: ui.SpringOptions{Duration: 280 * time.Millisecond, Bounce: 0.12}}

func ColorMotion() ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Duration: MotionColor, Ease: ui.EaseOut}
}

func PaneMotion() ui.ElementTransition {
	return ui.ElementTransition{Duration: MotionPane, Ease: ui.EaseOut}
}

func FadeEnter() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: MotionPane,
		Ease:     ui.EaseOut,
		Enter:    &ui.Motion{Opacity: 0},
	}
}

func ContentFade() ui.ContentTransition {
	a := ui.Animation{Duration: MotionColor, Ease: ui.EaseOut}
	return ui.ContentTransition{Animation: &a}
}

func WorkspaceMainMotion() ui.ElementTransition {
	t := PaneMotion()
	t.Match = workspaceMainMatch{}
	return t
}

func CommitGraphMotion(id int) ui.ElementTransition {
	t := PaneMotion()
	t.Match = commitGraphMatch{id: id}
	return t
}

type workspaceMainMatch struct{}

type commitGraphMatch struct{ id int }
