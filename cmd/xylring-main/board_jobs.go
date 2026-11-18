package main

// Board transactions run in submission order, including the final draft save
// when the window closes. Each still uses the shared filesystem lock.
func (a *app) runBoardJob(work func(), publish func()) {
	if a.win == nil {
		work()
		publish()
		return
	}
	previous := a.board.jobTail
	done := make(chan struct{})
	a.board.jobTail = done
	a.board.jobs++
	a.board.writeGen++
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		work()
		a.post(func() {
			a.board.jobs--
			if !a.closed {
				publish()
			}
		})
	}()
}

func (a *app) changeBoard(work func() (boardFile, error), done func(error)) {
	var bf boardFile
	var err error
	a.runBoardJob(func() { bf, err = work() }, func() {
		if err == nil {
			a.applyBoardFile(bf, nil)
		}
		if done != nil {
			done(err)
		} else if err != nil {
			a.setBanner(err.Error(), "")
		}
	})
}
