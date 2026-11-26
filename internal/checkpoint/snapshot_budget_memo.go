package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Remembered snapshot budget (after 1.0.41). In a 224 GB workspace every
// ctx_execute spent up to WholeSnapshotBudget (8 s) on a whole-workspace
// snapshot before giving up, and each command got its own warning (1,170 in
// one conversation's worker histories). The first over-budget capture is now
// remembered for the workspace, shared by every Manager of it (each GUI run
// opens its own) and saved next to its checkpoint store: later commands skip
// the attempt until SnapshotBudgetRecheck has passed (also across restarts),
// and a turn shows the notice once. ForgetSnapshotBudget re-checks at once.
var SnapshotBudgetRecheck = 6 * time.Hour

const snapshotBudgetFile = "snapshot-budget.json"

type budgetEntry struct {
	Until time.Time            `json:"until"`
	Cause string               `json:"cause"`
	Roots map[string]time.Time `json:"roots,omitempty"` // command workdirs also over budget
}

var budgetMemo = struct {
	sync.Mutex
	byStore map[string]*budgetEntry // checkpoint store directory -> entry (nil: none)
}{byStore: map[string]*budgetEntry{}}

func (m *Manager) budgetStore() string { return filepath.Dir(m.repo) }

// budgetEntryLocked returns the workspace's entry, loading the saved one the
// first time. budgetMemo is locked.
func (m *Manager) budgetEntryLocked() *budgetEntry {
	store := m.budgetStore()
	if entry, ok := budgetMemo.byStore[store]; ok {
		return entry
	}
	var entry *budgetEntry
	if raw, err := os.ReadFile(filepath.Join(store, snapshotBudgetFile)); err == nil {
		var saved budgetEntry
		if json.Unmarshal(raw, &saved) == nil {
			entry = &saved
		}
	}
	budgetMemo.byStore[store] = entry
	return entry
}

func (m *Manager) saveBudgetLocked(entry *budgetEntry) {
	store := m.budgetStore()
	budgetMemo.byStore[store] = entry
	path := filepath.Join(store, snapshotBudgetFile)
	if entry == nil {
		_ = os.Remove(path)
		return
	}
	if raw, err := json.Marshal(entry); err == nil {
		tmp := path + ".tmp"
		if os.WriteFile(tmp, raw, 0o600) == nil {
			_ = os.Rename(tmp, path)
		}
	}
}

// overBudget returns the remembered limit while it is still fresh.
func (m *Manager) overBudget() error {
	budgetMemo.Lock()
	defer budgetMemo.Unlock()
	entry := m.budgetEntryLocked()
	if entry == nil || entry.Cause == "" || !time.Now().Before(entry.Until) {
		return nil
	}
	return fmt.Errorf("%w%s", ErrSnapshotLimit, strings.TrimPrefix(entry.Cause, ErrSnapshotLimit.Error()))
}

func (m *Manager) rememberOverBudget(cause error) {
	budgetMemo.Lock()
	defer budgetMemo.Unlock()
	entry := m.budgetEntryLocked()
	next := budgetEntry{}
	if entry != nil {
		next = *entry
	}
	next.Cause, next.Until = cause.Error(), time.Now().Add(SnapshotBudgetRecheck)
	m.saveBudgetLocked(&next)
}

func (m *Manager) rootOverBudget(root string) bool {
	budgetMemo.Lock()
	defer budgetMemo.Unlock()
	entry := m.budgetEntryLocked()
	if entry == nil {
		return false
	}
	until, ok := entry.Roots[root]
	return ok && time.Now().Before(until)
}

func (m *Manager) rememberRootOverBudget(root string) {
	budgetMemo.Lock()
	defer budgetMemo.Unlock()
	entry := m.budgetEntryLocked()
	next := budgetEntry{}
	if entry != nil {
		next = *entry
	}
	roots := map[string]time.Time{}
	for k, v := range next.Roots {
		roots[k] = v
	}
	roots[root] = time.Now().Add(SnapshotBudgetRecheck)
	next.Roots = roots
	m.saveBudgetLocked(&next)
}

// ForgetSnapshotBudget drops the remembered limit, so the next command tries
// a full snapshot again (for example after the user removed large files).
func (m *Manager) ForgetSnapshotBudget() {
	budgetMemo.Lock()
	defer budgetMemo.Unlock()
	m.saveBudgetLocked(nil)
}

// onceNotice returns notice the first time a turn would show it, then "".
// The caller holds t.mu.
func (t *Turn) onceNotice(notice string) string {
	if notice == "" {
		return ""
	}
	if t.shownNotices == nil {
		t.shownNotices = map[string]bool{}
	}
	if t.shownNotices[notice] {
		return ""
	}
	t.shownNotices[notice] = true
	return notice
}
