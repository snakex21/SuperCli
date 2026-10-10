package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
	"supercli/internal/tools/interactive"
	"supercli/internal/tools/office"
)

type anthropicSchemaHTTPTransport func(*http.Request) (*http.Response, error)

func (f anthropicSchemaHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func anthropicSchemaHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}

// Exercise the complete HTTP request, including the real builtin at tools.10.
// The transport models the reported schema rejection without contacting a model.
func TestAnthropicMessagesToolSchemasHTTP(t *testing.T) {
	zip := office.NewReadZip(".", 0).Spec()
	ask := interactive.NewAskUser(nil).Spec()
	builtin := make([]llm.ToolDef, 0, 12)
	for i := 0; i < 10; i++ {
		builtin = append(builtin, llm.ToolDef{Name: fmt.Sprintf("filler_%d", i), Schema: `{"type":"object","properties":{}}`})
	}
	builtin = append(builtin, llm.ToolDef{Name: zip.Name, Description: zip.Description, Schema: zip.Schema}, llm.ToolDef{Name: ask.Name, Description: ask.Description, Schema: ask.Schema})
	cases := []struct {
		name         string
		tools        []llm.ToolDef
		branchFields []string
	}{
		{name: "builtins", tools: builtin},
	}
	for _, keyword := range []string{"oneOf", "allOf", "anyOf"} {
		raw := fmt.Sprintf(`{"type":"object","properties":{"common":{"type":"string"},"nested":{"type":"object","anyOf":[{"properties":{"x":{"type":"string"}},"required":["x"]},{"properties":{"y":{"type":"integer"}},"required":["y"]}]}},"required":["common"],"%s":[{"properties":{"left":{"type":"string","minLength":2}},"required":["left"]},{"properties":{"right":{"type":"integer","minimum":1}},"required":["right"]}]}`, keyword)
		cases = append(cases, struct {
			name         string
			tools        []llm.ToolDef
			branchFields []string
		}{keyword, []llm.ToolDef{{Name: "mcp_fixture", Schema: raw}}, []string{"left", "right"}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]llm.ToolDef(nil), tc.tools...)
			calls := 0
			var sent []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"input_schema"`
			}
			client := &http.Client{Transport: anthropicSchemaHTTPTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
					return nil, fmt.Errorf("unexpected endpoint %s %s", r.Method, r.URL.Path)
				}
				var request struct {
					Model    string            `json:"model"`
					Stream   bool              `json:"stream"`
					Messages []json.RawMessage `json:"messages"`
					Tools    []struct {
						Name        string         `json:"name"`
						InputSchema map[string]any `json:"input_schema"`
					} `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					return nil, err
				}
				if request.Model != "claude-fixture" || !request.Stream || len(request.Messages) != 1 || len(request.Tools) != len(tc.tools) {
					return nil, fmt.Errorf("incomplete request envelope")
				}
				sent = request.Tools
				for i, tool := range request.Tools {
					for _, keyword := range []string{"oneOf", "allOf", "anyOf"} {
						if _, exists := tool.InputSchema[keyword]; exists {
							return anthropicSchemaHTTPResponse(http.StatusBadRequest, fmt.Sprintf(`{"error":{"type":"invalid_request_error","message":"tools.%d.custom.input_schema does not support oneOf/allOf/anyOf at top level"}}`, i)), nil
						}
					}
				}
				return anthropicSchemaHTTPResponse(http.StatusOK, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), nil
			})}
			provider, err := llm.NewAnthropic(llm.AnthropicConfig{BaseURL: "http://schema.invalid/v1", Model: "claude-fixture", HTTPClient: client})
			if err != nil {
				t.Fatal(err)
			}
			output, err := provider.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "fixture"}}, tc.tools)
			if err != nil {
				t.Fatal(err)
			}
			var text string
			for delta := range output {
				if delta.Err != nil {
					t.Errorf("HTTP schema rejected: %v", delta.Err)
				}
				text += delta.Content
			}
			if calls != 1 || text != "ok" {
				t.Errorf("calls=%d text=%q, want one successful request", calls, text)
			}
			if !reflect.DeepEqual(tc.tools, original) {
				t.Fatal("request mutated original tool definitions")
			}
			for i, tool := range sent {
				if tool.Name != tc.tools[i].Name || tool.InputSchema["type"] != "object" {
					t.Errorf("tool identity/root changed: %+v", tool)
				}
				var full map[string]any
				if err := json.Unmarshal([]byte(tc.tools[i].Schema), &full); err != nil {
					t.Fatal(err)
				}
				fullProps, _ := full["properties"].(map[string]any)
				wireProps, _ := tool.InputSchema["properties"].(map[string]any)
				for name, value := range fullProps {
					if !reflect.DeepEqual(value, wireProps[name]) {
						t.Errorf("nested property %s changed", name)
					}
				}
				for _, name := range tc.branchFields {
					if _, exists := wireProps[name]; !exists {
						t.Errorf("lost branch-only property %q", name)
					}
				}
			}
		})
	}
}

func TestAnthropicProjectionPreservesFullLocalValidation(t *testing.T) {
	cases := []struct {
		spec           core.Tool
		valid, invalid []string
	}{
		{office.NewReadZip(".", 0).Spec(), []string{`{"path":"one.zip"}`, `{"paths":["one.zip"]}`}, []string{`{}`, `{"path":"one.zip","paths":["two.zip"]}`}},
		{interactive.NewAskUser(nil).Spec(), []string{`{"question":"Pick","options":[{"label":"A"},{"label":"B"}]}`, `{"questions":[{"question":"Pick","options":[{"label":"A"},{"label":"B"}]}]}`}, []string{`{}`, `{"question":"Pick","options":[{"label":"A"}]}`}},
	}
	for _, tc := range cases {
		t.Run(tc.spec.Name, func(t *testing.T) {
			original := tc.spec.Schema
			compiled, err := llm.CompileToolSchema(original)
			if err != nil {
				t.Fatal(err)
			}
			var full map[string]any
			if err := json.Unmarshal(compiled.Full, &full); err != nil {
				t.Fatal(err)
			}
			if _, one := full["oneOf"]; !one {
				if _, any := full["anyOf"]; !any {
					t.Fatal("full form lost root constraint")
				}
			}
			called := 0
			tc.spec.Fn = func(context.Context, json.RawMessage) (core.Result, error) {
				called++
				return core.Result{Text: "fixture"}, nil
			}
			registry := core.NewRegistry()
			if err := registry.Register(tc.spec); err != nil {
				t.Fatal(err)
			}
			for _, args := range tc.valid {
				if _, err := registry.Execute(context.Background(), tc.spec.Name, json.RawMessage(args)); err != nil {
					t.Errorf("valid arguments rejected: %v", err)
				}
			}
			before := called
			for _, args := range tc.invalid {
				if result, err := registry.Execute(context.Background(), tc.spec.Name, json.RawMessage(args)); err == nil && result.Err == nil {
					t.Errorf("invalid arguments accepted: %s", args)
				}
			}
			if called != before {
				t.Fatal("invalid arguments reached execution")
			}
			if tc.spec.Schema != original {
				t.Fatal("local schema mutated")
			}
		})
	}
}
