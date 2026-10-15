package mcp

import (
	"context"
	"testing"
)

func TestMCPCallScopeWithoutConfirmation(t *testing.T) {
	client := newTestClient(t)
	server := &Server{Name: "desktop", Config: ServerConfig{AllowedTools: []string{"echo"}}, client: client}
	if _, err := server.CallTool(context.Background(), "outside", []byte(`{}`)); err == nil {
		t.Fatal("scope escape")
	}
	for i := 0; i < 3; i++ {
		if _, err := server.CallTool(context.Background(), "echo", []byte(`{"text":"click"}`)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}
