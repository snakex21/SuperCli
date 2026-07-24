package webgui

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

type providerCallPerformance struct {
	Model       string    `json:"model,omitempty"`
	TTFTMS      int64     `json:"ttft_ms,omitempty"`
	DurationMS  int64     `json:"duration_ms,omitempty"`
	TokensIn    int       `json:"tokens_in,omitempty"`
	TokensOut   int       `json:"tokens_out,omitempty"`
	TokensPerS  float64   `json:"tokens_per_second,omitempty"`
	Failed      bool      `json:"failed"`
	Canceled    bool      `json:"canceled"`
	CompletedAt time.Time `json:"completed_at"`
}

type providerDiagnosticView struct {
	Name            string                   `json:"name"`
	Type            string                   `json:"type"`
	Endpoint        string                   `json:"endpoint"`
	Scope           string                   `json:"scope"`
	Server          string                   `json:"server"`
	Status          string                   `json:"status"`
	Disabled        bool                     `json:"disabled"`
	Active          bool                     `json:"active"`
	LatencyMS       int64                    `json:"latency_ms,omitempty"`
	Models          []string                 `json:"models"`
	SelectedModel   string                   `json:"selected_model,omitempty"`
	ContextWindow   int                      `json:"context_window,omitempty"`
	ToolUse         bool                     `json:"tool_use,omitempty"`
	CapabilityKnown bool                     `json:"capability_known"`
	LastCall        *providerCallPerformance `json:"last_call,omitempty"`
	Unavailable     []string                 `json:"unavailable"`
	Error           string                   `json:"error,omitempty"`
	CheckedAt       time.Time                `json:"checked_at"`
}

// handleProviderDiagnostics performs a passive models-endpoint probe. It never
// starts an inference, loads a model, or asks the remote machine for secrets.
// Hardware/queue fields are explicitly reported as unavailable instead of
// being guessed from response time.
func (s *Server) handleProviderDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	m := s.eng.providerManager()
	var found *config.ProviderConf
	for _, p := range m.Configured() {
		if p.Name == name {
			copy := p
			found = &copy
			break
		}
	}
	if found == nil {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	p := *found
	activeProvider, activeModel, _ := s.eng.RuntimeSelection()
	view := providerDiagnosticView{
		Name: p.Name, Type: p.Type, Endpoint: safeEndpoint(p.BaseURL),
		Scope: endpointScope(p.BaseURL), Server: endpointServer(p),
		Status: "checking", Disabled: p.Disabled, Active: activeProvider == p.Name,
		Models: []string{}, Unavailable: []string{"hardware", "backend_queue"},
		CheckedAt: time.Now().UTC(),
	}
	if perf, ok := s.eng.providerPerformance(p.Name); ok {
		view.LastCall = &perf
	}
	if p.Disabled {
		view.Status = "disabled"
		writeJSON(w, view)
		return
	}

	llm.InvalidateProviderModelCache(p.BaseURL)
	started := time.Now()
	var models []string
	var err error
	if p.Type == config.ProviderCodex {
		models = llm.RegisterCodexCatalog(s.eng.caps, p.Name)
	} else {
		// A diagnostic refresh is also the explicit capability refresh. The
		// manager reads the same passive model endpoints and records their
		// modality metadata, including native local-server capabilities.
		res := m.ScanProvider(p.Name, s.eng.caps)
		models, err = res.Models, res.Err
	}
	view.LatencyMS = time.Since(started).Milliseconds()
	view.CheckedAt = time.Now().UTC()
	if err != nil {
		view.Status = "offline"
		view.Error = safeDiagnosticError(err)
		writeJSON(w, view)
		return
	}
	visible := models[:0]
	for _, id := range models {
		if m.ModelVisible(p.Name, id) {
			visible = append(visible, id)
		}
	}
	models = visible
	sort.Strings(models)
	view.Models = models
	view.Status = "online"
	view.SelectedModel = p.Model
	if view.Active {
		view.SelectedModel = activeModel
	}
	if view.SelectedModel == "" && len(models) > 0 {
		view.SelectedModel = models[0]
	}
	if view.SelectedModel != "" {
		if info, ok := s.eng.caps.Get(view.SelectedModel); ok {
			view.ContextWindow, view.ToolUse, view.CapabilityKnown = info.ContextLength, info.ToolUse, true
		} else if info, ok := s.eng.caps.Get(p.Name + "/" + view.SelectedModel); ok {
			view.ContextWindow, view.ToolUse, view.CapabilityKnown = info.ContextLength, info.ToolUse, true
		}
	}
	writeJSON(w, view)
}

func safeEndpoint(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return strings.TrimRight(u.String(), "/")
}

func endpointScope(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "unknown"
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" {
		return "local"
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return "local"
		}
		if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return "lan"
		}
		return "remote"
	}
	if strings.HasSuffix(host, ".local") || !strings.Contains(host, ".") {
		return "lan"
	}
	return "remote"
}

func endpointServer(p config.ProviderConf) string {
	joined := strings.ToLower(p.Name + " " + p.BaseURL)
	u, _ := url.Parse(p.BaseURL)
	switch {
	case strings.Contains(joined, "ollama") || u.Port() == "11434":
		return "ollama"
	case strings.Contains(joined, "lmstudio") || strings.Contains(joined, "lm-studio") || u.Port() == "1234":
		return "lm-studio"
	case strings.Contains(joined, "llama"):
		return "llama.cpp"
	case p.Type == config.ProviderAnthropic:
		return "anthropic"
	case p.Type == config.ProviderCodex:
		return "codex"
	default:
		return "openai-compatible"
	}
}

func safeDiagnosticError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "timeout"
		}
		return "connection failed"
	}
	text := strings.ToLower(err.Error())
	for _, code := range []int{400, 401, 403, 404, 408, 409, 429, 500, 502, 503, 504} {
		if strings.Contains(text, "status "+strconv.Itoa(code)) || strings.Contains(text, "http "+strconv.Itoa(code)) {
			return "HTTP " + strconv.Itoa(code)
		}
	}
	if strings.Contains(text, "parse") || strings.Contains(text, "json") {
		return "invalid models response"
	}
	return "endpoint unavailable"
}
