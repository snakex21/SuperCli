package webgui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/ui/tui"
)

// TestCoreSurfaceParity pins the high-value local controls that must remain
// reachable from both interfaces. A missing /api path falls through to the
// embedded HTML file server, so JSON content (or a deliberate API error) is
// the route-presence contract.
func TestCoreSurfaceParity(t *testing.T) {
	actions := map[string]bool{}
	for _, id := range tui.ActionIDs() {
		actions[id] = true
	}
	required := []struct {
		name, action, route string
	}{
		{"models", "models", "/api/models"},
		{"providers", "providers", "/api/providers"},
		{"sessions", "sessions", "/api/sessions"},
		{"task queue", "queue", "/api/tasks"},
		{"goals", "goal", "/api/goal"},
		{"backup", "data", "/api/data/status"},
		{"doctor", "doctor", "/api/doctor"},
		{"workers", "workers", "/api/workers"},
		{"MCP", "mcp", "/api/mcp/servers"},
		{"undo", "undo", "/api/checkpoint"},
	}
	srv := newTestServer(t, false)
	h := srv.Handler()
	for _, capability := range required {
		t.Run(capability.name, func(t *testing.T) {
			if !actions[capability.action] {
				t.Fatalf("TUI action %q is missing", capability.action)
			}
			req := httptest.NewRequest(http.MethodGet, capability.route, nil)
			req.Host = "localhost"
			req.RemoteAddr = "127.0.0.1:43210"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			isJSON := strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json")
			if rec.Code == http.StatusNotFound || (rec.Code == http.StatusOK && !isJSON) {
				t.Fatalf("WebGUI route %q is missing", capability.route)
			}
		})
	}
}
