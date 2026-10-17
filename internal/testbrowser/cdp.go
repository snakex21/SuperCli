package testbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    string `json:"data"`
	} `json:"error,omitempty"`
}

type cdpEvent struct {
	seq uint64
	cdpMessage
}

// One command at a time, but a permanent reader handles control frames and saves
// navigation events even when Chrome emits them BEFORE a command's response.
// Only load events are retained; console/network traffic cannot grow the queue.
type cdpClient struct {
	ws      *webSocket
	gate    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	err     error
	nextID  int64
	pending int64
	reply   chan cdpMessage
	seq     uint64
	events  []cdpEvent
	changed chan struct{}
}

func newCDP(ws *webSocket) *cdpClient {
	c := &cdpClient{ws: ws, gate: make(chan struct{}, 1), done: make(chan struct{}), changed: make(chan struct{})}
	c.gate <- struct{}{}
	go c.read()
	return c
}

func (c *cdpClient) shutdown(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		if err == nil {
			err = errors.New("CDP connection closed")
		}
		c.err = err
		close(c.done)
		_ = c.ws.conn.Close()
	}
}

func (c *cdpClient) read() {
	for {
		data, err := c.ws.readText()
		if err != nil {
			c.shutdown(err)
			return
		}
		var message cdpMessage
		if err := json.Unmarshal(data, &message); err != nil {
			c.shutdown(fmt.Errorf("invalid CDP message: %w", err))
			return
		}
		c.mu.Lock()
		if message.ID != 0 && message.ID == c.pending {
			select {
			case c.reply <- message:
			default:
			}
		}
		if message.Method == "Page.loadEventFired" || message.Method == "Page.lifecycleEvent" {
			var lifecycle struct{ Name string }
			_ = json.Unmarshal(message.Params, &lifecycle)
			if message.Method == "Page.loadEventFired" || lifecycle.Name == "load" {
				c.seq++
				if len(c.events) == 128 {
					copy(c.events, c.events[1:])
					c.events = c.events[:127]
				}
				c.events = append(c.events, cdpEvent{seq: c.seq, cdpMessage: message})
				close(c.changed)
				c.changed = make(chan struct{})
			}
		}
		c.mu.Unlock()
	}
}

func (c *cdpClient) call(ctx context.Context, session, method string, params, out any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.gate:
	}
	defer func() { c.gate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, err)
	}
	c.nextID++
	id := c.nextID
	reply := make(chan cdpMessage, 1)
	c.pending, c.reply = id, reply
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.pending, c.reply = 0, nil
		c.mu.Unlock()
	}()
	payload, err := json.Marshal(struct {
		ID        int64  `json:"id"`
		Method    string `json:"method"`
		SessionID string `json:"sessionId,omitempty"`
		Params    any    `json:"params,omitempty"`
	}{id, method, session, params})
	if err != nil {
		return err
	}
	if err := c.ws.writeFrame(ctx, 1, payload); err != nil {
		// A partial frame cannot be recovered by the next command.
		c.shutdown(err)
		return fmt.Errorf("%s: %w", method, err)
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", method, ctx.Err())
	case <-c.done:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, err)
	case response := <-reply:
		if response.Error != nil {
			return fmt.Errorf("%s: CDP error %d: %s %s", method, response.Error.Code, response.Error.Message, response.Error.Data)
		}
		if out != nil {
			if err := json.Unmarshal(response.Result, out); err != nil {
				return fmt.Errorf("%s result: %w", method, err)
			}
		}
		return nil
	}
}

func (c *cdpClient) eventMark() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seq
}

func (c *cdpClient) waitEvent(ctx context.Context, after uint64, match func(cdpMessage) bool) error {
	for {
		c.mu.Lock()
		for _, event := range c.events {
			if event.seq > after && match(event.cdpMessage) {
				c.mu.Unlock()
				return nil
			}
		}
		err, changed := c.err, c.changed
		c.mu.Unlock()
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
		case <-changed:
		}
	}
}
