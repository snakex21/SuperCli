package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

type workerRequestTransport func(*http.Request) (*http.Response, error)

func (f workerRequestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// Capture the serialized first request, rather than inspecting the registry
// after a worker has had a chance to discover tools. Reproduces both transports
// from the coding evaluation without sending requests to an external model.
func TestCodeWorkerInitialRequestIncludesPatchSchema(t *testing.T) {
	for _, protocol := range []string{"chat", "zen-responses"} {
		for _, thin := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/thin=%t", protocol, thin), func(t *testing.T) {
				type wireFunction struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Parameters  struct {
						Type       string                     `json:"type"`
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"parameters"`
				}
				type wireTool struct {
					wireFunction
					Function wireFunction `json:"function"`
				}
				type capturedRequest struct {
					Model                 string     `json:"model"`
					Tools                 []wireTool `json:"tools"`
					Path, Client, Session string
				}
				captured := make(chan capturedRequest, 4)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var got capturedRequest
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					got.Path, got.Client, got.Session = r.URL.Path, r.Header.Get("X-OpenCode-Client"), r.Header.Get("X-OpenCode-Session")
					select {
					case captured <- got:
					default:
						http.Error(w, "unexpected extra request", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if protocol == "chat" {
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Ready.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Ready.\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n")
					}
				}))
				defer srv.Close()
				model := "qwen3.8-27b-uncensored"
				if protocol == "zen-responses" {
					model = "muse-spark-1.2-contributor-free"
				}
				caps := llm.NewCapabilityRegistry()
				caps.Register(llm.ModelInfo{ID: model, ToolUse: true, Source: llm.SourceProvider})
				var provider llm.Provider
				var err error
				if protocol == "chat" {
					provider, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL + "/v1", Model: model, Capabilities: caps})
				} else {
					// Keep the actual Zen encoding/gate/header branch, but route all HTTP
					// to the local fixture; the real service is never contacted.
					provider, err = llm.NewResponses(llm.ResponsesConfig{
						BaseURL: "https://opencode.ai/zen/v1", Model: model, Capabilities: caps,
						HTTPClient: &http.Client{Transport: workerRequestTransport(func(req *http.Request) (*http.Response, error) {
							clone := req.Clone(req.Context())
							clone.URL.Scheme, clone.URL.Host = "http", strings.TrimPrefix(srv.URL, "http://")
							clone.Host = clone.URL.Host
							return http.DefaultTransport.RoundTrip(clone)
						})},
					})
				}
				if err != nil {
					t.Fatal(err)
				}
				base := workerSchemaFixture(t)
				parent, err := NewLoop(LoopConfig{Provider: provider, Caps: caps, Registry: base, BaseDir: t.TempDir(), ThinTools: thin, StableToolset: true})
				if err != nil {
					t.Fatal(err)
				}
				specs := NewSubAgentRegistry()
				MustRegisterAll(specs, BuiltinSubAgents())
				task, err := NewAgentTool(specs, parent, base, provider, caps, NewLoop)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				ctx = llm.WithOpenCodeSession(ctx, "ses_0123456789abcdef0123456789")
				result, err := task.execute(ctx, json.RawMessage(`{"agent":"code","prompt":"Inspect your available tools and report ready."}`))
				if err != nil || result.Err != nil {
					t.Fatalf("worker: %v %v", err, result.Err)
				}
				if len(captured) != 1 {
					t.Fatalf("got %d HTTP requests, want one with no discovery round", len(captured))
				}
				got := <-captured
				if got.Model != model {
					t.Fatalf("model=%q", got.Model)
				}
				names := make(map[string]wireFunction)
				for _, tool := range got.Tools {
					fn := tool.wireFunction
					if protocol == "chat" {
						fn = tool.Function
					}
					names[fn.Name] = fn
				}
				for _, name := range []string{"patch_file", "create_file", "read_lines", "ctx_execute", "tool_search", "read_output"} {
					if names[name].Name != name {
						t.Errorf("%s absent from initial HTTP request", name)
					}
				}
				patch := names["patch_file"]
				if patch.Description == "" || patch.Parameters.Type != "object" {
					t.Fatal("patch definition has no description/object schema")
				}
				for _, field := range []string{"path", "old", "new", "changes", "expected_count", "base_hash"} {
					if !json.Valid(patch.Parameters.Properties[field]) {
						t.Errorf("patch argument %s absent from wire schema", field)
					}
				}
				if !strings.Contains(names["tool_search"].Description, "Use already available tools directly") {
					t.Fatal("discovery usage hint absent")
				}
				if protocol == "zen-responses" {
					if got.Path != "/zen/v1/responses" || got.Client != "cli" || got.Session != "ses_0123456789abcdef0123456789" {
						t.Fatalf("Zen route/headers changed: %s %s %s", got.Path, got.Client, got.Session)
					}
					if names["bash"].Name == "" || names["read"].Name == "" {
						t.Fatal("Zen compatibility tools missing")
					}
				} else if got.Path != "/v1/chat/completions" {
					t.Fatalf("chat path=%q", got.Path)
				}
				t.Logf("first HTTP request: model=%s thin=%t tools=%d patch_fields=%d; no discovery needed", got.Model, thin, len(got.Tools), len(patch.Parameters.Properties))
			})
		}
	}
}
