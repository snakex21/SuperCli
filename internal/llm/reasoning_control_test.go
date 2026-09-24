package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func drainReasoningTest(t *testing.T, p Provider) {
	t.Helper()
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "Reply OK"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for d := range ch {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
	}
}

func TestReasoningDialChangesRequestsWithoutRebuildingProvider(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	for _, tc := range []struct {
		name, model, route string
		capability         bool
	}{
		{"responses_catalog", "custom-thinker", "responses", true},
		{"responses_no_catalog", "custom-thinker", "responses", false},
		{"responses_gpt6", "gpt-6-astra", "responses", false},
		{"local_chat_qwen", "qwen3.8-27b-uncensored", "chat", false},
		{"chat_gpt6", "gpt-6-astra", "chat", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				captured = nil
				if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
					t.Error(err)
				}
				if tc.route == "responses" {
					codexSSE(w, `{"type":"response.completed","response":{}}`)
				} else {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
				}
			}))
			defer srv.Close()
			caps := NewCapabilityRegistry()
			if tc.capability {
				caps.Register(ModelInfo{ID: tc.model, Reasoning: true, Source: SourceProvider})
			}
			var p Provider
			var err error
			if tc.route == "responses" {
				p, err = NewResponses(ResponsesConfig{BaseURL: srv.URL, Model: tc.model, Capabilities: caps})
			} else {
				p, err = NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: tc.model, Capabilities: caps})
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, level := range []string{"low", "high", "xhigh", "none", ""} {
				if err := SetReasoningEffort(level); err != nil {
					t.Fatal(err)
				}
				drainReasoningTest(t, p)
				var got string
				if tc.route == "responses" {
					r, _ := captured["reasoning"].(map[string]any)
					got, _ = r["effort"].(string)
					if level == "none" && r["summary"] != nil {
						t.Errorf("none must not request a summary: %v", r)
					}
				} else {
					got, _ = captured["reasoning_effort"].(string)
					if captured["reasoning"] != nil {
						t.Errorf("local/standard chat received a gateway object: %v", captured["reasoning"])
					}
				}
				if got != level {
					t.Errorf("dial=%q wire=%q", level, got)
				}
			}
			if calls != 5 {
				t.Fatalf("requests=%d, want 5 (no probes or retries)", calls)
			}
		})
	}
}

func TestResponsesReasoningEvidenceSurvivesNextTurnAndStaysScoped(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	for _, rejectAll := range []bool{false, true} {
		t.Run(map[bool]string{false: "adjust", true: "unsupported"}[rejectAll], func(t *testing.T) {
			var efforts []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]any
				_ = json.NewDecoder(r.Body).Decode(&req)
				reasoning, _ := req["reasoning"].(map[string]any)
				effort, _ := reasoning["effort"].(string)
				efforts = append(efforts, effort)
				if len(efforts) == 1 {
					w.WriteHeader(400)
					if rejectAll {
						_, _ = w.Write([]byte(`{"error":{"message":"reasoning is not supported","param":"reasoning"}}`))
					} else {
						_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value: 'xhigh'. Supported values are: 'low', 'medium', 'high'.","param":"reasoning.effort"}}`))
					}
					return
				}
				codexSSE(w, `{"type":"response.completed","response":{}}`)
			}))
			defer srv.Close()
			p, _ := NewResponses(ResponsesConfig{BaseURL: srv.URL, Model: "gpt-6-astra"})
			_ = SetReasoningEffort("xhigh")
			drainReasoningTest(t, p)
			drainReasoningTest(t, p)
			want := "xhigh|high|high"
			if rejectAll {
				want = "xhigh||"
			}
			if strings.Join(efforts, "|") != want {
				t.Fatalf("efforts=%v want %s", efforts, want)
			}
			other, _ := NewResponses(ResponsesConfig{BaseURL: "https://other.example/v1", Model: "gpt-6-astra"})
			if got := ProviderReasoningState(other).Effective; got != "xhigh" {
				t.Fatalf("evidence leaked: %q", got)
			}
			state := ProviderReasoningState(p)
			if rejectAll && (state.Supported || state.Effective != "") {
				t.Fatalf("rejected control still advertised: %+v", state)
			}
		})
	}
}

