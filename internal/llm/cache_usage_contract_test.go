package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// Native serializers/parsers and the shared meter consume synthetic transport
// fixtures. No inference, real cache hits or provider throughput are measured.
func TestCacheUsageContractAcrossTransports(t *testing.T) {
	openAISSE := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2,\"total_tokens\":1002,\"prompt_tokens_details\":{\"cached_tokens\":750}}}\n\n" +
		"data: [DONE]\n\n"
	localSSE := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2,\"total_tokens\":1002},\"timings\":{\"prompt_n\":250,\"predicted_n\":2}}\n\n" +
		"data: [DONE]\n\n"
	anthropicSSE := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"cache_read_input_tokens\":750,\"cache_creation_input_tokens\":150}}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	responsesSSE := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1000,\"output_tokens\":2,\"total_tokens\":1002,\"input_tokens_details\":{\"cached_tokens\":750}}}}\n\n"
	for _, tc := range []struct {
		name string
		sse  string
		new  func(*http.Client) (Provider, error)
	}{
		{"openai", openAISSE, func(client *http.Client) (Provider, error) {
			off := false
			return NewOpenAI(OpenAIConfig{BaseURL: "https://api.openai.com/v1", Model: "cache-fixture", HTTPClient: client, CachePrompt: &off})
		}},
		{"local_timings", localSSE, func(client *http.Client) (Provider, error) {
			on := true
			return NewOpenAI(OpenAIConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: "cache-fixture", HTTPClient: client, CachePrompt: &on})
		}},
		{"anthropic", anthropicSSE, func(client *http.Client) (Provider, error) {
			return NewAnthropic(AnthropicConfig{BaseURL: "https://api.anthropic.com/v1", Model: "cache-fixture", HTTPClient: client})
		}},
		{"responses", responsesSSE, func(client *http.Client) (Provider, error) {
			return NewResponses(ResponsesConfig{BaseURL: "https://api.openai.com/v1", Model: "cache-fixture", HTTPClient: client})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: anthropicOwnedBodyTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				return anthropicOwnedBodyResponse(req, http.StatusOK, tc.sse), nil
			})}
			p, err := tc.new(client)
			if err != nil {
				t.Fatal(err)
			}
			stats := &sinkCapture{}
			metered := Metered(p, tc.name, PurposeMain, stats.sink())
			ch, err := metered.Complete(WithPurpose(context.Background(), PurposeCompact),
				[]Message{{Role: RoleSystem, Content: "fixture instructions"}, {Role: RoleUser, Content: "fixture transcript"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for d := range ch {
				if d.Err != nil {
					t.Fatal(d.Err)
				}
			}
			got := stats.all()
			if calls != 1 || len(got) != 1 {
				t.Fatalf("HTTP calls=%d metered calls=%d, want one", calls, len(got))
			}
			if got[0].TokensIn != 1000 || got[0].TokensCached != 750 || got[0].TokensOut != 2 ||
				got[0].PrefillEvaluated != 250 || got[0].Purpose != PurposeCompact || got[0].Failed {
				t.Fatalf("normalized cache telemetry=%+v", got[0])
			}
		})
	}
}

func TestResponsesCacheKeyStableAcrossPurposesAndInstances(t *testing.T) {
	var keys []string
	calls := 0
	client := &http.Client{Transport: anthropicOwnedBodyTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal(body, &root); err != nil {
			return nil, err
		}
		var key string
		if err := json.Unmarshal(root["prompt_cache_key"], &key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
		return anthropicOwnedBodyResponse(req, http.StatusOK, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n"), nil
	})}
	newProvider := func() *ResponsesProvider {
		p, err := NewResponses(ResponsesConfig{BaseURL: "https://api.openai.com/v1", Model: "cache-fixture", HTTPClient: client})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := newProvider()
	for _, purpose := range []string{PurposeMain, PurposeCompact, PurposeTask} {
		ch, err := p.Complete(WithPurpose(context.Background(), purpose), []Message{{Role: RoleUser, Content: purpose}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range drainDeltas(t, ch) {
			if d.Err != nil {
				t.Fatal(d.Err)
			}
		}
	}
	other := newProvider()
	ch, err := other.Complete(context.Background(), []Message{{Role: RoleUser, Content: "other provider instance"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drainDeltas(t, ch) {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
	}
	if calls != 4 || len(keys) != 4 || keys[0] == "" || keys[0] != keys[1] || keys[0] != keys[2] || keys[0] == keys[3] {
		t.Fatal("Responses cache key must persist per provider, including helper/task purposes; separate instances get independent keys")
	}
}
