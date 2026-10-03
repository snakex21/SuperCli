package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func standardResponsesLegacyFixture(model string, msgs []Message, tools []ToolDef, vision bool, effort, key string, reasoningModel bool, sampling Sampling) ([]byte, error) {
	body, err := buildCodexRequestWithEffort(model, msgs, tools, vision, effort)
	if err != nil {
		return nil, err
	}
	return prepareStandardResponsesRequest(body, key, reasoningModel, sampling)
}

func TestStandardResponsesOwnedStandardExactParity(t *testing.T) {
	texts := []string{"", "hi", "polski ąćęół; 日本語; 😀", "<&>\\\"\n\u2028", string([]byte{'x', 0xff, 0xfe, 'y'})}
	floatp := func(v float64) *float64 { return &v }
	samples := []Sampling{{}, {Temperature: floatp(0), TopP: floatp(.7)}, {Temperature: floatp(math.NaN())}, {TopP: floatp(math.Inf(1))}}
	tools := []ToolDef{{Name: "read_fixture", Description: "Read synthetic bytes", Schema: `{"type":"object","properties":{"path":{"type":"string","default":"<&>"},"number":{"type":"number","default":9007199254740993}},"required":["path"]}`}}
	count := 0
	for _, text := range texts {
		for _, effort := range []string{"", "none", "max"} {
			for _, reasoning := range []bool{false, true} {
				for _, sample := range samples {
					for _, defs := range [][]ToolDef{nil, tools} {
						msgs := []Message{{Role: RoleSystem, Content: "system <&>"}, {Role: RoleUser, Content: text}, {Role: RoleAssistant, Content: text, ToolCalls: []ToolCall{{ID: "call_fixture", Name: "read_fixture", Arguments: `{"path":"fixture.txt"}`}}}, {Role: RoleTool, Name: "read_fixture", ToolCallID: "call_fixture", Content: text}, {Role: RoleSystem, Content: "later system"}, {Role: RoleUser, Content: "next"}}
						want, we := standardResponsesLegacyFixture("fixture", msgs, defs, true, effort, " cache ", reasoning, sample)
						got, ge := buildOwnedStandardResponsesFixture("fixture", msgs, defs, true, effort, " cache ", reasoning, sample)
						if fmt.Sprint(we) != fmt.Sprint(ge) || !bytes.Equal(want, got) {
							t.Fatalf("fixture %d mismatch errors: %v/%v bytes=%d/%d", count, we, ge, len(want), len(got))
						}
						count++
					}
				}
			}
		}
	}
	for _, msgs := range [][]Message{nil, {}, {{Role: RoleSystem, Content: "only instruction"}}} {
		want, we := standardResponsesLegacyFixture("fixture", msgs, nil, false, "", "", false, Sampling{})
		got, ge := buildOwnedStandardResponsesFixture("fixture", msgs, nil, false, "", "", false, Sampling{})
		if fmt.Sprint(we) != fmt.Sprint(ge) || !bytes.Equal(want, got) {
			t.Fatal("nil input differs")
		}
	}
	t.Logf("matched %d normal text/tool/sampling fixtures", count)
}