func TestZenReasoningDialPreservesSpecialRequestShape(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort("") })
	var bodies []map[string]any
	var headers []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		bodies = append(bodies, req)
		headers = append(headers, r.Header.Clone())
		codexSSE(w, `{"type":"response.completed","response":{}}`)
	}))
	defer srv.Close()
	p, err := NewResponses(ResponsesConfig{
		BaseURL: "https://opencode.ai/zen/v1", Model: "muse-spark-1.3-contributor-free",
		HTTPClient: &http.Client{Transport: responsesRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.URL.Scheme = "http"
			r.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			return http.DefaultTransport.RoundTrip(r)
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"low", "high", "xhigh", ""} {
		_ = SetReasoningEffort(level)
		drainReasoningTest(t, p)
		req := bodies[len(bodies)-1]
		r, _ := req["reasoning"].(map[string]any)
		effort, _ := r["effort"].(string)
		if effort != level {
			t.Errorf("dial=%q wire=%q", level, effort)
		}
		if req["instructions"] != nil || req["parallel_tool_calls"] != nil || req["max_output_tokens"] != float64(32000) {
			t.Fatalf("Zen dialect changed: %v", req)
		}
		if req["prompt_cache_key"] != headers[len(headers)-1].Get("X-OpenCode-Session") {
			t.Fatal("Zen session key mismatch")
		}
	}
}

func TestLocalNativeReasoningToggleMetadata(t *testing.T) {
	models, err := parseProviderModelInfos([]byte(`{"models":[{"key":"qwen","architecture":"qwen35","capabilities":{"reasoning":{"allowed_options":["off","on"],"default":"on"}}},{"key":"graded","capabilities":{"reasoning":{"allowed_options":["low","medium","high"]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !models[0].ReasoningToggleOnly || !models[0].Reasoning || !models[0].ReasoningKnown || models[1].ReasoningToggleOnly {
		t.Fatalf("models=%+v", models)
	}
	t.Cleanup(func() { _ = SetReasoningEffort("") })
	caps := NewCapabilityRegistry()
	caps.Register(models[0])
	p, _ := NewOpenAI(OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: "qwen", Capabilities: caps})
	for _, level := range []string{"low", "high", "none", ""} {
		_ = SetReasoningEffort(level)
		state := ProviderReasoningState(p)
		want := level
		if level == "low" || level == "high" {
			want = "on"
		}
		if !state.ToggleOnly || state.Effective != want || state.Configured != level {
			t.Fatalf("state=%+v want=%q", state, want)
		}
	}
}

func TestGPT6ChatReasoningUsesStandardField(t *testing.T) {
	p, err := NewOpenAI(OpenAIConfig{BaseURL: "https://api.example/v1", Model: "openai/gpt-6-astra"})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.reasoningFormat(); got != openAIReasoningEffort {
		t.Fatalf("format=%v", got)
	}
}

func TestLocalReasoningDiscoverySupportsLoopbackIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"key":"qwen","architecture":"qwen35","capabilities":{"reasoning":{"allowed_options":["off","on"]}}}]}`))
	}))
	defer srv.Close()
	models := ListLocalNativeModelInfos(context.Background(), srv.URL+"/v1", "")
	if len(models) != 1 || !models[0].ReasoningToggleOnly {
		t.Fatalf("models=%+v", models)
	}
}

func TestReasoningMenuReflectsSupportedLevels(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport() })
	p, _ := NewResponses(ResponsesConfig{BaseURL: "https://reasoning-menu.example/v1", Model: "custom"})
	SetReasoningEffortSupport(p.inner.reasoningKey(), []string{"low", "medium", "high"})
	_ = SetReasoningEffort("minimal")
	state := ProviderReasoningState(p)
	if strings.Join(state.Levels, "|") != "low|medium|high" || state.Selected != "low" || state.Configured != "minimal" {
		t.Fatalf("state=%+v", state)
	}
	setReasoningEffortUnsupported(p.inner.reasoningKey())
	state = ProviderReasoningState(p)
	if len(state.Levels) != 0 || state.Selected != "" || state.Supported {
		t.Fatalf("unsupported state=%+v", state)
	}
}
