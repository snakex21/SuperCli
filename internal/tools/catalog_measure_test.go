package tools

import "testing"

// catalog_measure_test.go is a measurement harness, not a pass/fail
// assertion of an exact number. It builds the small-tier always-on
// tool set with REAL Spec()s and prints the per-turn input-token cost
// in JSON-schema mode vs compact-catalog mode, using the code's own
// chars/4 heuristic (EstimateSchemaTokens / EstimateCatalogTokens).
//
// Run it to see the concrete saving:
//
//	go test ./internal/tools/ -run TestMeasure_CatalogVsSchema -v
//
// The one assertion is a loose sanity floor: the catalog must be at
// least 3x cheaper than full schemas for this set. The headline X is
// in the logged output.
func smallTierAlwaysOnSpecs() []Tool {
	const base = "."
	// The small-tier always-on set from main.go:584-592, minus the two
	// tools that need runtime services (goal, tool_search). Their
	// schemas are small and present in both modes' core, so omitting
	// them does not distort the schema-vs-catalog comparison for the
	// schema-heavy file/edit tools that dominate the cost.
	return []Tool{
		NewReadLines(base).Spec(),
		NewReadContext(base).Spec(),
		NewEditLine(base).Spec(),
		NewEditLines(base).Spec(),
		NewInsertAfter(base).Spec(),
		NewDeleteLines(base).Spec(),
		NewFileOps(base).Spec(),
		NewEditDocx(base).Spec(),
		NewEditXlsx(base).Spec(),
	}
}

func TestMeasure_CatalogVsSchema(t *testing.T) {
	set := smallTierAlwaysOnSpecs()

	schemaTok := EstimateSchemaTokens(set)
	catalogTok := EstimateCatalogTokens(set, 80) // defaultThinHintMax

	saved := schemaTok - catalogTok
	pct := 0.0
	if schemaTok > 0 {
		pct = float64(saved) / float64(schemaTok) * 100
	}

	t.Logf("small-tier always-on set: %d tools", len(set))
	t.Logf("  JSON-schema mode:  ~%d tok/turn", schemaTok)
	t.Logf("  catalog mode:      ~%d tok/turn", catalogTok)
	t.Logf("  saved:             ~%d tok/turn (%.1f%%)", saved, pct)

	if catalogTok*3 > schemaTok {
		t.Errorf("catalog (%d) not at least 3x cheaper than schemas (%d)", catalogTok, schemaTok)
	}
}
