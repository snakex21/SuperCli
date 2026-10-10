package llm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func providerRuntimeModels(t *testing.T, body string) ([]ModelInfo, map[string]ModelInfo) {
	t.Helper()
	models, err := parseProviderModelInfos([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]ModelInfo, len(models))
	for _, model := range models {
		if _, duplicate := byID[model.ID]; duplicate {
			t.Fatalf("duplicate model ID %q in %#v", model.ID, models)
		}
		byID[model.ID] = model
	}
	return models, byID
}

func TestProviderRuntimeContextV0SeparateFromCatalog(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		catalog int
		runtime int
	}{
		{"small", `{"data":[{"id":"arbitrary-small","max_context_length":262144,"loaded_context_length":4096}]}`, 262144, 4096},
		{"extended", `{"data":[{"id":"arbitrary-small","max_context_length":16384,"loaded_context_length":262144}]}`, 16384, 262144},
		{"metadata-priority", `{"data":[{"id":"arbitrary-small","context_length":96000,"max_context_length":131072,"context_window":64000,"loaded_context_length":8192}]}`, 96000, 8192},
		{"metadata-fallback", `{"data":[{"id":"arbitrary-small","context_length":"invalid","max_context_length":131072,"loaded_context_length":2048}]}`, 131072, 2048},
		{"runtime-without-max", `{"data":[{"id":"arbitrary-small","loaded_context_length":1024}]}`, 0, 1024},
		{"missing-runtime", `{"data":[{"id":"arbitrary-small","max_context_length":131072}]}`, 131072, 0},
		{"no-context-from-name", `{"data":[{"id":"Qwen3-1M-reasoning-vision"}]}`, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			models, _ := providerRuntimeModels(t, tc.body)
			if len(models) != 1 || models[0].ContextLength != tc.catalog || models[0].RuntimeContextLength != tc.runtime {
				t.Fatalf("models = %#v; want catalog=%d runtime=%d", models, tc.catalog, tc.runtime)
			}
		})
	}
}

func TestProviderRuntimeContextV1ExactAliasesKeepCapabilities(t *testing.T) {
	models, byID := providerRuntimeModels(t, `{"models":[{
		"key":"arbitrary/family","type":"llm","max_context_length":131072,
		"capabilities":{"vision":true,"trained_for_tool_use":true,"reasoning":false},
		"loaded_instances":[
			{"id":"dall-e-3-qwen3-reasoning-alias","config":{"context_length":32768}},
			{"id":"CaseSensitive/Other","config":{"context_length":4096}},
			{"id":" opaque alias ","config":{"context_length":262144}}
		]
	}]}`)
	if len(models) != 4 {
		t.Fatalf("models = %#v; want base plus three exact instance IDs", models)
	}
	base := byID["arbitrary/family"]
	if base.ContextLength != 131072 || base.RuntimeContextLength != 4096 {
		t.Fatalf("base = %#v; want catalog 131072 and minimum loaded capacity 4096", base)
	}
	if !base.Vision || !base.VisionKnown || !base.ToolUse || base.Reasoning || !base.ReasoningKnown {
		t.Fatalf("authoritative capabilities were changed: %#v", base)
	}
	for _, instance := range []struct {
		id      string
		context int
	}{
		{"dall-e-3-qwen3-reasoning-alias", 32768},
		{"CaseSensitive/Other", 4096},
		{" opaque alias ", 262144},
	} {
		got, ok := byID[instance.id]
		if !ok {
			t.Fatalf("exact loaded instance %q missing", instance.id)
		}
		want := base
		want.ID, want.RuntimeContextLength = instance.id, instance.context
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("instance %q = %#v; want unchanged metadata clone %#v", instance.id, got, want)
		}
	}
}

func TestProviderRuntimeContextV1ExactBaseIDWinsOverMinimum(t *testing.T) {
	models, byID := providerRuntimeModels(t, `{"models":[{
		"key":"arbitrary-main","max_context_length":131072,
		"loaded_context_length":1024,
		"capabilities":{"vision":false,"reasoning":{"allowed_options":["off","on"]}},
		"loaded_instances":[
			{"id":"arbitrary-main","config":{"context_length":32768}},
			{"id":"arbitrary-small","config":{"context_length":4096}}
		]
	}]}`)
	if len(models) != 2 {
		t.Fatalf("models = %#v; base and matching instance ID must be deduplicated", models)
	}
	base := byID["arbitrary-main"]
	if base.RuntimeContextLength != 32768 || base.ContextLength != 131072 {
		t.Fatalf("exact base ID lost its own runtime capacity: %#v", base)
	}
	if !base.Reasoning || !base.ReasoningKnown || !base.ReasoningToggleOnly || base.Vision || !base.VisionKnown {
		t.Fatalf("reasoning/modality metadata changed: %#v", base)
	}
	if got := byID["arbitrary-small"].RuntimeContextLength; got != 4096 {
		t.Fatalf("other exact instance runtime = %d, want 4096", got)
	}
}

