package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"supercli/internal/tools/interactive"
)

func TestMCPCallScopeAndInteractiveConfirmation(t *testing.T) {
	client := newTestClient(t)
	ch := make(chan interactive.AskRequest, 1)
	server := &Server{Name: "desktop", Config: ServerConfig{ConfirmCalls: true, AllowedTools: []string{"echo"}}, client: client, ConfirmationChannel: ch}
	if _, err := server.CallTool(context.Background(), "outside", []byte(`{}`)); err == nil {
		t.Fatal("scope escape")
	}
	done := make(chan error, 1)
	go func() {
		_, err := server.CallTool(context.Background(), "echo", []byte(`{"text":"click"}`))
		done <- err
	}()
	var request interactive.AskRequest
	select {
	case request = <-ch:
	case <-time.After(time.Second):
		t.Fatal("missing confirmation")
	}
	select {
	case <-done:
		t.Fatal("executed without approval")
	default:
	}
	request.Respond <- interactive.AskAnswer{Selected: []string{"Allow once"}}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() { _, err := server.CallTool(context.Background(), "echo", []byte(`{}`)); done <- err }()
	request = <-ch
	request.Respond <- interactive.AskAnswer{Cancelled: true}
	if err := <-done; err == nil {
		t.Fatal("executed rejected action")
	}
	server.ConfirmationChannel = nil
	if _, err := server.CallTool(context.Background(), "echo", json.RawMessage(`{}`)); err == nil {
		t.Fatal("headless action approved")
	}
}
