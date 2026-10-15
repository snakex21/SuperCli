package processsession

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"supercli/internal/system/windowautomation"
	"supercli/internal/tools/core"
	"supercli/internal/tools/media"
)

// openWindowTarget accepts either a retained owned session or an external PID.
// No confirmation is asked: these operations use accessibility patterns only and
// never take the user's mouse or keyboard. The handle pins the process identity
// for the whole operation, so a reused PID is never targeted.
func (t *Tool) openWindowTarget(ctx context.Context, p params) (*windowautomation.Process, func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if utf8.RuneCountInString(p.WindowTitle) > 512 || strings.ContainsRune(p.WindowTitle, 0) {
		return nil, nil, fmt.Errorf("invalid window_title")
	}
	if p.ID != "" && p.ProcessID != 0 {
		return nil, nil, fmt.Errorf("choose an owned session id or an external process_id")
	}
	pid, birth := p.ProcessID, uint64(0)
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		t.Manager.mu.Lock()
		closed := t.Manager.closed
		t.Manager.mu.Unlock()
		if closed {
			return fmt.Errorf("process_session: manager closed")
		}
		return nil
	}
	if err := check(); err != nil {
		return nil, nil, err
	}
	if p.ID != "" {
		item, err := t.Manager.screenshotProcess(strings.TrimSpace(p.ID))
		if err != nil {
			return nil, nil, err
		}
		pid, err = item.screenshotPID()
		if err != nil {
			return nil, nil, err
		}
		birth = item.windowBirth
		if birth == 0 {
			return nil, nil, fmt.Errorf("owned native process identity unavailable; cannot authorize window control")
		}
		check = func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			current, err := t.Manager.screenshotProcess(strings.TrimSpace(p.ID))
			if err != nil || current != item {
				return fmt.Errorf("window session ownership ended")
			}
			_, err = item.screenshotPID()
			return err
		}
	}
	process, err := windowautomation.Open(pid, birth)
	if err != nil {
		return nil, nil, err
	}
	if err := check(); err != nil {
		process.Close()
		return nil, nil, err
	}
	if err := process.Alive(); err != nil {
		process.Close()
		return nil, nil, err
	}
	return process, check, nil
}

func (t *Tool) windowAction(ctx context.Context, p params, raw json.RawMessage) (core.Result, error) {
	fail := func(err error) (core.Result, error) {
		return core.Result{Err: fmt.Errorf("process_session: %w", err)}, nil
	}
	// Unknown model-supplied flags such as approved/use_mouse must not be
	// mistaken for authorization or cause a fallback to physical input.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var checked params
	if err := decoder.Decode(&checked); err != nil {
		return fail(err)
	}
	if p.Action == "type" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return fail(err)
		}
		text, present := fields["text"]
		if !present || bytes.Equal(bytes.TrimSpace(text), []byte("null")) {
			return fail(fmt.Errorf("type requires an explicit text string; use text:\"\" only to intentionally clear the field"))
		}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fail(fmt.Errorf("invalid trailing arguments"))
	}
	r := windowautomation.Request{Identity: windowautomation.Identity{PID: 1}, Action: p.Action, WindowTitle: strings.TrimSpace(p.WindowTitle), WindowID: p.WindowID, ElementID: p.ElementID, Text: p.Text}
	if p.Input != "" {
		return fail(fmt.Errorf("window type uses text, not console input"))
	}
	if err := windowautomation.Validate(r); err != nil {
		return fail(err)
	}
	process, check, err := t.openWindowTarget(ctx, p)
	if err != nil {
		return fail(err)
	}
	defer process.Close()
	r.Identity = process.Identity
	// A cancellable gate serializes actions/observations from concurrent worker
	// calls.
	t.windowMu.Lock()
	if t.windowGate == nil {
		t.windowGate = make(chan struct{}, 1)
	}
	gate := t.windowGate
	t.windowMu.Unlock()
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	defer func() { <-gate }()
	if err := check(); err != nil {
		return fail(err)
	}
	run := t.runWindow
	if run == nil {
		run = windowautomation.Execute
	}
	result, err := run(ctx, process, r)
	if err != nil {
		return fail(err)
	}
	if err := check(); err != nil {
		return fail(fmt.Errorf("window operation finished after ownership ended; an action may already have taken effect: %w", err))
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fail(err)
	}
	return core.Result{Text: string(data)}, nil
}

func (t *Tool) externalScreenshot(ctx context.Context, p params) (core.Result, error) {
	if p.ImageDetail != "" && p.ImageDetail != "auto" && p.ImageDetail != "original" {
		return core.Result{Err: fmt.Errorf("invalid image_detail")}, nil
	}
	if err := windowautomation.Validate(windowautomation.Request{Identity: windowautomation.Identity{PID: p.ProcessID}, Action: "inspect_window", WindowTitle: p.WindowTitle, WindowID: p.WindowID}); err != nil {
		return core.Result{Err: err}, nil
	}
	process, check, err := t.openWindowTarget(ctx, p)
	if err != nil {
		return core.Result{Err: err}, nil
	}
	defer process.Close()
	raw, _ := json.Marshal(map[string]any{"source": "window", "process_id": process.PID, "window_title": p.WindowTitle, "window_id": p.WindowID, "attach": p.Attach, "image_detail": p.ImageDetail})
	capture := t.captureScreenshot
	if capture == nil {
		capture = media.NewSendScreenshot(t.DataDir, nil).Execute
	}
	result, err := capture(ctx, raw)
	if checkErr := check(); checkErr != nil {
		return core.Result{Err: checkErr}, nil
	}
	if aliveErr := process.Alive(); aliveErr != nil {
		return core.Result{Err: aliveErr}, nil
	}
	return result, err
}