func TestStandardResponsesOwnedNativeAndSchemaParity(t *testing.T) {
	for _, raw := range []string{
		`{"type":"reasoning","encrypted_content":"opaque+/=<&>","summary":[],"counter":9007199254740993,"zero":-0,"exponent":1.2e3}`,
		`{"type":"reasoning","counter":true,"counter":false,"summary":[{"type":"summary_text","text":"plan"}],"future":{"opaque":"\\uD800","x":1e-3}}`,
		`{"type":"reasoning","counter":1e999}`, `{"type":"reasoning","unfinished":`, `null`, `[]`,
		"{\"type\":\"reasoning\",\"encrypted_content\":\"x" + string([]byte{0xff}) + "y\"}",
	} {
		block := nativeReasoning(ReasoningResponses, "fixture", "https://fixture.invalid/v1", []byte(raw))
		msgs := []Message{{Role: RoleUser, Content: "synthetic"}, {Role: RoleAssistant, Content: "answer", Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: block}, {Type: PartTypeText, Text: "answer"}}}, {Role: RoleUser, Content: "continue"}}
		before, _ := json.Marshal(msgs)
		want, we := standardResponsesLegacyFixture("fixture", msgs, nil, true, "max", "cache", true, Sampling{})
		got, ge := buildOwnedStandardResponsesFixture("fixture", msgs, nil, true, "max", "cache", true, Sampling{})
		after, _ := json.Marshal(msgs)
		if fmt.Sprint(we) != fmt.Sprint(ge) || !bytes.Equal(want, got) {
			t.Fatal("native parity differs")
		}
		if !bytes.Equal(before, after) || string(block.Data) != raw {
			t.Fatal("mutated native history")
		}
	}
	for _, schema := range []string{"", `null`, `[]`, `7`, `{"type":"object","required":[7]}`, `{"type":"object","broken":`, `{"type":"object","x":1e999}`} {
		defs := []ToolDef{{Name: "fixture", Schema: schema}}
		want, we := standardResponsesLegacyFixture("fixture", []Message{{Role: RoleUser, Content: "hi"}}, defs, false, "", "cache", false, Sampling{})
		got, ge := buildOwnedStandardResponsesFixture("fixture", []Message{{Role: RoleUser, Content: "hi"}}, defs, false, "", "cache", false, Sampling{})
		if fmt.Sprint(we) != fmt.Sprint(ge) || !bytes.Equal(want, got) {
			t.Fatal("schema error/body differs")
		}
	}
	msgs := []Message{{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeText, Text: "image fixture"}, {Type: PartTypeImage, Image: &ImageRef{URL: "https://fixture.invalid/image.png"}}}}}
	for _, vision := range []bool{false, true} {
		want, we := standardResponsesLegacyFixture("fixture", msgs, nil, vision, "", "cache", false, Sampling{})
		got, ge := buildOwnedStandardResponsesFixture("fixture", msgs, nil, vision, "", "cache", false, Sampling{})
		if fmt.Sprint(we) != fmt.Sprint(ge) || !bytes.Equal(want, got) {
			t.Fatal("image/fallback differs")
		}
	}
}

