package main

import (
	"context"
	"maps"
	"unsafe"
)

const historyMemoryStep = 64 << 20

// One query uses one Git stream, including --follow's rename state. Backpressure
// keeps at most two unread records; switching repositories closes the process.
type historyPager struct {
	rows    chan commit
	cancel  context.CancelFunc
	err     error // Published by closing rows.
	pending *commit
}

func newHistoryPager(parent context.Context, dir string, q historyQuery, tips []string, skip int) *historyPager {
	ctx, cancel := context.WithCancel(parent)
	p := &historyPager{rows: make(chan commit, 1), cancel: cancel}
	go func() {
		defer close(p.rows)
		defer cancel()
		p.err = streamHistory(ctx, dir, q, tips, skip, 0, func(c commit) error {
			select {
			case p.rows <- c:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	return p
}

func (p *historyPager) close() { p.cancel() }

func (p *historyPager) next(ctx context.Context, n int, maxBytes int64) ([]commit, bool, error) {
	rows := make([]commit, 0, n)
	var size int64
	if p.pending != nil {
		rows = append(rows, *p.pending)
		size = commitBytes(p.pending)
		p.pending = nil
	}
	for {
		select {
		case <-ctx.Done():
			p.close()
			return nil, false, ctx.Err()
		case c, ok := <-p.rows:
			if !ok {
				return rows, false, p.err
			}
			if len(rows) == n || len(rows) > 0 && size+commitBytes(&c) > maxBytes {
				p.pending = &c
				return rows, true, nil
			}
			rows = append(rows, c)
			size += commitBytes(&c)
		}
	}
}

func commitBytes(c *commit) int64 {
	// Includes the hash index's approximate entry cost and string storage.
	return int64(unsafe.Sizeof(*c) + 64 + uintptr(len(c.subject)+len(c.author)) + uintptr(len(c.parents))*unsafe.Sizeof(zeroHash))
}

type preparedHistory struct {
	commits []commit
	graph   graph
	authors map[string]string
	index   map[[20]byte]int
	bytes   int64
}

func prepareHistory(ctx context.Context, commits []commit, g graph, authors map[string]string, drawGraph bool) (preparedHistory, error) {
	t := &repoTab{authors: maps.Clone(authors)}
	p := preparedHistory{commits: commits, graph: g, index: make(map[[20]byte]int, len(commits))}
	var previous []seg
	for i := range commits {
		if err := ctx.Err(); err != nil {
			return preparedHistory{}, err
		}
		c := &commits[i]
		c.author = t.intern(c.author)
		if drawGraph {
			p.graph.add(c)
		}
		p.index[c.hash] = i
		p.bytes += commitBytes(c)
		if len(c.segs) > 0 && (len(previous) == 0 || &previous[0] != &c.segs[0]) {
			p.bytes += int64(cap(c.segs)) * int64(unsafe.Sizeof(seg{}))
		}
		previous = c.segs
	}
	p.authors = t.authors
	return p, nil
}
