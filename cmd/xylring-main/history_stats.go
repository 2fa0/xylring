package main

import (
	"bufio"
	"context"
	"strings"
	"time"
)

const historyStatsPending = -2

func canReadHistoryStats(ctx context.Context, dir string) bool {
	_, err := configCommand(ctx, dir, "--get-regexp", `^(extensions\.partialclone|remote\..*\.promisor)$`)
	return configExit(err, 1)
}

func (a *app) loadVisibleHistoryStats(t *repoTab) {
	if t.loading || t.historyStatsCancel != nil {
		return
	}
	first, last := t.list.Visible()
	if last < first {
		first, last = 0, 39
	} else {
		first, last = max(0, first-12), last+12
	}
	offset := t.worktreeOffset()
	from, to := max(0, first-offset), min(len(t.commits), last-offset+1)
	if from >= to {
		return
	}
	var hashes [][20]byte
	for _, c := range t.commits[from:to] {
		if c.lines == historyStatsPending {
			hashes = append(hashes, c.hash)
		}
	}
	if len(hashes) == 0 {
		return
	}
	dir, gen := t.path, t.generation
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.historyStatsCancel = cancel
	go func() {
		defer cancel()
		stats := readHistoryStats(ctx, dir, hashes)
		a.post(func() {
			if t.closed || t.generation != gen {
				return
			}
			t.historyStatsCancel = nil
			for _, hash := range hashes {
				i := t.indexOf(hash)
				if i < 0 {
					continue
				}
				if n, ok := stats[hash]; ok {
					t.commits[i].lines = n
				} else {
					t.commits[i].lines = -1
				}
			}
		})
	}()
}

func readHistoryStats(ctx context.Context, dir string, hashes [][20]byte) map[[20]byte]int32 {
	// Partial clones can require a network fetch just to calculate a diff.
	if !canReadHistoryStats(ctx, dir) {
		return nil
	}
	var input strings.Builder
	for _, hash := range hashes {
		input.WriteString(hashHex(hash))
		input.WriteByte('\n')
	}
	stats := make(map[[20]byte]int32, len(hashes))
	args := []string{"log", "--no-walk=unsorted", "--no-color", "--format=%x1e%H", "--shortstat", "--stdin", "--"}
	_ = streamLog(ctx, dir, args, input.String(), splitByte(0x1e), func(sc *bufio.Scanner, _ *gitLease) error {
		for sc.Scan() {
			hash, lines := splitShortstat(strings.TrimSpace(sc.Text()))
			if h, err := parseHash(hash); err == nil {
				stats[h] = lines
			}
		}
		return sc.Err()
	})
	return stats
}
