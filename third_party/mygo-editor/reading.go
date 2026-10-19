package editor

// DiffReadingPosition retains geometry and expanded ranges only, never text,
// buffers, shaped glyphs or callbacks. It can outlive the corresponding diff.
type DiffReadingPosition struct {
	y         float64
	x         [2]float32
	expanded  bool
	revealed  []lineSpan
	oldActive bool
}

func (d *DiffState) ReadingPosition() DiffReadingPosition {
	y := d.scroll.y
	if d.Layout == DiffUnified {
		y = d.eds[1].scrollY
	}
	return DiffReadingPosition{y: y, x: [2]float32{d.eds[0].scrollX, d.eds[1].scrollX}, expanded: d.ExpandUnchanged, revealed: append([]lineSpan(nil), d.revealed...), oldActive: d.oldActive}
}

// RestoreReadingPosition applies after layout, when scroll limits are known.
// Use only for the same file and comparison identity.
func (d *DiffState) RestoreReadingPosition(position DiffReadingPosition) {
	d.ensure()
	d.ExpandUnchanged = position.expanded
	d.revealed = append([]lineSpan(nil), position.revealed...)
	d.expandV++
	d.pendingReading = &position
}

func (d *DiffState) restoreReadingPosition() {
	p := d.pendingReading
	if p == nil {
		return
	}
	d.pendingReading = nil
	d.oldActive = p.oldActive
	for i := range d.eds {
		if d.Layout == DiffUnified && i == 0 {
			continue
		}
		ed := &d.eds[i]
		ed.reveal = revealNone
		ed.setScroll(p.y)
		ed.scrollX = max(0, min(p.x[i], ed.maxScrollX()))
	}
	if d.Layout != DiffUnified {
		d.scroll.set(p.y)
	}
}
