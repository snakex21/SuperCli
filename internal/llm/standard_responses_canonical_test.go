package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// These definitions exercise canonical key ordering, numeric normalization,
// JSON escaping and nested schemas while remaining synthetic.
func canonicalResponsesToolDefs(count int) []ToolDef {
	defs := make([]ToolDef, count)
	for i := range defs {
		defs[i] = ToolDef{
			Name:        fmt.Sprintf("fixture_%d", i),
			Description: "Synthetic ąćę 日本語 <&> tool",
			Schema:      fmt.Sprintf(`{"title":"fixture_%d","type":"object","properties":{"zeta":{"type":"string","description":"Synthetic path <&> \u2028 \uD800","default":"ąćę 日本語 😀"},"number":{"type":"number","default":9007199254740993},"zero":{"type":"number","default":-0},"exponent":{"type":"number","default":1.20e3},"choice":{"type":"string","enum":["b","a"],"default":"first","default":"last"},"items":{"type":"array","items":{"type":"object","properties":{"value":{"type":"string"}}}}},"required":["zeta"],"additionalProperties":false}`, i),
		}
	}
	return defs
}

func canonicalResponsesHistory(size int) []Message {
	text := strings.Repeat("synthetic ordinary text <&> ąćę 日本語\n", size/42+1)
	return []Message{
		{Role: RoleSystem, Content: "synthetic instruction"},
		{Role: RoleUser, Content: text},
		{Role: RoleAssistant, Content: text},
		{Role: RoleUser, Content: "continue"},
	}
}

func canonicalResponsesLegacyRequest(req codexRequest, key string, reasoning bool, sampling Sampling) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return prepareStandardResponsesRequest(body, key, reasoning, sampling)
}

func cloneCanonicalResponsesRequest(req codexRequest) codexRequest {
	copyReq := req
	copyReq.Include = slices.Clone(req.Include)
	copyReq.Input = slices.Clone(req.Input)
	for i := range copyReq.Input {
		copyReq.Input[i].Raw = slices.Clone(req.Input[i].Raw)
		copyReq.Input[i].Content = slices.Clone(req.Input[i].Content)
	}
	copyReq.Tools = slices.Clone(req.Tools)
	for i := range copyReq.Tools {
		copyReq.Tools[i].Parameters = slices.Clone(req.Tools[i].Parameters)
	}
	if req.Reasoning != nil {
		reasoning := *req.Reasoning
		copyReq.Reasoning = &reasoning
	}
	return copyReq
}

func assertCanonicalResponsesParity(t *testing.T, req codexRequest, key string, reasoning bool, sampling Sampling) {
	t.Helper()
	before := cloneCanonicalResponsesRequest(req)
	want, wantErr := canonicalResponsesLegacyRequest(req, key, reasoning, sampling)
	got, gotErr := prepareAssembledStandardResponsesRequest(req, key, reasoning, sampling)
	if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !bytes.Equal(want, got) {
		t.Fatalf("canonical request differs: errors=%v/%v bytes=%d/%d", wantErr, gotErr, len(want), len(got))
	}
	if !reflect.DeepEqual(before, req) {
		t.Fatal("canonical prepare mutated its source request")
	}
}

func TestStandardResponsesCanonicalAssembledParity(t *testing.T) {
	floatp := func(v float64) *float64 { return &v }
	samples := []Sampling{
		{},
		{Temperature: floatp(0), TopP: floatp(.7)},
		{Temperature: floatp(math.NaN())},
		{TopP: floatp(math.Inf(1))},
	}
	for _, count := range []int{0, 3, 30} {
		for _, size := range []int{16, 128 << 10} {
			t.Run(fmt.Sprintf("tools=%d/history=%d", count, size), func(t *testing.T) {
				defs := canonicalResponsesToolDefs(count)
				defsBefore := slices.Clone(defs)
				msgs := canonicalResponsesHistory(size)
				before, err := json.Marshal(msgs)
				if err != nil {
					t.Fatal(err)
				}
				for _, effort := range []string{"", "none", "max"} {
					req, err := assembleCodexRequestWithEffort("fixture", msgs, defs, true, effort)
					if err != nil {
						t.Fatal(err)
					}
					for _, reasoning := range []bool{false, true} {
						for _, sampling := range samples {
							assertCanonicalResponsesParity(t, req, " cache ", reasoning, sampling)
						}
					}
					// Warm-cache ownership must preserve the same canonical bytes.
					again, err := assembleCodexRequestWithEffort("fixture", msgs, defs, true, effort)
					if err != nil || !reflect.DeepEqual(req, again) {
						t.Fatalf("assembly changed after preparation: %v", err)
					}
				}
				after, err := json.Marshal(msgs)
				if err != nil || !bytes.Equal(before, after) || !reflect.DeepEqual(defsBefore, defs) {
					t.Fatal("source messages or tool definitions were mutated")
				}
			})
		}
	}
}

