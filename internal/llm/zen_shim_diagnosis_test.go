package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Characterization only: every request terminates in this in-memory transport.
// No real model, account, catalog, or endpoint is consulted.
func TestZenShimDiagnosisFakeHTTP(t *testing.T) {
	for _, tc := range []struct {
		name string
		base string
		want []string
	}{
		{"zen", "https://opencode.ai/zen/v1", []string{"ctx_execute", "read_lines", "bash", "read"}},
		{"go", "https://opencode.ai/zen/go/v1", []string{"ctx_execute", "read_lines", "bash", "read"}},
		{"ordinary", "https://fixture.invalid/v1", []string{"ctx_execute", "read_lines"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p, err := NewOpenAI(OpenAIConfig{
				BaseURL: tc.base,
				Model:   "synthetic-shim-diagnosis",
				HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					var req struct {
						Tools []struct {
							Type     string `json:"type"`
							Function struct {
								Name       string          `json:"name"`
								Parameters json.RawMessage `json:"parameters"`
							} `json:"function"`
						} `json:"tools"`
						Stream bool `json:"stream"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						return nil, err
					}
					var names []string
					for _, tool := range req.Tools {
						if tool.Type != "function" || len(tool.Function.Parameters) == 0 {
							t.Errorf("not a nested function declaration: %+v", tool)
						}
						names = append(names, tool.Function.Name)
					}
					if !req.Stream || !reflect.DeepEqual(names, tc.want) {
						t.Errorf("stream=%v names=%v want=%v", req.Stream, names, tc.want)
					}
					// The LLM parser must retain the original wire alias for the agent.
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
						"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"shim-1\",\"type\":\"function\",\"function\":{\"name\":\"functions.read\",\"arguments\":\"{\\\"filePath\\\":\\\"synthetic.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"))}, nil
				})},
			})
			if err != nil {
				t.Fatal(err)
			}
			defs := []ToolDef{{Name: "ctx_execute", Schema: `{"type":"object"}`}, {Name: "read_lines", Schema: `{"type":"object"}`}}
			ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "synthetic fixture"}}, defs)
			if err != nil {
				t.Fatal(err)
			}
			toolCalls := 0
			for d := range ch {
				if d.Err != nil {
					t.Fatal(d.Err)
				}
				if d.ToolCall != nil {
					toolCalls++
					if d.ToolCall.Name != "functions.read" || d.ToolCall.Arguments != `{"filePath":"synthetic.txt"}` {
						t.Errorf("wire alias changed: %+v", d.ToolCall)
					}
				}
			}
			if calls != 1 || toolCalls != 1 || len(defs) != 2 {
				t.Fatalf("requests=%d toolCalls=%d callerDefs=%d", calls, toolCalls, len(defs))
			}
			t.Logf("requests=%d inbound alias retained=%d outbound names=%v", calls, toolCalls, tc.want)
		})
	}
}

func TestZenShimDiagnosisInvalidToolsClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"http400", http.StatusBadRequest, `{"error":{"message":"INVALIDTOOLS","type":"invalid_request_error"}}`, "400"},
		{"stream200", http.StatusOK, "data: {\"error\":{\"message\":\"INVALIDTOOLS\"}}\n\n", "provider stream error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p, err := NewOpenAI(OpenAIConfig{
				BaseURL: "https://opencode.ai/zen/v1", Model: "synthetic-invalid-tools",
				HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
				})},
			})
			if err != nil {
				t.Fatal(err)
			}
			ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "synthetic fixture"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			errors, tools := 0, 0
			var classification string
			for d := range ch {
				if d.ToolCall != nil {
					tools++
				}
				if d.Err != nil {
					errors++
					classification = fmt.Sprint(d.Err)
					if !strings.Contains(classification, tc.want) || !strings.Contains(classification, "INVALIDTOOLS") {
						t.Errorf("unexpected error classification: %s", classification)
					}
				}
			}
			if calls != 1 || errors != 1 || tools != 0 {
				t.Fatalf("requests=%d errors=%d toolCalls=%d", calls, errors, tools)
			}
			t.Logf("requests=%d toolCalls=%d error=%s", calls, tools, classification)
		})
	}
}
