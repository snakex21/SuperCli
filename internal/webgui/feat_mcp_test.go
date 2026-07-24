package webgui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/system/config"
)

func TestPortableMCPIsDiscoveredButNotStarted(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	pkgDir := filepath.Join(dataDir, "mcp", "scene-tools")
	if err := os.MkdirAll(filepath.Join(pkgDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(pkgDir, "bin", "scene-mcp")
	if err := os.WriteFile(command, []byte("not executed"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema = 1
name = "scene-tools"
version = "2.0"
description = "Portable scene tools"
command = "bin/scene-mcp"
tags = ["scene", "3d"]
`
	if err := os.WriteFile(filepath.Join(pkgDir, "manifest.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	manager := eng.mcpRuntime()
	if manager == nil || len(manager.Names()) != 1 || manager.Names()[0] != "scene-tools" {
		t.Fatalf("unexpected MCP runtime: %#v", manager)
	}
	if manager.Statuses()[0].Running {
		t.Fatal("portable MCP process started during discovery")
	}

	rec := httptest.NewRecorder()
	NewServer(eng, false).handleMcpServers(rec, httptest.NewRequest(http.MethodGet, "/api/mcp/servers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Lazy     bool             `json:"lazy"`
		Packages []mcpPackageView `json:"packages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Lazy || len(response.Packages) != 1 || !response.Packages[0].Available {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Packages[0].Running {
		t.Fatal("status endpoint started the package")
	}
}

func TestMCPJSONConfigReplacesExplicitServers(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	server := NewServer(eng, false)

	body := []byte(`{"servers":{"docs":{"command":"mcp-docs","args":["--stdio"],"env":{"MODE":"safe"}}}}`)
	rec := httptest.NewRecorder()
	server.handleMcpConfig(rec, httptest.NewRequest(http.MethodPut, "/api/mcp/config", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("put status %d: %s", rec.Code, rec.Body.String())
	}
	tc, err := config.LoadToml(filepath.Join(dataDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tc.Mcp.Servers) != 1 || tc.Mcp.Servers["docs"].Command != "mcp-docs" {
		t.Fatalf("saved servers = %#v", tc.Mcp.Servers)
	}

	rec = httptest.NewRecorder()
	server.handleMcpConfig(rec, httptest.NewRequest(http.MethodGet, "/api/mcp/config", nil))
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"docs"`)) {
		t.Fatalf("get status %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.handleMcpConfig(rec, httptest.NewRequest(http.MethodPut, "/api/mcp/config", bytes.NewReader([]byte(`{"servers":{}}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status %d: %s", rec.Code, rec.Body.String())
	}
	tc, err = config.LoadToml(filepath.Join(dataDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tc.Mcp.Servers) != 0 {
		t.Fatalf("servers were not replaced: %#v", tc.Mcp.Servers)
	}
}
