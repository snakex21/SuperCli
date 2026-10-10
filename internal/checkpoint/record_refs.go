package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Parentless snapshots must each be reachable. A latest-only ref does not
// protect the before state or any older turn from Git maintenance.
func recordRefRoot(id string) string {
	hash := sha256.Sum256([]byte(id))
	return "refs/supercli/records/" + hex.EncodeToString(hash[:16])
}

// Reject reused metadata IDs and physical ref roots without another Git call.
// The native StoreIO gate protects cooperating managers; atomic Git create
// rejects orphaned before/after refs and races with other writers. Unknown
// children of this root remain untouched by both rollback and pair removal.
func (m *Manager) requireNewRecordLocked(r Record) error {
	root := recordRefRoot(r.ID)
	for _, existing := range m.records {
		if existing.ID == r.ID || recordRefRoot(existing.ID) == root {
			return fmt.Errorf("checkpoint record %q already exists", r.ID)
		}
	}
	return nil
}

func (m *Manager) pinRecordsLocked(ctx context.Context, records []Record) error {
	return m.pinRecordRefsLocked(ctx, records, false)
}

func (m *Manager) pinNewRecordLocked(ctx context.Context, r Record) error {
	return m.pinRecordRefsLocked(ctx, []Record{r}, true)
}

func (m *Manager) pinRecordRefsLocked(ctx context.Context, records []Record, create bool) error {
	var updates strings.Builder
	operation := "update"
	if create {
		operation = "create"
	}
	for _, r := range records {
		for _, side := range []struct{ name, commit string }{{"before", r.Before}, {"after", r.After}} {
			if side.commit == "" {
				continue // Metadata-only callers never created snapshots.
			}
			if len(side.commit) != 40 {
				return fmt.Errorf("invalid checkpoint commit for record %q", r.ID)
			}
			if _, err := hex.DecodeString(side.commit); err != nil {
				return fmt.Errorf("invalid checkpoint commit for record %q", r.ID)
			}
			fmt.Fprintf(&updates, "%s %s/%s %s\n", operation, recordRefRoot(r.ID), side.name, side.commit)
		}
	}
	return m.updateRecordRefsLocked(ctx, updates.String(), "retain records")
}

func (m *Manager) unpinRecordsLocked(ctx context.Context, records []Record) error {
	var updates strings.Builder
	for _, r := range records {
		if r.Before != "" {
			fmt.Fprintf(&updates, "delete %s/before\n", recordRefRoot(r.ID))
		}
		if r.After != "" {
			fmt.Fprintf(&updates, "delete %s/after\n", recordRefRoot(r.ID))
		}
	}
	return m.updateRecordRefsLocked(ctx, updates.String(), "release records")
}

// Roll back only this failed admission, never an existing record or active
// snapshot. A canceled request must not strand new roots. Expected OIDs and
// one Git transaction prevent partial deletion if an external writer changed
// either ref; failure deliberately preserves recovery roots and is reported.
func (m *Manager) rollbackNewRecordRefsLocked(r Record) error {
	var updates strings.Builder
	for _, side := range []struct{ name, commit string }{{"before", r.Before}, {"after", r.After}} {
		if side.commit != "" {
			fmt.Fprintf(&updates, "delete %s/%s %s\n", recordRefRoot(r.ID), side.name, side.commit)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.updateRecordRefsLocked(ctx, updates.String(), "rollback new record refs")
}

func (m *Manager) updateRecordRefsLocked(ctx context.Context, updates, operation string) error {
	if updates == "" {
		return nil
	}
	writes := int64(0)
	for _, line := range strings.Split(updates, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			if err := m.prepareRefUpdatesLocked(fields[1]); err != nil {
				return err
			}
			if fields[0] == "update" || fields[0] == "create" {
				writes++
			}
		}
	}
	if err := m.accountRefsLocked(writes); err != nil {
		return err
	}
	cmd := m.gitCommand(ctx, "-c", "core.fsync=reference", "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader("start\n" + updates + "prepare\ncommit\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("checkpoint %s: %w: %s", operation, err, strings.TrimSpace(string(out)))
	}
	return nil
}