func TestStandardResponsesOwnedFakeWire(t *testing.T) {
	msgs := []Message{{Role: RoleSystem, Content: "synthetic system"}, {Role: RoleUser, Content: "synthetic user"}, {Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_fixture", Name: "fixture", Arguments: `{"path":"synthetic"}`}}}, {Role: RoleTool, Name: "fixture", ToolCallID: "call_fixture", Content: "fixture output"}, {Role: RoleUser, Content: "continue"}}
	defs := []ToolDef{{Name: "fixture", Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`}}
	for _, base := range []string{"https://fixture.invalid/v1", "https://opencode.ai/zen/v1"} {
		var actual []byte
		transport := responsesRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var err error
			actual, err = io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{}}\n\n")), Request: req}, nil
		})
		p, err := NewResponses(ResponsesConfig{BaseURL: base, Model: "fixture", HTTPClient: &http.Client{Transport: transport}})
		if err != nil {
			t.Fatal(err)
		}
		p.inner.cfg.PromptCacheKey = "fixed-cache"
		ctx := WithOpenCodeSession(context.Background(), "sess-fixture-fixed")
		ch, err := p.Complete(ctx, msgs, defs)
		if err != nil {
			t.Fatal(err)
		}
		for delta := range ch {
			if delta.Err != nil {
				t.Fatal(delta.Err)
			}
		}
		want, err := buildCodexRequestWithEffort(p.inner.cfg.Model, filterNativeReasoning(msgs, ReasoningResponses, p.inner.cfg.Model, p.inner.cfg.BackendURL), defs, true, p.inner.reasoningEffort())
		if err != nil {
			t.Fatal(err)
		}
		if isOpenCodeZenBaseURL(base) {
			want, err = prepareOpenCodeZenResponsesRequest(want, "sess-fixture-fixed")
		} else {
			want, err = prepareStandardResponsesRequest(want, p.inner.cfg.PromptCacheKey, p.inner.supportsReasoningControl(), p.inner.sampling)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, actual) {
			t.Fatalf("wire mismatch base=%s bytes=%d/%d", base, len(want), len(actual))
		}
	}
}

func TestStandardResponsesOwnedCompleteErrorParity(t *testing.T) {
	for _, kind := range []string{"schema", "native-number", "sampling"} {
		msgs := []Message{{Role: RoleUser, Content: "synthetic"}}
		defs := []ToolDef(nil)
		if kind == "schema" {
			defs = []ToolDef{{Name: "fixture", Schema: `{"type":"object","unfinished":`}}
		}
		if kind == "native-number" {
			msgs = append(msgs, Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: nativeReasoning(ReasoningResponses, "fixture", "https://fixture.invalid/v1", []byte(`{"type":"reasoning","extra":1e999}`))}}})
		}
		called := false
		p, err := NewResponses(ResponsesConfig{BaseURL: "https://fixture.invalid/v1", Model: "fixture", HTTPClient: &http.Client{Transport: responsesRoundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, fmt.Errorf("unexpected transport")
		})}})
		if err != nil {
			t.Fatal(err)
		}
		if kind == "sampling" {
			v := math.NaN()
			p.inner.sampling = Sampling{Temperature: &v}
		}
		filtered := filterNativeReasoning(msgs, ReasoningResponses, p.inner.cfg.Model, p.inner.cfg.BackendURL)
		body, expected := buildCodexRequestWithEffort(p.inner.cfg.Model, filtered, defs, true, p.inner.reasoningEffort())
		if expected != nil {
			expected = fmt.Errorf("build request: %w", expected)
		} else {
			_, expected = prepareStandardResponsesRequest(body, p.inner.cfg.PromptCacheKey, p.inner.supportsReasoningControl(), p.inner.sampling)
			if expected != nil {
				expected = fmt.Errorf("build standard responses request: %w", expected)
			}
		}
		_, actual := p.Complete(context.Background(), msgs, defs)
		if expected == nil || fmt.Sprint(expected) != fmt.Sprint(actual) || called {
			t.Fatalf("failure %s changed: expected=%v actual=%v called=%v", kind, expected, actual, called)
		}
	}
}

func BenchmarkStandardResponsesOwnedCompleteAssembly(b *testing.B) {
	for _, size := range []int{16, 128 << 10, 1 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			text := strings.Repeat("synthetic ordinary text <&> ąćę 日本語\n", size/42+1)
			msgs := []Message{{Role: RoleSystem, Content: "synthetic instruction"}, {Role: RoleUser, Content: text}, {Role: RoleAssistant, Content: text}, {Role: RoleUser, Content: "continue"}}
			defs := []ToolDef{{Name: "fixture", Description: "synthetic", Schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`}}
			transport := responsesRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				_, err := io.Copy(io.Discard, req.Body)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{}}\n\n")), Request: req}, nil
			})
			p, err := NewResponses(ResponsesConfig{BaseURL: "https://fixture.invalid/v1", Model: "fixture", HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				b.Fatal(err)
			}
			p.inner.cfg.PromptCacheKey = "fixed-cache"
			ctx := context.Background()
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

func TestStandardResponsesOwnedMirroredFields(t *testing.T) {
	for _, tc := range []struct {
		value  any
		fields []string
	}{
		{codexRequest{}, []string{"Model:string:model", "Instructions:string:instructions,omitempty", "Input:[]llm.codexItem:input", "Tools:[]llm.codexToolDecl:tools,omitempty", "ToolChoice:string:tool_choice", "ParallelToolCalls:bool:parallel_tool_calls", "Store:bool:store", "Stream:bool:stream", "Include:[]string:include", "Reasoning:*llm.codexReasoning:reasoning,omitempty"}},
		{codexItem{}, []string{"Raw:json.RawMessage:-", "Type:string:type", "Role:string:role,omitempty", "Content:[]llm.codexContentPart:content,omitempty", "Name:string:name,omitempty", "Arguments:string:arguments,omitempty", "CallID:string:call_id,omitempty", "Output:string:output,omitempty"}},
		{codexContentPart{}, []string{"Type:string:type", "Text:string:text,omitempty", "ImageURL:string:image_url,omitempty"}},
		{codexToolDecl{}, []string{"Type:string:type", "Name:string:name", "Description:string:description,omitempty", "Strict:bool:strict", "Parameters:json.RawMessage:parameters,omitempty"}},
		{codexReasoning{}, []string{"Effort:string:effort,omitempty", "Summary:string:summary,omitempty"}},
	} {
		var fields []string
		typ := reflect.TypeOf(tc.value)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			fields = append(fields, f.Name+":"+f.Type.String()+":"+f.Tag.Get("json"))
		}
		if !reflect.DeepEqual(fields, tc.fields) {
			t.Fatalf("%s wire fields/type/full JSON tags changed; update map parity guards\ngot=%v\nwant=%v", typ.Name(), fields, tc.fields)
		}
	}
}

func TestStandardResponsesOwnedErrorPriority(t *testing.T) {
	for _, kind := range []string{"schema-before-image-native", "image-build-before-native-prepare", "native-before-sampling"} {
		image := &ImageRef{URL: "https://fixture.invalid/image.png"}
		if kind != "native-before-sampling" {
			image = &ImageRef{Path: filepath.Join(t.TempDir(), "missing.png"), MediaType: "image/png"}
		}
		msgs := []Message{{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeText, Text: "synthetic"}, {Type: PartTypeImage, Image: image}}}, {Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: nativeReasoning(ReasoningResponses, "fixture", "https://fixture.invalid/v1", []byte(`{"type":"reasoning","extra":1e999}`))}}}}
		defs := []ToolDef(nil)
		if kind == "schema-before-image-native" {
			defs = []ToolDef{{Name: "fixture", Schema: `{"type":"object","unfinished":`}}
		}
		called := false
		p, err := NewResponses(ResponsesConfig{BaseURL: "https://fixture.invalid/v1", Model: "fixture", HTTPClient: &http.Client{Transport: responsesRoundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, fmt.Errorf("unexpected transport")
		})}})
		if err != nil {
			t.Fatal(err)
		}
		v := math.NaN()
		p.inner.sampling = Sampling{Temperature: &v}
		filtered := filterNativeReasoning(msgs, ReasoningResponses, p.inner.cfg.Model, p.inner.cfg.BackendURL)
		body, expected := buildCodexRequestWithEffort(p.inner.cfg.Model, filtered, defs, true, p.inner.reasoningEffort())
		if expected != nil {
			expected = fmt.Errorf("build request: %w", expected)
		} else {
			_, expected = buildCodexRequestWithEffort(p.inner.cfg.Model, filtered, defs, false, p.inner.reasoningEffort())
			if expected != nil {
				expected = fmt.Errorf("build image fallback request: %w", expected)
			} else {
				_, expected = prepareStandardResponsesRequest(body, p.inner.cfg.PromptCacheKey, p.inner.supportsReasoningControl(), p.inner.sampling)
				if expected != nil {
					expected = fmt.Errorf("build standard responses request: %w", expected)
				}
			}
		}
		_, actual := p.Complete(context.Background(), msgs, defs)
		if expected == nil || fmt.Sprint(expected) != fmt.Sprint(actual) || called {
			t.Fatalf("priority %s changed: expected=%v actual=%v called=%v", kind, expected, actual, called)
		}
	}
}

