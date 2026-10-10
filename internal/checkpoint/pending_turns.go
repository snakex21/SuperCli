package checkpoint

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrRecoveryRequired is scoped to this Manager's workspace. Native owner
// absence never authorizes discarding snapshots or manufacturing an after
// snapshot from the current (possibly manually edited) workspace.
var ErrRecoveryRequired = fmt.Errorf("%w: interrupted or unknown checkpoint requires recovery", ErrActiveTurn)

// PendingTurn describes owner/ref metadata only. OwnerKnown means a lease file
// was found and probed. Live is native lock contention, never a PID/age guess.
// Unknown legacy refs are retained even if no associated lease file exists.
type PendingTurn struct {
	ID         string            `json:"id"`
	Live       bool              `json:"live"`
	OwnerKnown bool              `json:"owner_known"`
	Before     string            `json:"before,omitempty"`
	After      string            `json:"after,omitempty"`
	OtherRefs  map[string]string `json:"other_refs,omitempty"`
}

func (p PendingTurn) hasSnapshots() bool {
	return p.Before != "" || p.After != "" || len(p.OtherRefs) != 0
}

// PendingTurns makes one store transaction and one nonblocking native probe
// per existing owner file. It changes no refs, records or workspace files.
func (m *Manager) PendingTurns(ctx context.Context) (pending []PendingTurn, err error) {
	unlock, err := m.lockStore(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return m.pendingTurnsLocked(ctx)
}

func (m *Manager) pendingTurnsLocked(ctx context.Context) ([]PendingTurn, error) {
	owners := map[string]*PendingTurn{}
	owner := func(id string) *PendingTurn {
		if owners[id] == nil {
			owners[id] = &PendingTurn{ID: id}
		}
		return owners[id]
	}
	dir := m.turnLeaseDir()
	if err := checkTurnLeaseDir(dir); err == nil {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id := strings.TrimSuffix(entry.Name(), ".lock")
			if entry.Name() != id+".lock" || !validTurnLeaseID(id) {
				return nil, errors.New("checkpoint lease directory has an unknown entry")
			}
			path := filepath.Join(dir, entry.Name())
			if err := checkCheckpointStoreLockPath(path); err != nil {
				return nil, err
			}
			unlock, err := checkpointStoreLock(path)
			if errors.Is(err, ErrStoreBusy) {
				owner(id).Live, owner(id).OwnerKnown = true, true
				continue
			}
			if err != nil {
				return nil, err
			}
			if err := unlock(); err != nil {
				return nil, err
			}
			owner(id).OwnerKnown = true
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(m.repo, "HEAD")); err == nil {
		refs, err := m.git(ctx, "for-each-ref", "--format=%(refname) %(objectname)", "refs/supercli/active/")
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(strings.TrimSpace(refs), "\n") {
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) != 2 {
				return nil, errors.New("checkpoint active ref metadata is malformed")
			}
			name := strings.TrimPrefix(parts[0], "refs/supercli/active/")
			id, side, _ := strings.Cut(name, "/")
			pending := owner(id)
			if validTurnLeaseID(id) && side == "before" {
				pending.Before = parts[1]
			} else if validTurnLeaseID(id) && side == "after" {
				pending.After = parts[1]
			} else {
				if pending.OtherRefs == nil {
					pending.OtherRefs = map[string]string{}
				}
				pending.OtherRefs[parts[0]] = parts[1]
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	result := make([]PendingTurn, 0, len(owners))
	for _, pending := range owners {
		result = append(result, *pending)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func validTurnLeaseID(id string) bool {
	if len(id) != 32 || id != strings.ToLower(id) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
