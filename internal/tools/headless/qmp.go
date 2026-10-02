package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/core"
	"supercli/internal/tools/media"
	"supercli/internal/tools/sandbox"
)

const maxQMPMessage = 64 << 10

type qmpClient struct {
	conn         net.Conn
	reader       *bufio.Reader
	encoder      *json.Encoder
	next         int
	wantedEvent  string
	pendingEvent *qmpMessage
}
type qmpMessage struct {
	Greeting json.RawMessage               `json:"QMP"`
	Return   json.RawMessage               `json:"return"`
	Error    *struct{ Class, Desc string } `json:"error"`
	Event    string                        `json:"event"`
	ID       int                           `json:"id"`
	Data     json.RawMessage               `json:"data"`
}

func (q *qmpClient) read() (qmpMessage, error) {
	line, err := q.reader.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		return qmpMessage{}, fmt.Errorf("QMP message exceeds 64 KiB")
	}
	if err != nil {
		return qmpMessage{}, err
	}
	var message qmpMessage
	if err := json.Unmarshal(line, &message); err != nil {
		return message, fmt.Errorf("invalid QMP response: %w", err)
	}
	return message, nil
}
func (q *qmpClient) command(ctx context.Context, name string, args any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q.next++
	id := q.next
	request := map[string]any{"execute": name, "id": id}
	if args != nil {
		request["arguments"] = args
	}
	if err := q.encoder.Encode(request); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		message, err := q.read()
		if err != nil {
			return nil, err
		}
		if message.Event != "" {
			if message.Event == q.wantedEvent && q.pendingEvent == nil {
				q.pendingEvent = &message
			}
			continue
		}
		if message.ID != id {
			return nil, fmt.Errorf("QMP response ID mismatch")
		}
		if message.Error != nil {
			return nil, fmt.Errorf("QMP %s: %s: %s", name, message.Error.Class, message.Error.Desc)
		}
		if message.Return == nil {
			return nil, fmt.Errorf("QMP %s: missing result", name)
		}
		return message.Return, nil
	}
}
func dialQMP(ctx context.Context, endpoint, wantedEvent string) (*qmpClient, func(), error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, nil, fmt.Errorf("connect QMP: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, nil, err
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	closeFn := func() { stop(); _ = conn.Close() }
	q := &qmpClient{conn: conn, reader: bufio.NewReaderSize(conn, maxQMPMessage), encoder: json.NewEncoder(conn), wantedEvent: wantedEvent}
	greeting, err := q.read()
	if err != nil || len(greeting.Greeting) == 0 {
		closeFn()
		if err == nil {
			err = fmt.Errorf("endpoint did not provide a QMP greeting")
		}
		return nil, nil, err
	}
	if _, err := q.command(ctx, "qmp_capabilities", nil); err != nil {
		closeFn()
		return nil, nil, err
	}
	return q, closeFn, nil
}
func validateQMPParams(p params) error {
	switch p.Action {
	case "status", "screenshot":
	case "wait_event":
		if len(p.Event) == 0 || len(p.Event) > 64 {
			return fmt.Errorf("QMP wait_event requires event")
		}
		for _, c := range p.Event {
			if !(c == '_' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return fmt.Errorf("QMP event must be an uppercase event name")
			}
		}
	case "keys":
		if len(p.Keys) == 0 || len(p.Keys) > 16 {
			return fmt.Errorf("QMP keys requires 1..16 qcodes")
		}
		for _, key := range p.Keys {
			if len(key) == 0 || len(key) > 48 {
				return fmt.Errorf("invalid QMP qcode")
			}
			for _, c := range key {
				if !(c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
					return fmt.Errorf("invalid QMP qcode %q", key)
				}
			}
		}
	case "click":
		if p.X < 0 || p.Y < 0 || p.X > 32767 || p.Y > 32767 {
			return fmt.Errorf("QMP coordinates must be absolute 0..32767")
		}
		if p.Button != "" && p.Button != "left" && p.Button != "right" && p.Button != "middle" {
			return fmt.Errorf("invalid QMP button")
		}
	default:
		return fmt.Errorf("QMP action must be status, keys, click, screenshot or wait_event")
	}
	return nil
}
func (t *Tool) runQMP(ctx context.Context, p params) (core.Result, error) {
	if err := validateQMPParams(p); err != nil {
		return core.Result{}, err
	}
	q, closeFn, err := dialQMP(ctx, p.Endpoint, p.Event)
	if err != nil {
		return core.Result{}, err
	}
	defer closeFn()
	switch p.Action {
	case "status":
		value, err := q.command(ctx, "query-status", nil)
		if err != nil {
			return core.Result{}, err
		}
		return jsonResult(map[string]any{"protocol": "qmp", "status": value})
	case "wait_event":
		if q.pendingEvent != nil {
			return jsonResult(map[string]any{"protocol": "qmp", "event": q.pendingEvent.Event, "data": q.pendingEvent.Data})
		}
		for {
			if err := ctx.Err(); err != nil {
				return core.Result{}, err
			}
			message, err := q.read()
			if err != nil {
				return core.Result{}, err
			}
			if message.Event == p.Event {
				return jsonResult(map[string]any{"protocol": "qmp", "event": message.Event, "data": message.Data})
			}
		}
	case "keys":
		events := make([]any, 0, len(p.Keys)*2)
		for _, key := range p.Keys {
			events = append(events, map[string]any{"type": "key", "data": map[string]any{"down": true, "key": map[string]string{"type": "qcode", "data": key}}})
		}
		for i := len(p.Keys) - 1; i >= 0; i-- {
			events = append(events, map[string]any{"type": "key", "data": map[string]any{"down": false, "key": map[string]string{"type": "qcode", "data": p.Keys[i]}}})
		}
		if _, err := q.command(ctx, "input-send-event", map[string]any{"events": events}); err != nil {
			return core.Result{}, err
		}
	case "click":
		button := p.Button
		if button == "" {
			button = "left"
		}
		events := []any{
			map[string]any{"type": "abs", "data": map[string]any{"axis": "x", "value": p.X}},
			map[string]any{"type": "abs", "data": map[string]any{"axis": "y", "value": p.Y}},
			map[string]any{"type": "btn", "data": map[string]any{"button": button, "down": true}},
			map[string]any{"type": "btn", "data": map[string]any{"button": button, "down": false}},
		}
		if _, err := q.command(ctx, "input-send-event", map[string]any{"events": events}); err != nil {
			return core.Result{}, err
		}
	case "screenshot":
		return t.qmpScreenshot(ctx, q, p)
	}
	return jsonResult(map[string]any{"protocol": "qmp", "action": p.Action, "ok": true})
}
func (t *Tool) qmpScreenshot(ctx context.Context, q *qmpClient, p params) (core.Result, error) {
	if strings.TrimSpace(t.DataDir) == "" {
		return core.Result{}, fmt.Errorf("portable data directory is required")
	}
	base, err := filepath.Abs(t.DataDir)
	if err != nil {
		return core.Result{}, err
	}
	dir, err := sandbox.ResolveWithin(base, filepath.Join(".supercli", "headless-capture"))
	if err != nil {
		return core.Result{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return core.Result{}, err
	}
	f, err := os.CreateTemp(dir, "qmp-*.png")
	if err != nil {
		return core.Result{}, err
	}
	filename := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(filename)
		return core.Result{}, err
	}
	defer os.Remove(filename)
	if _, err := q.command(ctx, "screendump", map[string]any{"filename": filename, "format": "png"}); err != nil {
		return core.Result{}, fmt.Errorf("PNG screendump requires local QEMU 7.1+ with PNG support: %w", err)
	}
	input, err := os.Open(filename)
	if err != nil {
		return core.Result{}, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return core.Result{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > media.DefaultMaxScreenshotBytes {
		return core.Result{}, fmt.Errorf("QMP screenshot exceeds 16 MiB or is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(input, media.DefaultMaxScreenshotBytes+1))
	if err != nil {
		return core.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	return capturedResult(ctx, t.DataDir, "qmp", data, p.Attach, p.ImageDetail)
}
