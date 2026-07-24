package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeuristicCapabilities_DoesNotGuessVisionFromModelName(t *testing.T) {
	cases := []string{
		"gpt-4-vision-preview",
		"llama-3.2-vision",
		"qwen3.5-9b-uncensored-hauhaucs-aggressive",
		"qwen2.5-vl-32b-instruct",
		"qwen3-omni-30b",
	}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			m := HeuristicCapabilities(id)
			if m.Vision || m.VisionKnown {
				t.Errorf("vision was guessed from model name %q: %+v", id, m)
			}
		})
	}
}

func TestHeuristicCapabilities_TextOnlyQwen3DoesNotBecomeVision(t *testing.T) {
	for _, id := range []string{"qwen3-8b", "qwen3-32b-instruct", "qwen2.5-14b"} {
		if got := HeuristicCapabilities(id); got.Vision {
			t.Errorf("Vision = true, want false for text-only family %q", id)
		}
	}
}

func TestParseProviderModelInfos_UsesAdvertisedModalities(t *testing.T) {
	body := []byte(`{"data":[
		{"id":"vision-model","architecture":{"input_modalities":["text","image"]},"context_length":131072},
		{"id":"text-model","architecture":{"input_modalities":["text"]}},
		{"id":"unknown-model"}
	]}`)
	models, err := parseProviderModelInfos(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %d, want 3", len(models))
	}
	if !models[0].Vision || !models[0].VisionKnown || models[0].ContextLength != 131072 {
		t.Fatalf("vision metadata not parsed: %+v", models[0])
	}
	if models[1].Vision || !models[1].VisionKnown {
		t.Fatalf("text-only metadata not parsed: %+v", models[1])
	}
	if models[2].Vision || models[2].VisionKnown {
		t.Fatalf("missing metadata should stay unknown: %+v", models[2])
	}
}

func TestParseProviderModelInfos_LMStudioNativeCapabilities(t *testing.T) {
	body := []byte(`{"models":[
		{"key":"loaded-vlm","type":"vlm","capabilities":{"vision":true,"trained_for_tool_use":true}},
		{"key":"loaded-text","type":"llm","capabilities":{"vision":false}}
	]}`)
	models, err := parseProviderModelInfos(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || !models[0].Vision || !models[0].VisionKnown {
		t.Fatalf("LM Studio VLM metadata not parsed: %+v", models)
	}
	if models[1].Vision || !models[1].VisionKnown {
		t.Fatalf("LM Studio text metadata not parsed: %+v", models[1])
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

func TestIsFreeModelID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"kilo-auto/free", true},
		{"openai/gpt-oss-20b:free", true},
		{"deepseek-v4-flash-free", true},
		{"anthropic/claude-sonnet-4.5", false},
		{"freedom-model", false},
	}
	for _, tc := range cases {
		if got := IsFreeModelID(tc.id); got != tc.want {
			t.Fatalf("IsFreeModelID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestListFreeModelsUsesProviderMetadataWithoutTreatingMissingPricesAsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "paid-without-metadata"},
				{"id": "paid", "isFree": false, "pricing": map[string]any{"prompt": "0.000001", "completion": "0.000002"}},
				{"id": "zero-priced-preview", "isFree": false, "pricing": map[string]any{"prompt": "0", "completion": "0"}},
				{"id": "kilo-routed", "isFree": true, "pricing": map[string]any{"prompt": "0", "completion": "0"}},
				{"id": "promotional-free"},
				{"id": "zero-priced-alias", "pricing": map[string]any{"input": 0, "output": 0}},
			},
		})
	}))
	defer srv.Close()

	got, err := ListFreeModels(context.Background(), srv.URL+"/v1", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kilo-routed", "promotional-free", "zero-priced-alias"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("free models = %v, want %v", got, want)
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

func TestListAnthropicModels_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "ANTHROPICKEY" {
			t.Errorf("x-api-key = %q, want ANTHROPICKEY", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Errorf("anthropic-version header missing")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "claude-sonnet-4-5", "type": "model"},
				{"id": "claude-haiku-4-5", "type": "model"},
			},
		})
	}))
	defer srv.Close()
	got, err := ListAnthropicModels(context.Background(), srv.URL, "ANTHROPICKEY")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"claude-sonnet-4-5", "claude-haiku-4-5"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("models=%v want %v", got, want)
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

func TestListProviderModels_CacheIsScopedByCredential(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.Header.Get("Authorization") {
		case "Bearer key-a":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "model-a"}}})
		default:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	if _, err := ListProviderModels(context.Background(), srv.URL, "key-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := ListProviderModels(context.Background(), srv.URL, "key-b"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("new credential reused an old successful cache entry: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestInvalidateProviderModelCacheForcesCredentialVerification(t *testing.T) {
	authorized := true
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !authorized {
			http.Error(w, "revoked", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "model"}}})
	}))
	defer srv.Close()
	if _, err := ListProviderModels(context.Background(), srv.URL, "key"); err != nil {
		t.Fatal(err)
	}
	authorized = false
	if _, err := ListProviderModels(context.Background(), srv.URL, "key"); err != nil {
		t.Fatalf("expected the ordinary catalog read to use its fresh cache: %v", err)
	}
	InvalidateProviderModelCache(srv.URL)
	if _, err := ListProviderModels(context.Background(), srv.URL, "key"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("invalidated cache still hid revoked credentials: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestCleanAPIKey_StripsCommonPasteWrappers(t *testing.T) {
	cases := map[string]string{
		`"sk-test"`:          "sk-test",
		`"Bearer sk-test"`:   "sk-test",
		`Bearer "sk-test"`:   "sk-test",
		"Bearer ":            "",
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
