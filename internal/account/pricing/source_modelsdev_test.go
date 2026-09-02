package pricing

import (
	"strings"
	"testing"
)

// The fixture mirrors the live https://models.dev/api.json shape:
// providers keyed by id, each with a "models" map, per-model "limit"
// and "cost" objects. The zen-style row is the one that matters —
// a gateway-exclusive free-tier model with a real window and no price.
const modelsDevFixture = `{
  "opencode": {
    "id": "opencode",
    "models": {
      "deepseek-v4-flash-free": {
        "id": "deepseek-v4-flash-free",
        "limit": { "context": 200000, "output": 128000 }
      },
      "mimo-v2.5-free": {
        "id": "mimo-v2.5-free",
        "limit": { "context": 200000, "output": 64000 },
        "cost": { "input": 0, "output": 0 }
      }
    }
  },
  "deepseek": {
    "id": "deepseek",
    "models": {
      "deepseek-v4-flash": {
        "id": "deepseek-v4-flash",
        "limit": { "context": 1000000, "output": 128000 },
        "cost": { "input": 0.27, "output": 1.1, "cache_read": 0.07 }
      }
    }
  },
  "broken-provider": {
    "id": "broken-provider",
    "models": {
      "no-metadata": { "id": "no-metadata" }
    }
  }
}`

func TestParseModelsDev(t *testing.T) {
	entries, err := parseModelsDev([]byte(modelsDevFixture))
	if err != nil {
		t.Fatalf("parseModelsDev: %v", err)
	}
	byID := make(map[string]PriceEntry, len(entries))
	for _, e := range entries {
		byID[e.ModelID] = e
	}

	zen, ok := byID["deepseek-v4-flash-free"]
	if !ok {
		t.Fatalf("deepseek-v4-flash-free missing; got %d entries", len(entries))
	}
	if zen.ContextLength != 200000 {
		t.Errorf("zen context = %d, want 200000 (the whole point of this source)", zen.ContextLength)
	}
	if zen.Source != "modelsdev" {
		t.Errorf("source = %q, want modelsdev", zen.Source)
	}
	if zen.InputPer1M != 0 || zen.OutputPer1M != 0 {
		t.Errorf("free model should keep zero prices, got in=%v out=%v", zen.InputPer1M, zen.OutputPer1M)
	}

	paid, ok := byID["deepseek-v4-flash"]
	if !ok {
		t.Fatal("deepseek-v4-flash missing")
	}
	if paid.ContextLength != 1000000 {
		t.Errorf("paid context = %d, want 1000000", paid.ContextLength)
	}
	if paid.InputPer1M != 0.27 || paid.OutputPer1M != 1.1 {
		t.Errorf("paid prices = %v/%v, want 0.27/1.1", paid.InputPer1M, paid.OutputPer1M)
	}
	if paid.CachedInputPer1M != 0.07 {
		t.Errorf("cached input = %v, want 0.07", paid.CachedInputPer1M)
	}

	if _, ok := byID["no-metadata"]; ok {
		t.Error("row without limit and without price must be dropped")
	}
}

func TestParseModelsDevEmptyDocument(t *testing.T) {
	if _, err := parseModelsDev([]byte(`{}`)); err == nil {
		t.Fatal("empty registry must error so FetchAll falls through to the next source")
	}
	if _, err := parseModelsDev([]byte(`not json`)); err == nil || !strings.Contains(err.Error(), "modelsdev parse") {
		t.Fatalf("malformed document: err = %v, want modelsdev parse failure", err)
	}
}

// Merge behavior: when OpenRouter already contributed a price for an
// id, the models.dev row must still contribute its context_length.
func TestModelsDevEntryMergesContextIntoExistingRow(t *testing.T) {
	existing := PriceEntry{
		ModelID:     "deepseek-v4-flash",
		InputPer1M:  0.30,
		OutputPer1M: 1.20,
		Source:      "openrouter",
	}
	fresh := PriceEntry{
		ModelID:       "deepseek-v4-flash",
		ContextLength: 1000000,
		Source:        "modelsdev",
	}
	merged := mergePriceEntryMetadata(existing, fresh)
	if merged.ContextLength != 1000000 {
		t.Errorf("merged context = %d, want 1000000", merged.ContextLength)
	}
	if merged.InputPer1M != 0.30 {
		t.Errorf("merged input price = %v, first source's price must win", merged.InputPer1M)
	}
}