func TestStandardResponsesOwnedLargePublicBodyParity(t *testing.T) {
	previousEffort := ReasoningEffort()
	_ = SetReasoningEffort("")
	t.Cleanup(func() { _ = SetReasoningEffort(previousEffort) })
	goldens := map[int]string{16: "556895e590fb542bf204f251481f42c02efc5cbaf233372a5e1e77968885eb7e", 128 << 10: "496e5992ec959991f7e95ce37a56847837cf1d4fe5945cb5fc1a73163ef17f3a", 1 << 20: "48c3784dfe726ac0045d3151257554701a6e6fd73e7706256fbbf299ba472e30"}
	for _, size := range []int{16, 128 << 10, 1 << 20} {
		text := strings.Repeat("synthetic ordinary text <&> ąćę 日本語\n", size/42+1)
		msgs := []Message{{Role: RoleSystem, Content: "synthetic instruction"}, {Role: RoleUser, Content: text}, {Role: RoleAssistant, Content: text}, {Role: RoleUser, Content: "continue"}}
		defs := []ToolDef{{Name: "fixture", Description: "synthetic", Schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`}}
		var actual []byte
		transport := responsesRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var err error
			actual, err = io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{}}\n\n")), Request: req}, nil
		})
		p, err := NewResponses(ResponsesConfig{BaseURL: "https://fixture.invalid/v1", Model: "fixture", HTTPClient: &http.Client{Transport: transport}})
		if err != nil {
			t.Fatal(err)
		}
		p.inner.cfg.PromptCacheKey = "fixed-cache"
		want, err := standardResponsesLegacyFixture(p.inner.cfg.Model, msgs, defs, true, p.inner.reasoningEffort(), p.inner.cfg.PromptCacheKey, p.inner.supportsReasoningControl(), p.inner.sampling)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := p.Complete(context.Background(), msgs, defs)
		if err != nil {
			t.Fatal(err)
		}
		for delta := range ch {
			if delta.Err != nil {
				t.Fatal(delta.Err)
			}
		}
		if !bytes.Equal(want, actual) {
			t.Fatalf("large body %d changed", size)
		}
		if hash := fmt.Sprintf("%x", sha256.Sum256(actual)); hash != goldens[size] {
			t.Fatalf("old public body golden changed for size=%d", size)
		}
		t.Logf("fixture=%d text_bytes_each=%d request_bytes=%d sha256=%x", size, len(text), len(actual), sha256.Sum256(actual))
	}
}

func buildOwnedStandardResponsesFixture(model string, msgs []Message, tools []ToolDef, vision bool, effort, key string, reasoningModel bool, sampling Sampling) ([]byte, error) {
	req, err := assembleCodexRequestWithEffort(model, msgs, tools, vision, effort)
	if err != nil {
		return nil, err
	}
	return prepareOwnedStandardResponsesRequest(req, key, reasoningModel, sampling)
}
