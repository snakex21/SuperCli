package mcp

import (
	"context"
	"errors"
	"testing"
)

func TestMockServer_Name(t *testing.T) {
	m := NewMockServer("semble")
	if m.Name() != "semble" {
		t.Errorf("Name = %q", m.Name())
	}
}

func TestMockServer_StartStop(t *testing.T) {
	m := NewMockServer("semble")
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !m.Started() {
		t.Error("Started = false after Start")
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !m.Stopped() {
		t.Error("Stopped = false after Stop")
	}
}

func TestMockServer_Tools_ReturnsRegistered(t *testing.T) {
	m := NewMockServer("semble")
	m.SetTool(ToolDef{Name: "search", Description: "search the code"}, func(ctx context.Context, args []byte) (Result, error) {
		return Result{Text: "ok"}, nil
	})
	tools := m.Tools()
	if len(tools) != 1 {
		t.Fatalf("len = %d", len(tools))
	}
	if tools[0].Name != "search" {
		t.Errorf("Name = %q", tools[0].Name)
	}
}

func TestMockServer_CallTool_RoutesToHandler(t *testing.T) {
	m := NewMockServer("semble")
	called := false
	m.SetTool(ToolDef{Name: "ping"}, func(ctx context.Context, args []byte) (Result, error) {
		called = true
		return Result{Text: "pong"}, nil
	})
	res, err := m.CallTool(context.Background(), "ping", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Text != "pong" {
		t.Errorf("Text = %q", res.Text)
	}
	if !called {
		t.Error("handler not called")
	}
}

func TestMockServer_CallTool_Unknown(t *testing.T) {
	m := NewMockServer("semble")
	_, err := m.CallTool(context.Background(), "missing", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	var perr *ProtocolError
	if !errors.As(err, &perr) {
		t.Errorf("expected *ProtocolError, got %T", err)
	}
}

func TestMockServer_CallTool_HandlerError(t *testing.T) {
	m := NewMockServer("semble")
	m.SetTool(ToolDef{Name: "fail"}, func(ctx context.Context, args []byte) (Result, error) {
		return Result{}, errors.New("boom")
	})
	_, err := m.CallTool(context.Background(), "fail", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMockServer_Stop_Idempotent(t *testing.T) {
	m := NewMockServer("semble")
	m.Start(context.Background())
	if err := m.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestProtocolError_Error(t *testing.T) {
	e := &ProtocolError{Code: -32600, Message: "bad"}
	if got := e.Error(); got != "mcp: protocol error -32600: bad" {
		t.Errorf("Error() = %q", got)
	}
}
