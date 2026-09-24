package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DraftRecovery holds the latest unsent composer state. Writes are coalesced,
// bounded to one timer, and flushed on normal exit or Windows console close.
type DraftRecovery struct {
	mu      sync.Mutex
	path    string
	state   DraftSnapshot
	timer   *time.Timer
	dirty   bool
	lastErr error
}
type DraftSnapshot struct {
	SessionID   string   `json:"session_id"`
	Text        string   `json:"text"`
	Attachments []string `json:"attachments,omitempty"`
}

func OpenDraftRecovery(dataDir, home string) (*DraftRecovery, error) {
	if dataDir == "" {
		return nil, nil
	}
	root, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	d := &DraftRecovery{path: filepath.Join(dataDir, "tui-drafts", hex.EncodeToString(sum[:16])+".json")}
	info, err := os.Stat(d.path)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if info.Size() > 4<<20 {
		return d, fmt.Errorf("saved draft exceeds 4 MiB")
	}
	raw, err := os.ReadFile(d.path)
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal(raw, &d.state); err != nil {
		return d, err
	}
	return d, nil
}
func (d *DraftRecovery) Snapshot() DraftSnapshot {
	if d == nil {
		return DraftSnapshot{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.state
	s.Attachments = append([]string(nil), s.Attachments...)
	return s
}
func (d *DraftRecovery) Update(s DraftSnapshot) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if s.SessionID == d.state.SessionID && s.Text == d.state.Text && reflect.DeepEqual(s.Attachments, d.state.Attachments) {
		return
	}
	s.Attachments = append([]string(nil), s.Attachments...)
	d.state = s
	d.dirty = true
	if d.timer == nil {
		d.timer = time.AfterFunc(600*time.Millisecond, func() { _ = d.Flush() })
	}
}
func (d *DraftRecovery) Flush() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	if !d.dirty {
		return d.lastErr
	}
	d.lastErr = d.writeLocked()
	if d.lastErr == nil {
		d.dirty = false
	}
	return d.lastErr
}
func (d *DraftRecovery) writeLocked() error {
	if d.state.Text == "" && len(d.state.Attachments) == 0 {
		err := os.Remove(d.path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	raw, err := json.Marshal(d.state)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(d.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(d.path), ".draft-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, d.path)
}

// FlushDraft is also called by the app's window-close handler, which shares
// the thread-safe recovery object with the TUI.
func (m Model) FlushDraft() error { return m.drafts.Flush() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	n, ok := next.(Model)
	if !ok || n.drafts == nil {
		return next, cmd
	}
	switch msg.(type) {
	case tea.KeyMsg, clipboardImageMsg, nativeAttachmentsMsg, resumeLoadedMsg, runStartMsg, runEndMsg:
		// Keep a submitted draft durable until Run has accepted it.
		if n.submittingDraft == "" {
			n.drafts.Update(DraftSnapshot{SessionID: n.sessionID, Text: n.input.Value(), Attachments: n.pendingAttachments})
		}
	}
	if n.quitting {
		_ = n.drafts.Flush()
	}
	if err := n.drafts.Err(); err != nil && n.draftErrorShown != err.Error() {
		n.draftErrorShown = err.Error()
		n.setStatus(fmt.Sprintf(n.tr("Draft save failed: %v", "Nie udało się zapisać szkicu: %v"), err), false)
		cmd = tea.Batch(cmd, n.statusClearCmd())
	}
	return n, cmd
}

func (d *DraftRecovery) Err() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastErr
}