func TestStandardResponsesCanonicalNativeAndUnicodeParity(t *testing.T) {
	raws := []string{
		`{"type":"reasoning","encrypted_content":"opaque+/=<&>","summary":[],"counter":9007199254740993,"zero":-0,"exponent":1.2e3}`,
		`{"type":"reasoning","counter":true,"counter":false,"future":{"text":"\uD800\u2028","number":1e-3}}`,
		`{"type":"reasoning","counter":1e999}`,
		`{"type":"reasoning","unfinished":`,
		"null",
		"[]",
		"{\"type\":\"reasoning\",\"encrypted_content\":\"x" + string([]byte{0xff}) + "y\"}",
	}
	for i, raw := range raws {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			block := nativeReasoning(ReasoningResponses, "fixture", "https://fixture.invalid/v1", []byte(raw))
			msgs := []Message{
				{Role: RoleUser, Content: "synthetic"},
				{Role: RoleAssistant, Content: "answer", Parts: []ContentPart{
					{Type: PartTypeReasoning, Reasoning: block},
					{Type: PartTypeText, Text: "answer"},
				}},
				{Role: RoleUser, Content: "continue"},
			}
			req, err := assembleCodexRequestWithEffort("fixture", msgs, canonicalResponsesToolDefs(3), true, "max")
			if err != nil {
				t.Fatal(err)
			}
			assertCanonicalResponsesParity(t, req, "cache", true, Sampling{})
			if string(block.Data) != raw {
				t.Fatal("mutated opaque native reasoning")
			}
		})
	}
	invalidUTF8 := string([]byte{'x', 0xff, 0xfe, 'y'})
	for _, text := range []string{"", "ąćę 日本語 😀", "<&>\\\"\n\u2028\u2029", invalidUTF8} {
		defs := canonicalResponsesToolDefs(3)
		defs[0].Description = text
		req, err := assembleCodexRequestWithEffort(text, []Message{{Role: RoleUser, Content: text}}, defs, false, text)
		if err != nil {
			t.Fatal(err)
		}
		assertCanonicalResponsesParity(t, req, text, false, Sampling{})
	}
}

func TestStandardResponsesCanonicalSchemaErrors(t *testing.T) {
	for _, schema := range []string{"", "null", "[]", "7", `{"type":"object","required":[7]}`, `{"type":"object","broken":`, `{"type":"object","x":1e999}`} {
		msgs := []Message{{Role: RoleUser, Content: "synthetic"}}
		defs := []ToolDef{{Name: "fixture", Schema: schema}}
		want, wantErr := standardResponsesLegacyFixture("fixture", msgs, defs, false, "", "cache", false, Sampling{})
		req, gotErr := assembleCodexRequestWithEffort("fixture", msgs, defs, false, "")
		var got []byte
		if gotErr == nil {
			got, gotErr = prepareAssembledStandardResponsesRequest(req, "cache", false, Sampling{})
		}
		if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !bytes.Equal(want, got) {
			t.Fatalf("schema %q behavior changed: %v/%v", schema, wantErr, gotErr)
		}
	}
}

func TestStandardResponsesCanonicalKeepsArbitraryTypedLegacyDecode(t *testing.T) {
	for i, parameters := range []string{
		"",
		`{"z":9007199254740993,"a":1.20e3,"zero":-0,"escaped":"\u003c\u2028\uD800","duplicate":1,"duplicate":2}`,
		`[9007199254740993,1.20e3,-0,"<&>"]`,
		"null",
		"true",
		`"arbitrary string"`,
		`{"overflow":1e999}`,
		`{"unfinished":`,
		"{\"text\":\"x" + string([]byte{0xff}) + "y\"}",
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			req := codexRequest{
				Model: "fixture", ToolChoice: "auto", Stream: true, Include: []string{},
				Input: []codexItem{{Raw: json.RawMessage(`{"type":"reasoning","counter":9007199254740993}`)}},
				Tools: []codexToolDecl{{Type: "function", Name: "fixture", Parameters: json.RawMessage(parameters)}},
			}
			before := cloneCanonicalResponsesRequest(req)
			want, wantErr := canonicalResponsesLegacyRequest(req, "", false, Sampling{})
			got, gotErr := prepareOwnedStandardResponsesRequest(req, "", false, Sampling{})
			if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || !bytes.Equal(want, got) {
				t.Fatalf("arbitrary typed parameters changed: %v/%v bytes=%d/%d", wantErr, gotErr, len(want), len(got))
			}
			if !reflect.DeepEqual(before, req) {
				t.Fatal("legacy typed preparation mutated source")
			}
		})
	}
}

