package webgui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type queueAttachmentLimit struct {
	images int
	bytes  int64
}

// Provider limits live here, where the active provider is known. The existing
// transport's MaxFiles (currently 8) is an additional, stricter per-batch cap.
var queueAttachmentLimits = map[string]queueAttachmentLimit{
	"anthropic": {20, 30 << 20}, "openai": {50, 45 << 20},
	"responses": {50, 45 << 20}, "codex": {50, 45 << 20},
	"gemini": {100, 45 << 20}, "local": {5, 20 << 20},
}

func queueLimit(provider, baseURL string) queueAttachmentLimit {
	// Local servers commonly use an OpenAI-compatible provider type.
	if strings.Contains(baseURL, "localhost") || strings.Contains(baseURL, "127.0.0.1") || strings.Contains(baseURL, "[::1]") {
		return queueAttachmentLimits["local"]
	}
	if limit, ok := queueAttachmentLimits[strings.ToLower(provider)]; ok {
		return limit
	}
	return queueAttachmentLimits["local"]
}

func queueImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".avif", ".heic":
		return true
	}
	return false
}

// splitQueuedMessages preserves message and attachment order, including splitting
// a single attachment-heavy message. Never silently drop files or exceed a cap.
// More than three batches (or a single oversize file) leaves the queue unchanged.
func splitQueuedMessages(tasks []session.QueuedTask, label string, limit queueAttachmentLimit, size func(string) (int64, error)) ([]session.QueuedTask, error) {
	var out []session.QueuedTask
	current := session.QueuedTask{}
	images, bytes := 0, int64(0)
	lastMessage := -1
	flush := func() {
		if current.Prompt != "" {
			out = append(out, current)
		}
		current = session.QueuedTask{}
		images, bytes, lastMessage = 0, 0, -1
	}
	for i, task := range tasks {
		paragraph := strings.ReplaceAll(strings.ReplaceAll(label, "{index}", fmt.Sprint(i+1)), "{count}", fmt.Sprint(len(tasks))) + "\n" + task.Prompt
		addText := func() {
			if lastMessage == i {
				return
			}
			if current.Prompt != "" {
				current.Prompt += "\n\n"
			}
			current.Prompt += paragraph
			lastMessage = i
			if current.SessionID == "" {
				current.SessionID = task.SessionID
			}
		}
		if len(task.Attachments) == 0 {
			addText()
		}
		for _, path := range task.Attachments {
			n, err := size(path)
			if err != nil {
				return nil, err
			}
			if n > limit.bytes {
				return nil, fmt.Errorf("attachment exceeds safe batch size: %s", filepath.Base(path))
			}
			img := 0
			if queueImage(path) {
				img = 1
			}
			if len(current.Attachments) >= maxChatAttachments || images+img > limit.images || bytes+n > limit.bytes {
				flush()
			}
			addText()
			current.Attachments = append(current.Attachments, path)
			images += img
			bytes += n
		}
	}
	flush()
	if len(out) > 3 {
		return nil, fmt.Errorf("queue requires more than 3 safe batches; send fewer messages together")
	}
	return out, nil
}

func (s *Server) handleQueueMerge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs   []string `json:"ids"`
		Label string   `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) == 0 || len(body.Label) > 200 || !strings.Contains(body.Label, "{index}") || !strings.Contains(body.Label, "{count}") {
		http.Error(w, "invalid queue merge", 400)
		return
	}
	store, err := s.eng.sessionStore()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tasks, err := store.ListQueuedTasks(r.Context(), s.eng.Home())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Require the exact ordered snapshot. A stale phone must not overwrite edits,
	// steal claimed work, or merge the replacement rows a second time.
	if len(tasks) != len(body.IDs) {
		http.Error(w, "queue changed; retry", 409)
		return
	}
	for i, task := range tasks {
		if task.ID != body.IDs[i] || s.queueClaims[task.ID] != nil {
			http.Error(w, "queue changed or dispatching", 409)
			return
		}
	}
	s.eng.mu.RLock()
	cfg := s.eng.cfg
	s.eng.mu.RUnlock()
	batches, err := splitQueuedMessages(tasks, body.Label, queueLimit(cfg.Provider, cfg.BaseURL), func(path string) (int64, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.eng.Home(), path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() {
			return 0, fmt.Errorf("not a regular attachment")
		}
		return info.Size(), nil
	})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// Do not re-merge safe batches on the next automatic handoff.
	for i := range batches {
		batches[i].Merged = true
	}
	out, err := store.ReplaceQueuedTasks(r.Context(), s.eng.Home(), body.IDs, batches)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	s.eng.syncHub.queueChanged(s.eng.Home())
	writeJSON(w, out)
}

func (s *Server) queueMergeMode() string {
	tc, err := config.ResolveConfig(s.eng.DataDir(), s.eng.Home(), "")
	if err == nil && tc.QueueMerge == "auto" {
		return "auto"
	}
	return "off"
}
