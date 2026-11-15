package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Live Logfare (1.0.28): a chat model listed with chat_capabilities.tools =
// false answered a request carrying tools with HTTP 200, no tool call and
// unrelated text. The recorded reply is replayed here (sanitized).
func TestCatalogNoToolsModelIsSentWithoutTools(t *testing.T) {
	catalog, err := os.ReadFile("testdata/logfare_models.json")
	if err != nil {
		t.Fatal(err)
	}
	var modelsCalls, toolsSent atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			modelsCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(catalog)
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			body, _ := io.ReadAll(r.Body)
			var req map[string]json.RawMessage
			_ = json.Unmarshal(body, &req)
			if _, ok := req["tools"]; ok {
				toolsSent.Add(1)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"logfare-<ID>\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"[ ]\"},\"finish_reason\":null}]}\n\n"+
				"data: {\"id\":\"logfare-<ID>\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	catalogToolsSkipLocal = false
	defer func() { catalogToolsSkipLocal = true; resetCatalogTools(); resetToolsUnsupported() }()
	tool := []ToolDef{{Name: "read_lines", Description: "read", Schema: `{"type":"object","properties":{"file":{"type":"string"}}}`}}
	run := func(model string) (notices []string) {
		p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: model, APIKey: "test"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "read notes.txt"}}, tool)
		if err != nil {
			t.Fatal(err)
		}
		for d := range ch {
			if d.Err != nil {
				t.Fatal(d.Err)
			}
			if d.Notice != "" {
				notices = append(notices, d.Notice)
			}
		}
		return notices
	}
	// Listed without tools: no tools on the wire, one notice.
	notices := run("text-only-sample")
	if toolsSent.Load() != 0 || len(notices) != 1 || !strings.Contains(notices[0], "does not support tool calling") {
		t.Fatalf("tools sent=%d notices=%q", toolsSent.Load(), notices)
	}
	// Listed with tools: unchanged, and the catalog is read once per endpoint.
	if notices := run("gemma-4-26b"); toolsSent.Load() != 1 || len(notices) != 0 {
		t.Fatalf("tools-capable model: tools sent=%d notices=%q", toolsSent.Load(), notices)
	}
	if modelsCalls.Load() != 1 {
		t.Fatalf("catalog read %d times, want 1", modelsCalls.Load())
	}
}

func TestCatalogToolsCheckSkipsLocalAndFirstParty(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:1234/v1", "https://api.openai.com/v1"} {
		if catalogListsWithoutTools(context.Background(), u, "", "m") {
			t.Fatalf("%s checked", u)
		}
	}
}