func TestStandardResponsesCanonicalProviderPrimaryAndImageFallback(t *testing.T) {
	for _, imageFallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("fallback=%t", imageFallback), func(t *testing.T) {
			msgs := canonicalResponsesHistory(16)
			if imageFallback {
				msgs[1] = Message{Role: RoleUser, Parts: []ContentPart{
					{Type: PartTypeText, Text: "image fixture"},
					{Type: PartTypeImage, Image: &ImageRef{URL: "https://fixture.invalid/image.png"}},
				}}
			}
			defs := canonicalResponsesToolDefs(3)
			var actual [][]byte
			transport := responsesRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				actual = append(actual, body)
				if imageFallback && len(actual) == 1 {
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"image inputs are not supported"}}`)),
						Request:    req,
					}, nil
				}
				return canonicalResponsesCompletedResponse(req), nil
			})
			p, err := NewResponses(ResponsesConfig{BaseURL: "https://fixture.invalid/v1", Model: "fixture", HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			p.inner.cfg.PromptCacheKey = "fixed-cache"
			ch, err := p.Complete(context.Background(), msgs, defs)
			if err != nil {
				t.Fatal(err)
			}
			for delta := range ch {
				if delta.Err != nil {
					t.Fatal(delta.Err)
				}
			}
			count := 1
			if imageFallback {
				count = 2
			}
			if len(actual) != count {
				t.Fatalf("expected %d requests, got %d", count, len(actual))
			}
			filtered := filterNativeReasoning(msgs, ReasoningResponses, p.inner.cfg.Model, p.inner.cfg.BackendURL)
			for i, body := range actual {
				req, err := assembleCodexRequestWithEffort(p.inner.cfg.Model, filtered, defs, i == 0, p.inner.reasoningEffort())
				if err != nil {
					t.Fatal(err)
				}
				want, err := canonicalResponsesLegacyRequest(req, p.inner.cfg.PromptCacheKey, p.inner.supportsReasoningControl(), p.inner.sampling)
				if err != nil || !bytes.Equal(want, body) {
					t.Fatalf("provider request %d differs from legacy bytes: %v", i, err)
				}
			}
		})
	}
}

func canonicalResponsesCompletedResponse(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{}}\n\n")),
		Request:    req,
	}
}

var canonicalResponsesBenchmarkBody []byte

// Compare the actual previous provider preparation boundary with the new one:
// both assemble on each iteration and use the existing warm normalization cache.
// The full-body legacy decoder is only the parity oracle, not this baseline.
func BenchmarkStandardResponsesCanonicalPreparedRequest(b *testing.B) {
	for _, count := range []int{0, 3, 30} {
		for _, size := range []int{16, 128 << 10} {
			b.Run(fmt.Sprintf("tools=%d/history=%d", count, size), func(b *testing.B) {
				msgs := canonicalResponsesHistory(size)
				defs := canonicalResponsesToolDefs(count)
				if _, err := assembleCodexRequestWithEffort("fixture", msgs, defs, true, ""); err != nil {
					b.Fatal(err)
				}
				for _, tc := range []struct {
					name    string
					prepare func(codexRequest, string, bool, Sampling) ([]byte, error)
				}{
					{"original", prepareOwnedStandardResponsesRequest},
					{"canonical", prepareAssembledStandardResponsesRequest},
				} {
					b.Run(tc.name, func(b *testing.B) {
						b.ReportAllocs()
						b.ResetTimer()
						for n := 0; n < b.N; n++ {
							req, err := assembleCodexRequestWithEffort("fixture", msgs, defs, true, "")
							if err != nil {
								b.Fatal(err)
							}
							body, err := tc.prepare(req, "fixed-cache", false, Sampling{})
							if err != nil {
								b.Fatal(err)
							}
							canonicalResponsesBenchmarkBody = body
						}
					})
				}
			})
		}
	}
}

// This exercises the unchanged public Complete dispatch with a synthetic SSE
// transport. For an end-to-end A/B, the baseline overlay changes only the two
// production prepare callsites back to prepareOwnedStandardResponsesRequest.
func BenchmarkStandardResponsesCanonicalComplete(b *testing.B) {
	for _, count := range []int{0, 3, 30} {
		for _, size := range []int{16, 128 << 10} {
			b.Run(fmt.Sprintf("tools=%d/history=%d", count, size), func(b *testing.B) {
				msgs := canonicalResponsesHistory(size)
				defs := canonicalResponsesToolDefs(count)
				transport := responsesRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					if _, err := io.Copy(io.Discard, req.Body); err != nil {
						return nil, err
					}
					return canonicalResponsesCompletedResponse(req), nil
				})
				p, err := NewResponses(ResponsesConfig{BaseURL: "https://fixture.invalid/v1", Model: "fixture", HTTPClient: &http.Client{Transport: transport}})
				if err != nil {
					b.Fatal(err)
				}
				p.inner.cfg.PromptCacheKey = "fixed-cache"
				ctx := context.Background()
				ch, err := p.Complete(ctx, msgs, defs)
				if err != nil {
					b.Fatal(err)
				}
				for delta := range ch {
					if delta.Err != nil {
						b.Fatal(delta.Err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					ch, err := p.Complete(ctx, msgs, defs)
					if err != nil {
						b.Fatal(err)
					}
					for delta := range ch {
						if delta.Err != nil {
							b.Fatal(delta.Err)
						}
					}
				}
			})
		}
	}
}
