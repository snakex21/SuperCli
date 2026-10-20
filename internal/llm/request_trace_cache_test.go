package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Missing/null details are unknown, not explicit zero; positive local timing
// evidence remains usable when cloud-style details carry no cached_tokens.
func TestRequestTraceCachePresence(t *testing.T) {
	for _, transport := range []string{"openai", "codex"} {
		for _, tc := range []struct {
			name, detail string
			known        bool
			cached       int
		}{
			{"omitted", "", false, 0},
			{"empty", `{}`, false, 0},
			{"null object", `null`, false, 0},
			{"null count", `{"cached_tokens":null}`, false, 0},
			{"zero", `{"cached_tokens":0}`, true, 0},
			{"positive", `{"cached_tokens":64}`, true, 64},
		} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					detail := ""
					if transport == "codex" {
						if tc.detail != "" {
							detail = `,"input_tokens_details":` + tc.detail
						}
						codexSSE(w, `{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":2,"total_tokens":102`+detail+`}}}`)
					} else {
						if tc.detail != "" {
							detail = `,"prompt_tokens_details":` + tc.detail
						}
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2,\"total_tokens\":102%s}}\n\ndata: [DONE]\n\n", detail)
					}
				}))
				defer srv.Close()
				var inner Provider
				var err error
				if transport == "codex" {
					inner, err = NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: &fakeTokens{access: "fake"}})
				} else {
					inner, err = NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "fixture"})
				}
				if err != nil {
					t.Fatal(err)
				}
				stats := make(chan CallStat, 1)
				p := Metered(inner, transport, PurposeMain, func(s CallStat) { stats <- s })
				got := requestTraceRun(t, p, WithOpenCodeSession(context.Background(), "cache-presence-"+transport+tc.name), stats)
				if got.HasCachedInput != tc.known || got.TokensCached != tc.cached {
					t.Fatalf("known=%v cached=%d, want %v/%d", got.HasCachedInput, got.TokensCached, tc.known, tc.cached)
				}
			})
		}
	}
	unknown := &Usage{Input: 100}
	deriveCachedFromTimings(unknown, &llamaTimings{CacheN: 64})
	if !unknown.HasCachedInput || unknown.CachedInput != 64 {
		t.Fatal("unknown details blocked local timing evidence")
	}
}
