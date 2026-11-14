package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRefreshLocalModelContextsReadsLlamaServerProps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			fmt.Fprint(w, `{"default_generation_settings":{"n_ctx":12288},"total_slots":1,"chat_template":"..."}`)
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"Qwen3-8B-Q4_K_M.gguf","meta":{"n_ctx_train":40960}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base := srv.URL + "/v1"
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	if got := ProviderRuntimeContext(base, "", "Qwen3-8B-Q4_K_M.gguf"); got != 12288 {
		t.Fatalf("listed id runtime=%d, want loaded n_ctx 12288 (not n_ctx_train)", got)
	}
	// llama-server answers any model name with its one loaded model.
	if got := ProviderRuntimeContext(base, "", "qwen3-8b"); got != 12288 {
		t.Fatalf("configured alias runtime=%d", got)
	}
	if got := ProviderRuntimeContext(srv.URL+"/other/v1", "", "qwen3-8b"); got != 0 {
		t.Fatalf("wildcard crossed endpoints: %d", got)
	}
}

func TestRefreshLocalModelContextsLlamaRouterIsPerModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			switch r.URL.Query().Get("model") {
			case "a":
				fmt.Fprint(w, `{"default_generation_settings":{"n_ctx":8192}}`)
			case "b":
				fmt.Fprint(w, `{"default_generation_settings":{"n_ctx":65536}}`)
			default:
				fmt.Fprint(w, `{"role":"router"}`)
			}
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"a"},{"id":"b"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base := srv.URL + "/v1"
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	if a, b := ProviderRuntimeContext(base, "", "a"), ProviderRuntimeContext(base, "", "b"); a != 8192 || b != 65536 {
		t.Fatalf("router windows a=%d b=%d", a, b)
	}
	if got := ProviderRuntimeContext(base, "", "c"); got != 0 {
		t.Fatalf("router must not guess a window for an unknown model: %d", got)
	}
}

func TestRefreshLocalModelContextsReadsOllamaPs(t *testing.T) {
	loaded := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			http.NotFound(w, r)
			return
		}
		if !loaded {
			fmt.Fprint(w, `{"models":[]}`)
			return
		}
		fmt.Fprint(w, `{"models":[{"name":"qwen3:latest","model":"qwen3:latest","context_length":4096}]}`)
	}))
	defer srv.Close()
	base := srv.URL + "/v1"
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	if got := ProviderRuntimeContext(base, "", "qwen3"); got != 0 {
		t.Fatalf("nothing loaded yet, runtime=%d", got)
	}
	// Ollama loads lazily: an empty /api/ps must not mark the server unsupported.
	loaded = true
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"qwen3:latest", "qwen3"} {
		if got := ProviderRuntimeContext(base, "", id); got != 4096 {
			t.Fatalf("%s runtime=%d, want 4096", id, got)
		}
	}
}
