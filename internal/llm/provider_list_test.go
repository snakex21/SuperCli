package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeuristicCapabilities_Vision(t *testing.T) {
	cases := []string{"gpt-4-vision-preview", "llama-3.2-vision", "claude-3-vision", "FooVISION-bar"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			m := HeuristicCapabilities(id)
			if !m.Vision {
				t.Errorf("Vision = false, want true for %q", id)
			}
		})
	}
}

func TestHeuristicCapabilities_Reasoning(t *testing.T) {
	cases := []string{"o1", "o1-mini", "o3-mini", "o4", "deepseek-r1", "deepseek-reasoning", "qwq-32b-thinking", "qwq", "Qwen3-R1"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			m := HeuristicCapabilities(id)
			if !m.Reasoning {
				t.Errorf("Reasoning = false, want true for %q", id)
			}
		})
	}
}

func TestHeuristicCapabilities_EmbeddingsNoToolUse(t *testing.T) {
	cases := []string{"text-embedding-3-small", "text-embedding-ada-002", "dall-e-3", "tts-1", "tts-1-hd", "whisper-1"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			m := HeuristicCapabilities(id)
			if m.ToolUse {
				t.Errorf("ToolUse = true, want false for %q", id)
			}
		})
	}
}

func TestHeuristicCapabilities_DefaultsToChat(t *testing.T) {
	// An id that has no marker should still get
	// tool use + stream (they are the modern
	// default). The heuristic must be optimistic
	// for unknown chat-shaped ids.
	m := HeuristicCapabilities("gpt-4o")
	if !m.ToolUse || !m.Stream {
		t.Errorf("defaults = (ToolUse=%v, Stream=%v), want (true, true)", m.ToolUse, m.Stream)
	}
	if m.Vision || m.Reasoning {
		t.Errorf("gpt-4o has no vision/reasoning marker; got Vision=%v Reasoning=%v", m.Vision, m.Reasoning)
	}
	if m.Source != SourceProvider {
		t.Errorf("Source = %v, want SourceProvider", m.Source)
	}
	if m.Provider != "" {
		t.Errorf("Provider = %q, want empty (heuristic must not guess)", m.Provider)
	}
}

func TestListProviderModels_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer TESTKEY" {
			t.Errorf("auth = %q, want Bearer TESTKEY", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "gpt-4o", "object": "model"},
				{"id": "gpt-4o-mini", "object": "model"},
				{"id": "", "object": "model"}, // empty id is filtered
			},
		})
	}))
	defer srv.Close()
	got, err := ListProviderModels(context.Background(), srv.URL, "TESTKEY")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gpt-4o", "gpt-4o-mini"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestListProviderModels_EmptyBaseURL(t *testing.T) {
	_, err := ListProviderModels(context.Background(), "", "k")
	if err == nil {
		t.Fatal("err = nil, want error for empty baseURL")
	}
}

func TestListProviderModels_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := ListProviderModels(context.Background(), srv.URL, "k")
	if err == nil {
		t.Fatal("err = nil, want error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %q, want substring 404", err.Error())
	}
}

func TestListProviderModels_NoAuthHeaderWhenKeyEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("auth = %q, want empty (no key provided)", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()
	got, err := ListProviderModels(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestListProviderModels_CleansPastedAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("auth = %q, want Bearer sk-test", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m"}}})
	}))
	defer srv.Close()
	got, err := ListProviderModels(context.Background(), srv.URL+"/v1", " sk-\r\ntest\t ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "m" {
		t.Fatalf("models = %v, want [m]", got)
	}
}

func TestCleanAPIKey_StripsCommonPasteWrappers(t *testing.T) {
	cases := map[string]string{
		`"sk-test"`:          "sk-test",
		`"Bearer sk-test"`:   "sk-test",
		`Bearer "sk-test"`:   "sk-test",
		" 'sk-test' ":        "sk-test",
		"\tBearer sk-\nkey ": "sk-key",
	}
	for in, want := range cases {
		if got := CleanAPIKey(in); got != want {
			t.Fatalf("CleanAPIKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListProviderModels_HandlesTrailingSlash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()
	if _, err := ListProviderModels(context.Background(), srv.URL+"/", "k"); err != nil {
		t.Errorf("trailing slash should work: %v", err)
	}
}
