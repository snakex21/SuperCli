package webgui

import "sync"

// liveDataKinds maps agent tools whose execution changes data shown by GUI
// panels to the sync "data_changed" kind that open views refresh.
var liveDataKinds = map[string]string{
	"goal":     "goal",
	"remember": "memory",
}

// toolDataTracker pairs tool_call and tool_result events of one run, so a
// data_changed frame is published only after the tool has finished writing.
type toolDataTracker struct {
	mu      sync.Mutex
	pending map[string]string
}

// observe returns the data kind changed by ev, or "" when nothing changed.
func (t *toolDataTracker) observe(ev wireEvent) string {
	switch ev.Type {
	case "tool_call":
		kind, ok := liveDataKinds[ev.Name]
		if !ok || ev.ID == "" {
			return ""
		}
		t.mu.Lock()
		if t.pending == nil {
			t.pending = make(map[string]string)
		}
		t.pending[ev.ID] = kind
		t.mu.Unlock()
	case "tool_result":
		t.mu.Lock()
		kind := t.pending[ev.ID]
		delete(t.pending, ev.ID)
		t.mu.Unlock()
		// A failed call may still have written part of a batch; a refetch of
		// unchanged data is a cheap no-op in the browser.
		return kind
	}
	return ""
}
