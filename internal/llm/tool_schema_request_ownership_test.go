package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func TestToolSchemaRequestBorrowPreservesOwnedNormalizers(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	for _, mode := range []toolSchemaMode{toolSchemaFull, toolSchemaPortable, toolSchemaAnthropic} {
		for _, raw := range []string{
			"", `{}`, ` {"path":{"type":"string"}} `,
			`{"type":null,"properties":{"nested":{"anyOf":[{"type":"string"},{"type":"integer"}]}}}`,
			`{"oneOf":[{"properties":{"a":{"const":"<&>"}},"required":["a"]},{"properties":{"b":{"type":"integer"}},"required":["b"]}]}`,
			`{"type":"object","properties":{"x":{"default":9007199254740993}},"title":"Zażółć"}`,
			`null`, `[]`, `{bad json`,
		} {
			borrowed, err := toolSchemaForRequest(raw, mode)
			owned, ownedErr := cachedToolSchema(raw, mode)
			if fmt.Sprint(err) != fmt.Sprint(ownedErr) || !bytes.Equal(borrowed, owned) {
				t.Fatalf("mode=%d raw=%q: borrowed=%q/%v owned=%q/%v", mode, raw, borrowed, err, owned, ownedErr)
			}
			if err != nil {
				continue
			}
			want := bytes.Clone(borrowed)
			owned[0] = '['
			again, err := cachedToolSchema(raw, mode)
			if err != nil || !bytes.Equal(borrowed, want) || !bytes.Equal(again, want) {
				t.Fatalf("owned mutation leaked into request/cache mode=%d: %v", mode, err)
			}
		}
	}
	compiled, err := CompileToolSchema(schemaResidencySmall)
	if err != nil {
		t.Fatal(err)
	}
	compiled.Full[0], compiled.Portable[0] = '[', '['
	again, err := CompileToolSchema(schemaResidencySmall)
	if err != nil || again.Full[0] != '{' || again.Portable[0] != '{' {
		t.Fatalf("exported compiled schema lost copy ownership: %v", err)
	}
}

func TestToolSchemaRequestBorrowSurvivesEvictionAndConcurrentOwnedMutation(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	for _, mode := range []toolSchemaMode{toolSchemaPortable, toolSchemaAnthropic} {
		borrowed, err := toolSchemaForRequest(schemaResidencySmall, mode)
		if err != nil {
			t.Fatal(err)
		}
		want := bytes.Clone(borrowed)
		var wg sync.WaitGroup
		for worker := 0; worker < 8; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				for i := 0; i < 80; i++ {
					owned, err := cachedToolSchema(schemaResidencySmall, mode)
					if err != nil {
						t.Error(err)
						return
					}
					owned[0] = '['
					if _, err := cachedToolSchema(schemaResidencyLarge(worker*80+i, 0), mode); err != nil {
						t.Error(err)
						return
					}
					body, err := json.Marshal(struct {
						Schema json.RawMessage `json:"schema"`
					}{borrowed})
					if err != nil || !bytes.Equal(body, append(append([]byte(`{"schema":`), want...), '}')) {
						t.Errorf("concurrent encoding changed borrowed schema: %q / %v", body, err)
						return
					}
				}
			}(worker)
		}
		wg.Wait()
		// Force eviction without touching the request's held reference.
		for i := 0; i < toolSchemaCacheLimit+1; i++ {
			if _, err := toolSchemaForRequest(schemaResidencyLarge(i+10_000, 0), mode); err != nil {
				t.Fatal(err)
			}
		}
		normalizedToolSchemaCache.Lock()
		_, resident := normalizedToolSchemaCache.entries[toolSchemaCacheKey{mode: mode, raw: schemaResidencySmall}]
		normalizedToolSchemaCache.Unlock()
		if resident || !bytes.Equal(borrowed, want) || !json.Valid(borrowed) {
			t.Fatalf("eviction mutated held schema or failed to evict: resident=%v", resident)
		}
	}
}

func TestToolSchemaRequestBodiesRemainIndependent(t *testing.T) {
	tools := []ToolDef{{Name: "edit", Description: "edit precisely", Schema: schemaResidencySmall}}
	msgs := []Message{{Role: RoleSystem, Content: "Keep user constraints."}, {Role: RoleUser, Content: "Inspect the code."}}
	for _, build := range []func() ([]byte, error){
		func() ([]byte, error) { return buildOpenAIRequest("ownership-fixture", msgs, tools, true, true) },
		func() ([]byte, error) { return buildAnthropicRequest("ownership-fixture", msgs, tools, true, 4096) },
	} {
		first, err := build()
		if err != nil {
			t.Fatal(err)
		}
		want := bytes.Clone(first)
		for i := range first {
			first[i] = 'x'
		}
		second, err := build()
		if err != nil || !bytes.Equal(second, want) {
			t.Fatalf("body mutation leaked into cache/history: %v", err)
		}
		if tools[0].Schema != schemaResidencySmall || msgs[0].Content != "Keep user constraints." {
			t.Fatal("request builder mutated caller data")
		}
	}
}
