package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression: the provider connection check always sent max_tokens and
// reasoning_effort. OpenAI reasoning models reject max_tokens and classic
// chat models reject reasoning_effort, so onboarding reported a working
// model as unavailable; reasoning in completion_tokens_details was missed.
func TestProbeAdaptsToOpenAIModelFamilies(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		bodies = append(bodies, m)
		model, _ := m["model"].(string)
		_, effort := m["reasoning_effort"]
		_, maxTokens := m["max_tokens"]
		switch {
		case strings.HasPrefix(model, "gpt-5") && maxTokens:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."}}`))
		case strings.HasPrefix(model, "gpt-4o") && effort:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: reasoning_effort"}}`))
		case strings.HasPrefix(model, "gpt-5"):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}],"usage":{"completion_tokens_details":{"reasoning_tokens":5}}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}],"usage":{}}`))
		}
	}))
	defer srv.Close()
	for _, tc := range []struct {
		model     string
		reasoning bool
	}{{"gpt-5.4", true}, {"gpt-4o", false}} {
		res, err := Probe(context.Background(), srv.URL, "k", tc.model)
		if err != nil || res.Error != "" {
			t.Fatalf("%s: probe failed: err=%v result=%q bodies=%v", tc.model, err, res.Error, bodies)
		}
		if res.Reasoning != tc.reasoning {
			t.Fatalf("%s: reasoning=%v, want %v", tc.model, res.Reasoning, tc.reasoning)
		}
	}
}
