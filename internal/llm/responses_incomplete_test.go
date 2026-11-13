package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression: a Responses stream that ends with response.incomplete (output
// budget reached, more likely at high/xhigh effort) surfaced as "stream ended
// before response.completed" and lost the partial answer's usage and calls.
func TestResponsesIncompleteIsATruncatedFinishNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		codexSSE(w,
			`{"type":"response.output_text.delta","delta":"partial"}`,
			`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":2,"output_tokens":32000,"total_tokens":32002}}}`,
		)
	}))
	defer srv.Close()
	p, err := NewResponses(ResponsesConfig{BaseURL: srv.URL, APIKey: "k", Model: "gpt-5.4"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var finish, text string
	var usage *Usage
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("unexpected error: %v", d.Err)
		}
		text += d.Content
		if d.FinishReason != "" {
			finish = d.FinishReason
		}
		if d.Usage != nil {
			usage = d.Usage
		}
	}
	if finish != "length" || text != "partial" || usage == nil || usage.Output != 32000 {
		t.Fatalf("finish=%q text=%q usage=%+v", finish, text, usage)
	}
}