func TestProviderRuntimeContextV1DeduplicatesExactIDs(t *testing.T) {
	models, byID := providerRuntimeModels(t, `{"models":[{
		"key":"base","max_context_length":131072,
		"loaded_instances":[
			{"id":"same","config":{"context_length":32768}},
			{"id":"same","config":{"context_length":16384}},
			{"id":"SAME","config":{"context_length":65536}}
		]
	},{
		"key":"base","max_context_length":262144,
		"loaded_instances":[{"id":"same","config":{"context_length":8192}}]
	}]}`)
	if len(models) != 3 || byID["same"].RuntimeContextLength != 8192 || byID["SAME"].RuntimeContextLength != 65536 {
		t.Fatalf("ID deduplication lost exact case-sensitive capacities: %#v", models)
	}
	if byID["base"].RuntimeContextLength != 8192 || byID["base"].ContextLength != 131072 {
		t.Fatalf("duplicate base records must retain first metadata and minimum positive fallback: %#v", byID["base"])
	}
}

func TestProviderRuntimeContextExactAliasPrecedenceAcrossRows(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		aliasRow := `{"key":"alias","max_context_length":65536,"loaded_instances":[{"id":"small","config":{"context_length":1024}}]}`
		instanceRow := `{"key":"owner","max_context_length":131072,"loaded_instances":[{"id":"alias","config":{"context_length":32768}}]}`
		rows := aliasRow + "," + instanceRow
		if reversed {
			rows = instanceRow + "," + aliasRow
		}
		models, byID := providerRuntimeModels(t, `{"models":[`+rows+`]}`)
		if len(models) != 3 || byID["alias"].RuntimeContextLength != 32768 {
			t.Fatalf("reversed=%v: exact alias capacity did not beat ambiguous key fallback: %#v", reversed, models)
		}
	}
}

func TestProviderRuntimeContextMalformedValuesAreIgnored(t *testing.T) {
	for _, raw := range []string{`null`, `0`, `-4096`, `"32768"`, `true`, `{}`, `[]`, `4096.5`, `1e100`} {
		t.Run(raw, func(t *testing.T) {
			body := fmt.Sprintf(`{"models":[{"key":"arbitrary","max_context_length":131072,"loaded_context_length":%s,"loaded_instances":[{"id":"bad-instance","config":{"context_length":%s}}]}]}`, raw, raw)
			models, _ := providerRuntimeModels(t, body)
			if len(models) != 1 || models[0].ContextLength != 131072 || models[0].RuntimeContextLength != 0 {
				t.Fatalf("malformed runtime %s affected metadata or produced an alias: %#v", raw, models)
			}
		})
	}
	for _, raw := range []string{`null`, `true`, `"bad"`, `{}`, `23`} {
		body := fmt.Sprintf(`{"models":[{"key":"arbitrary","loaded_context_length":2048,"loaded_instances":%s}]}`, raw)
		models, _ := providerRuntimeModels(t, body)
		if len(models) != 1 || models[0].RuntimeContextLength != 2048 {
			t.Fatalf("malformed instance list %s lost valid v0 capacity: %#v", raw, models)
		}
	}
	models, byID := providerRuntimeModels(t, `{"models":[{
		"key":"arbitrary","max_context_length":131072,
		"loaded_instances":[
			null, false, 23, "bad", [],
			{"id":23,"config":{"context_length":128}},
			{"config":{"context_length":128}},
			{"id":"   ","config":{"context_length":128}},
			{"id":"missing-config"},
			{"id":"wrong-config","config":[]},
			{"id":"empty-config","config":{}},
			{"id":"valid","config":{"context_length":8192}}
		]
	}]}`)
	if len(models) != 2 || byID["arbitrary"].RuntimeContextLength != 8192 || byID["valid"].RuntimeContextLength != 8192 {
		t.Fatalf("malformed instances must not hide valid sibling metadata: %#v", models)
	}
}

func TestProviderRuntimeContextPositiveIntegralNumbers(t *testing.T) {
	for _, raw := range []string{`4096`, `4096.0`, `4.096e3`, fmt.Sprint(int(^uint(0) >> 1))} {
		var want int
		if raw == fmt.Sprint(int(^uint(0)>>1)) {
			want = int(^uint(0) >> 1)
		} else {
			want = 4096
		}
		body := fmt.Sprintf(`{"data":[{"id":"arbitrary","loaded_context_length":%s}]}`, raw)
		models, _ := providerRuntimeModels(t, body)
		if len(models) != 1 || models[0].RuntimeContextLength != want {
			t.Fatalf("integral number %s = %#v; want runtime=%d", raw, models, want)
		}
	}
}

func TestRuntimeContextLengthDoesNotPersistToJSON(t *testing.T) {
	original := ModelInfo{ID: "arbitrary", ContextLength: 131072, RuntimeContextLength: 4096, Vision: true, VisionKnown: true, ToolUse: true, Reasoning: true, ReasoningKnown: true, Source: SourceProvider}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "runtime") || strings.Contains(string(raw), "RuntimeContextLength") || strings.Contains(string(raw), "loaded_context") {
		t.Fatalf("runtime-only capacity leaked into catalog JSON: %s", raw)
	}
	var reloaded ModelInfo
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.RuntimeContextLength != 0 || reloaded.ContextLength != original.ContextLength || reloaded.Capabilities() != original.Capabilities() {
		t.Fatalf("persisted metadata changed or retained stale runtime capacity: %#v", reloaded)
	}
	if err := json.Unmarshal([]byte(`{"id":"arbitrary","context_length":131072,"RuntimeContextLength":32768,"runtime_context_length":32768}`), &reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.RuntimeContextLength != 0 {
		t.Fatalf("catalog JSON supplied runtime capacity: %#v", reloaded)
	}
}
