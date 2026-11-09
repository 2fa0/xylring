package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"slices"
	"strings"
	"time"
)

type savedRepo struct {
	Path   string
	Draft  string
	Amend  bool
	Commit string
	File   string
}
type savedProvenance struct{ Agent, Model string }
type savedSession struct {
	RingSkipNotice   bool              `json:"ring_skip_notice,omitempty"`
	RecentProvenance []savedProvenance `json:"recent_provenance,omitempty"`
	Repos            []savedRepo
	Active           string
	BottomFullWidth  *bool    `json:"bottom_full_width,omitempty"`
	RepoOrder        []string `json:"repo_order,omitempty"`
	Found            []string `json:"found,omitempty"`
	FoundFrom        string   `json:"found_from,omitempty"`
}

func loadSession() savedSession {
	var s savedSession
	p, e := configFile("session.json")
	if e != nil {
		return s
	}
	b, e := os.ReadFile(p)
	if e == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}
func (a *app) queueSession() {
	if !a.persist || !a.sessionReady || a.closed {
		return
	}
	select {
	case err := <-a.sessionResults:
		if err != nil {
			a.setBanner("保存会话失败，将自动重试："+err.Error(), "")
		} else if strings.HasPrefix(a.banner, "保存会话失败，将自动重试：") {
			a.setBanner("", "")
		}
	default:
	}
	// Compare the persisted fields directly; unchanged frames allocate no JSON.
	bw := a.bottomFullWidth
	s := savedSession{
		RingSkipNotice:  a.ringSkipNotice,
		Repos:           make([]savedRepo, 0, len(a.tabs)),
		BottomFullWidth: &bw,
		RepoOrder:       a.repoOrder,
		Found:           slices.Clone(a.found),
		FoundFrom:       a.foundFrom,
	}
	for _, p := range a.recentProvenance {
		s.RecentProvenance = append(s.RecentProvenance, savedProvenance{p.agent, p.model})
	}
	for _, t := range a.tabs {
		f := ""
		if t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
			f = t.compareFiles[t.fileSel].path
		}
		commit := ""
		if t.inspecting != zeroHash && t.inspecting != worktreeHash {
			commit = hashHex(t.inspecting)
		}
		if t.restore != nil {
			commit, f = "", t.restore.file
			if t.restore.hash != zeroHash && t.restore.hash != worktreeHash {
				commit = hashHex(t.restore.hash)
			}
		}
		if t.pendingSession != nil {
			commit = t.pendingSession.Commit
			f = t.pendingSession.File
		}
		s.Repos = append(s.Repos, savedRepo{t.path, t.commitMsg, t.commitAmend, commit, f})
	}
	if t := a.activeTab(); t != nil {
		s.Active = t.path
	}
	bwEqual := (s.BottomFullWidth == nil && a.lastSession != nil && a.lastSession.BottomFullWidth == nil) ||
		(s.BottomFullWidth != nil && a.lastSession != nil && a.lastSession.BottomFullWidth != nil && *s.BottomFullWidth == *a.lastSession.BottomFullWidth)
	repoOrderEqual := (a.lastSession == nil && len(s.RepoOrder) == 0) ||
		(a.lastSession != nil && slices.Equal(s.RepoOrder, a.lastSession.RepoOrder))
	if a.lastSession != nil && s.Active == a.lastSession.Active && s.RingSkipNotice == a.lastSession.RingSkipNotice && slices.Equal(s.RecentProvenance, a.lastSession.RecentProvenance) && slices.Equal(s.Repos, a.lastSession.Repos) && bwEqual && repoOrderEqual && s.FoundFrom == a.lastSession.FoundFrom && slices.Equal(s.Found, a.lastSession.Found) {
		return
	}
	if a.sessionWrites == nil {
		p, err := configFile("session.json")
		if err != nil {
			a.setBanner("保存会话失败："+err.Error(), "")
			return
		}
		a.sessionWrites = make(chan savedSession, 1)
		a.sessionDone = make(chan struct{})
		a.sessionResults = make(chan error, 1)
		ch, done, results := a.sessionWrites, a.sessionDone, a.sessionResults
		go func() {
			defer close(done)
			writeSessionQueue(ch, results, func(s savedSession) error { return writeJSON(p, s) })
		}()
	}
	a.lastSession = &s
	select {
	case <-a.sessionWrites:
	default:
	}
	a.sessionWrites <- s
}

// Coalesce edits, retry failed writes, and flush the newest snapshot on shutdown.
func writeSessionQueue(ch <-chan savedSession, results chan error, write func(savedSession) error) {
	var pending *savedSession
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var tick <-chan time.Time
	save := func() bool {
		err := write(*pending)
		if err != nil {
			log.Printf("save session: %v", err)
		}
		select {
		case <-results:
		default:
		}
		select {
		case results <- err:
		default:
		}
		return err == nil
	}
	for {
		select {
		case s, ok := <-ch:
			if !ok {
				if pending != nil {
					save()
				}
				return
			}
			pending = &s
			timer.Reset(250 * time.Millisecond)
			tick = timer.C
		case <-tick:
			if save() {
				pending, tick = nil, nil
			} else {
				timer.Reset(time.Second)
			}
		}
	}
}

func (a *app) restoreSession() {
	s := loadSession()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var valid []savedRepo
		var found []string
		for _, path := range s.Found {
			if isGitWorkTree(path) {
				found = append(found, path)
			}
		}
		for _, r := range s.Repos {
			if root, e := repoRoot(ctx, r.Path); e == nil && root == r.Path {
				valid = append(valid, r)
			}
		}
		a.post(func() {
			a.ringSkipNotice = s.RingSkipNotice
			for _, p := range s.RecentProvenance {
				if len(a.recentProvenance) >= 8 {
					break
				}
				a.recentProvenance = append(a.recentProvenance, provenance{agent: p.Agent, model: p.Model})
			}
			if s.BottomFullWidth != nil {
				a.bottomFullWidth = *s.BottomFullWidth
			}
			if len(s.RepoOrder) > 0 {
				a.repoOrder = s.RepoOrder
			}
			if len(a.tabs) == 0 {
				if a.foundFrom == "" {
					a.found, a.foundFrom = found, s.FoundFrom
				}
				for _, r := range valid {
					a.openRoot(r.Path)
					t := a.tabByPath(r.Path)
					if t == nil {
						continue
					}
					t.commitMsg = r.Draft
					t.commitAmend = r.Amend
					t.pendingSession = &r
				}
				for i, t := range a.tabs {
					if t.path == s.Active {
						a.activate(i)
						break
					}
				}
			}
			a.sessionReady = true
		})
	}()
}
