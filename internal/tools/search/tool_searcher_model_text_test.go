package search

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

const discoveryContractSchema = `{
	"type":"object",
	"properties":{
		"mode":{"type":"string","enum":["two  spaces","quoted \"text\""],"default":"two  spaces","description":"Keep  spaces, newlines\nand Unicode: zażółć"},
		"count":{"type":"integer","minimum":1,"maximum":3,"default":2},
		"options":{"type":"object","properties":{"enabled":{"type":"boolean","default":false}},"required":["enabled"],"additionalProperties":false}
	},
	"required":["mode","count"],
	"additionalProperties":false,
	"examples":[9007199254740993,1e+09,-0]
}`

func TestToolSearcherModelTextPreservesCompleteResponse(t *testing.T) {
	reg := NewRegistry()
	for _, spec := range []Tool{
		{Name: "contract_tool", Description: "Contract fixture", Schema: discoveryContractSchema},
		{Name: "empty_tool", Description: "Empty contract fixture"},
		{Name: "boolean_tool", Description: "Boolean contract fixture", Schema: "true"},
	} {
		spec.Fn = func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "ok"}, nil }
		reg.MustRegister(spec)
	}
	result, err := NewToolSearcher(reg, nil).execute(context.Background(), json.RawMessage(`{"query":"contract_tool, empty_tool, boolean_tool"}`))
	if err != nil || result.Err != nil {
		t.Fatalf("discovery failed: %v / %v", err, result.Err)
	}
	if result.ModelText == "" || len(result.ModelText) >= len(result.Text) {
		t.Fatalf("no smaller complete model view: public=%d model=%d", len(result.Text), len(result.ModelText))
	}
	assertDiscoveryModelEquivalent(t, result.Text, result.ModelText)
	var public discoveryResponse
	if err := json.Unmarshal([]byte(result.Text), &public); err != nil {
		t.Fatalf("public schema strings changed: %v", err)
	}
	if public.Matches[0].Schema != compactDiscoverySchema(discoveryContractSchema) || public.Matches[1].Schema != "" || public.Matches[2].Schema != "true" {
		t.Fatal("public schema contracts or match order changed")
	}
	if !strings.Contains(result.ModelText, `[9007199254740993,1e+09,-0]`) {
		t.Fatal("model view changed exact numeric spellings")
	}
	if got := core.NewOutputStore().ModelContent("tool_search", result); got != result.ModelText {
		t.Fatal("small complete result did not use model representation")
	}
	original, _ := reg.Get("contract_tool")
	if original.Schema != discoveryContractSchema {
		t.Fatal("registered schema changed")
	}
}

func assertDiscoveryModelEquivalent(t *testing.T, public, model string) {
	t.Helper()
	decode := func(text string) map[string]any {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var value map[string]any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before, after := decode(public), decode(model)
	for _, rawMatch := range before["matches"].([]any) {
		match := rawMatch.(map[string]any)
		schema := match["schema"].(string)
		if json.Valid([]byte(schema)) {
			decoder := json.NewDecoder(strings.NewReader(schema))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			match["schema"] = value
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("model view lost discovery metadata or schema fields:\n%v\n%v", before, after)
	}
}

func TestToolSearcherModelTextLeavesLargeAndErrorResultsAlone(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(Tool{Name: "large_contract", Description: "large fixture", Schema: `{"type":"object","description":"` + strings.Repeat("x", core.ModelOutputInlineBytes) + `"}`, Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }})
	search := NewToolSearcher(reg, nil)
	result, err := search.execute(context.Background(), json.RawMessage(`{"query":"large_contract"}`))
	if err != nil || result.Err != nil || len(result.Text) <= core.ModelOutputInlineBytes || result.ModelText != "" {
		t.Fatalf("large result bypassed original retention path: %v / %v, public=%d model=%d", err, result.Err, len(result.Text), len(result.ModelText))
	}
	view := core.NewOutputStore().ModelContent("tool_search", result)
	if !strings.Contains(view, "[large tool output:") || !strings.Contains(view, "read_output") {
		t.Fatal("large result no longer uses retained original")
	}
	for _, args := range []string{"{", `{"query":""}`} {
		result, err := search.execute(context.Background(), json.RawMessage(args))
		if err != nil || result.Err == nil || result.ModelText != "" {
			t.Fatalf("error projected as success: %v / %+v", err, result)
		}
	}
	result, err = search.execute(context.Background(), json.RawMessage(`{"query":"absent_zzqq_9283"}`))
	if err != nil || result.Err != nil || result.ModelText != "" {
		t.Fatal("no-match response should retain its public representation")
	}
}

func TestDiscoveryModelTextPreservesMalformedSchemaStrings(t *testing.T) {
	for _, schema := range []string{"", "{broken", "{} trailing"} {
		response := discoveryResponse{Query: "fixture", Hint: "same hint", Matches: []discoveryMatch{
			{Name: "valid", Schema: `{"type":"object","properties":{"x":{"type":"string","default":"x"}}}`},
			{Name: "fallback", Schema: schema},
		}}
		public, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		model := discoveryModelText(response, string(public))
		if model == "" {
			t.Fatal("valid sibling lost its model projection")
		}
		assertDiscoveryModelEquivalent(t, string(public), model)
	}
	response := discoveryResponse{Matches: []discoveryMatch{{Schema: "{broken"}}}
	public, _ := json.Marshal(response)
	if got := discoveryModelText(response, string(public)); got != "" {
		t.Fatal("a response without convertible schemas should stay unchanged")
	}
}
