package processsession

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"supercli/internal/tools/core"
	"supercli/internal/tools/media"
)

// screenshot targets only the native PID owned by this session. It does not
// inspect output, infer shell descendants, focus a window, or fall back to the
// foreground desktop. A minimized application may not maintain renderable pixels;
// the native window capturer reports that limitation rather than restoring it.
func (t *Tool) screenshot(ctx context.Context, p params) (core.Result, error) {
	failure := func(err error) (core.Result, error) { return core.Result{Err: err}, nil }
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	if p.ImageDetail != "" && p.ImageDetail != "auto" && p.ImageDetail != "original" {
		return failure(fmt.Errorf("process_session: image_detail must be auto or original"))
	}
	title := strings.TrimSpace(p.WindowTitle)
	if utf8.RuneCountInString(title) > 512 {
		return failure(fmt.Errorf("process_session: window_title exceeds 512 characters"))
	}
	id := strings.TrimSpace(p.ID)
	item, err := t.Manager.screenshotProcess(id)
	if err != nil {
		return failure(err)
	}
	pid, err := item.screenshotPID()
	if err != nil {
		return failure(err)
	}
	args, err := json.Marshal(struct {
		Source      string `json:"source"`
		ProcessID   int    `json:"process_id"`
		WindowTitle string `json:"window_title,omitempty"`
		Attach      bool   `json:"attach"`
		ImageDetail string `json:"image_detail,omitempty"`
	}{Source: "window", ProcessID: pid, WindowTitle: title, Attach: p.Attach, ImageDetail: p.ImageDetail})
	if err != nil {
		return failure(fmt.Errorf("process_session: screenshot args: %w", err))
	}
	capture := t.captureScreenshot
	if capture == nil {
		dataDir := t.DataDir
		if strings.TrimSpace(dataDir) == "" {
			dataDir = t.BaseDir // compatibility with existing explicit Tool literals
		}
		capture = media.NewSendScreenshot(dataDir, nil).Execute
	}
	result, captureErr := capture(ctx, args)
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	if _, err := item.screenshotPID(); err != nil {
		return failure(fmt.Errorf("process_session: owned process ended or stopped during screenshot: %w", err))
	}
	if owned, err := t.Manager.screenshotProcess(id); err != nil || owned != item {
		return failure(fmt.Errorf("process_session: screenshot ownership ended"))
	}
	// Keep native media metadata and image blocks at the top level. Re-marshalling
	// them as a process snapshot would hide both chat previews and model pixels.
	return result, captureErr
}

func (m *Manager) screenshotProcess(id string) (*process, error) {
	if id == "" {
		return nil, fmt.Errorf("process_session: id is required for screenshot")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("process_session: manager closed")
	}
	item := m.items[id]
	if item == nil {
		return nil, fmt.Errorf("process_session: unknown id %q", id)
	}
	return item, nil
}

func (p *process) screenshotPID() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status != "running" || p.stopRequested || p.exited.Load() {
		return 0, fmt.Errorf("process_session: %s is not an active owned process", p.id)
	}
	if p.pid <= 0 {
		return 0, fmt.Errorf("process_session: %s has no owned native PID", p.id)
	}
	return p.pid, nil
}
