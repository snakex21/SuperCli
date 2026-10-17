package testbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCDPBuffersEventsBeforeReply(t *testing.T) {
	ws, server := socketPair(t)
	client := newCDP(ws)
	defer client.shutdown(nil)
	done := make(chan error, 1)
	go func() {
		_, data, err := readClientFrame(server)
		if err != nil {
			done <- err
			return
		}
		var command struct {
			ID                int64
			Method, SessionID string
		}
		if err = json.Unmarshal(data, &command); err == nil && (command.Method != "Page.navigate" || command.SessionID != "our-page") {
			err = errors.New("wrong flattened session command")
		}
		if err == nil {
			for _, event := range []map[string]any{
				{"method": "Page.loadEventFired", "sessionId": "other-page", "params": map[string]int{"timestamp": 1}},
				{"method": "Page.loadEventFired", "sessionId": "our-page", "params": map[string]int{"timestamp": 2}},
			} {
				wire, _ := json.Marshal(event)
				if _, err = server.Write(serverFrame(true, 1, wire)); err != nil {
					break
				}
			}
		}
		if err == nil {
			wire, _ := json.Marshal(map[string]any{"id": command.ID, "result": map[string]string{"loaderId": "loaded"}})
			_, err = server.Write(serverFrame(true, 1, wire))
		}
		done <- err
	}()
	mark := client.eventMark()
	var result struct{ LoaderID string }
	if err := client.call(t.Context(), "our-page", "Page.navigate", map[string]string{"url": "about:blank"}, &result); err != nil {
		t.Fatal(err)
	}
	if result.LoaderID != "loaded" {
		t.Fatalf("unexpected result %+v", result)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := client.waitEvent(ctx, mark, func(event cdpMessage) bool {
		return event.Method == "Page.loadEventFired" && event.SessionID == "our-page"
	}); err != nil {
		t.Fatalf("lost early loadEventFired: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCDPCancelAndProtocolError(t *testing.T) {
	ws, server := socketPair(t)
	client := newCDP(ws)
	defer client.shutdown(nil)
	done := make(chan error, 1)
	go func() {
		_, first, err := readClientFrame(server)
		if err != nil {
			done <- err
			return
		}
		var firstCommand, secondCommand struct{ ID int64 }
		_ = json.Unmarshal(first, &firstCommand)
		_, second, err := readClientFrame(server)
		if err == nil {
			_ = json.Unmarshal(second, &secondCommand)
			// The canceled command's late reply must not satisfy the new call.
			for _, reply := range []map[string]any{
				{"id": firstCommand.ID, "result": map[string]int{"value": 1}},
				{"id": secondCommand.ID, "error": map[string]any{"code": -32000, "message": "synthetic CDP failure", "data": "detail"}},
			} {
				wire, _ := json.Marshal(reply)
				if _, err = server.Write(serverFrame(true, 1, wire)); err != nil {
					break
				}
			}
		}
		done <- err
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := client.call(ctx, "", "Runtime.evaluate", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline: %v", err)
	}
	if err := client.call(t.Context(), "", "Runtime.evaluate", nil, nil); err == nil || !strings.Contains(err.Error(), "synthetic CDP failure detail") {
		t.Fatalf("CDP error was hidden: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCDPEventQueueBounded(t *testing.T) {
	ws, server := socketPair(t)
	client := newCDP(ws)
	defer client.shutdown(nil)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 200; i++ {
			wire, _ := json.Marshal(map[string]any{"method": "Page.loadEventFired", "sessionId": "page", "params": map[string]int{"timestamp": i}})
			if _, err := server.Write(serverFrame(true, 1, wire)); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := client.waitEvent(ctx, 0, func(event cdpMessage) bool {
		return strings.Contains(string(event.Params), "199")
	}); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	length := len(client.events)
	client.mu.Unlock()
	if length != 128 {
		t.Fatalf("event queue has %d entries", length)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
