package checkpoint

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Rewinding one conversation while another runs (after 1.0.41). A rewind of
// an idle conversation was refused whenever any other conversation in the
// same project had a live turn ("you cannot go back because the other one is
// running"), even a conversation that changed no files. The working tree is
// shared, so only the files matter: a file rewind is refused only when the
// files it would restore overlap what another live turn has captured, and
// then with the list, so the user can rewind only the conversation or rewind
// anyway.

// RewindOverlap is one file another live turn holds.
type RewindOverlap struct {
	File string `json:"file"`
	// Session is the conversation that holds it ("" when another SuperCli
	// process holds a turn whose files are unknown).
	Session string `json:"session"`
}

// RewindOverlapError lists the overlapping files of a refused file rewind.
type RewindOverlapError struct {
	Overlaps []RewindOverlap
}

func (e *RewindOverlapError) Error() string {
	files := make([]string, 0, len(e.Overlaps))
	for _, o := range e.Overlaps {
		files = append(files, o.File)
	}
	return fmt.Sprintf("%v: files used by another running conversation: %s", ErrActiveTurn, strings.Join(files, ", "))
}

func (e *RewindOverlapError) Unwrap() error { return ErrActiveTurn }

// turnHolder is what another conversation's live turn has captured.
type turnHolder struct {
	session string
	whole   bool // whole workspace, or not readable now: overlaps every file
	roots   []string
}

// liveTurnHolders snapshots the in-process pending turns' captured scope.
// Called before the store lock (Turn.mu is never taken under it); a turn
// busy right now counts as holding everything.
func (m *Manager) liveTurnHolders(sessionID string) (holders []turnHolder, own bool, count int) {
	m.pendingMu.Lock()
	turns := make([]*Turn, 0, len(m.pending))
	for t := range m.pending {
		turns = append(turns, t)
	}
	m.pendingMu.Unlock()
	for _, t := range turns {
		count++
		if !t.mu.TryLock() {
			if t.sessionID == sessionID {
				own = true
				continue
			}
			holders = append(holders, turnHolder{session: t.sessionID, whole: true})
			continue
		}
		h := turnHolder{session: t.sessionID, whole: t.wholeWorkspace, roots: append([]string(nil), t.scopeRoots...)}
		touched := t.touched
		t.mu.Unlock()
		if !touched {
			continue
		}
		if h.session == sessionID {
			own = true
			continue
		}
		holders = append(holders, h)
	}
	return holders, own, count
}

// requireNoConflictingTurnLocked replaces the blanket "no live turn" rule for
// a session's file rewind: its own live turn and an interrupted turn needing
// recovery still block; other sessions' live turns block only when the files
// to restore overlap what they captured (unless force).
func (m *Manager) requireNoConflictingTurnLocked(ctx context.Context, files []string, holders []turnHolder, own bool, inProcess int, force bool) error {
	pending, err := m.pendingTurnsLocked(ctx)
	if err != nil {
		return err
	}
	live := 0
	for _, turn := range pending {
		if turn.Live {
			live++
		} else if turn.hasSnapshots() {
			return fmt.Errorf("%w: owner %s", ErrRecoveryRequired, turn.ID)
		}
	}
	if live == 0 {
		return nil
	}
	if own {
		return ErrActiveTurn // this conversation's own run
	}
	if len(files) == 0 || force {
		return nil
	}
	unknownLive := live > inProcess // a turn of another SuperCli process
	var overlaps []RewindOverlap
	for _, f := range files {
		f = strings.TrimPrefix(strings.ReplaceAll(f, "\\", "/"), "./")
		if unknownLive {
			overlaps = append(overlaps, RewindOverlap{File: f})
			continue
		}
		for _, h := range holders {
			if h.whole || scopeContains(h.roots, f) || rootsUnder(h.roots, f) {
				overlaps = append(overlaps, RewindOverlap{File: f, Session: h.session})
				break
			}
		}
	}
	if len(overlaps) == 0 {
		return nil
	}
	sort.Slice(overlaps, func(i, j int) bool { return overlaps[i].File < overlaps[j].File })
	return &RewindOverlapError{Overlaps: overlaps}
}

// rootsUnder reports a captured root inside directory dir.
func rootsUnder(roots []string, dir string) bool {
	for _, r := range roots {
		if pathPrefix(r, dir+"/") {
			return true
		}
	}
	return false
}
