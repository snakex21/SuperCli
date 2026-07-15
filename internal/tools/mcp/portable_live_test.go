package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPortableWorkspaceLive verifies a real portable MCP package end to end:
// discovery stays process-free, while bridge search starts the selected server,
// completes the MCP handshake and retrieves its tool catalog. It is opt-in so
// ordinary unit tests never launch bundled or user-provided executables.
//
// Run with:
//
//	SUPERCLI_LIVE_MCP_DATA_DIR=/path/to/supercli-data go test -v ./internal/tools/mcp -run PortableWorkspaceLive
func TestPortableWorkspaceLive(t *testing.T) {
	dataDir := strings.TrimSpace(os.Getenv("SUPERCLI_LIVE_MCP_DATA_DIR"))
	if dataDir == "" {
		t.Skip("set SUPERCLI_LIVE_MCP_DATA_DIR to run a real portable MCP smoke test")
	}

	configs, packages, err := LoadWorkspace(dataDir, nil)
	if err != nil {
		t.Fatalf("load portable MCP workspace: %v", err)
	}
	if len(configs) == 0 {
		t.Fatalf("no available portable MCP packages in %s (discovered=%d)", dataDir, len(packages))
	}

	manager := NewManager(configs)
	t.Cleanup(manager.StopAll)
	bridge := NewBridge(manager).Spec()

	list, err := bridge.Fn(testCtx(t), json.RawMessage(`{"action":"list"}`))
	if err != nil || list.Err != nil {
		t.Fatalf("bridge list: result=%+v err=%v", list, err)
	}
	for _, status := range manager.Statuses() {
		if status.Running {
			t.Fatalf("metadata-only list eagerly started %q", status.Name)
		}
	}

	server := manager.Names()[0]
	searchRaw, _ := json.Marshal(map[string]any{"action": "search", "server": server})
	search, err := bridge.Fn(testCtx(t), searchRaw)
	if err != nil || search.Err != nil {
		t.Fatalf("bridge search %q: result=%+v err=%v", server, search, err)
	}
	var payload struct {
		Tools []struct {
			Server string `json:"server"`
			Name   string `json:"name"`
		} `json:"tools"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(search.Text), &payload); err != nil {
		t.Fatalf("decode bridge search: %v\n%s", err, search.Text)
	}
	if len(payload.Errors) > 0 || len(payload.Tools) == 0 {
		t.Fatalf("portable MCP returned no tools: errors=%v payload=%s", payload.Errors, search.Text)
	}
	status, ok := manager.Get(server)
	if !ok || !status.Status().Running {
		t.Fatalf("portable MCP %q did not remain running after handshake", server)
	}
	names := make([]string, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		names = append(names, tool.Name)
	}
	t.Logf("portable MCP %q ready: %d tools (%s)", server, len(names), strings.Join(names, ", "))

	// An optional URL promotes the handshake smoke test to a real tools/call
	// browser round-trip. Keeping it opt-in avoids making the normal suite
	// depend on Chromium or the public network.
	if target := strings.TrimSpace(os.Getenv("SUPERCLI_LIVE_MCP_URL")); target != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		navigateRaw, _ := json.Marshal(map[string]any{
			"action": "call", "server": server, "tool": "browser_navigate",
			"arguments": map[string]any{"url": target, "waitUntil": "domcontentloaded"},
		})
		navigate, callErr := bridge.Fn(ctx, navigateRaw)
		if callErr != nil || navigate.Err != nil {
			t.Fatalf("browser_navigate %s: result=%+v err=%v", target, navigate, callErr)
		}
		snapshotRaw, _ := json.Marshal(map[string]any{
			"action": "call", "server": server, "tool": "browser_snapshot", "arguments": map[string]any{},
		})
		snapshot, callErr := bridge.Fn(ctx, snapshotRaw)
		if callErr != nil || snapshot.Err != nil || strings.TrimSpace(snapshot.Text) == "" {
			t.Fatalf("browser_snapshot: result=%+v err=%v", snapshot, callErr)
		}
		t.Logf("portable MCP browser round-trip ready: %s", coreSummary(snapshot.Text, 240))
	}
}

func coreSummary(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
