package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/llm"
)

// Opt-in replay of captured source reads; never execute the captured tools or
// contact a model. Use explicit latency pressure to test that an active task
// does not lose working evidence while well below the hard context window.
func TestActiveEvidenceHistoricalReplay(t *testing.T) {
	path := os.Getenv("SUPERCLI_ACTIVE_SOURCE_TRACE")
	if path == "" {
		t.Skip("opt-in portable source-read trace")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var messages []llm.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		t.Fatal(err)
	}
	l := pruneLoop(t, 128000, 1)
	l.prefillProfiles = llm.LoadPrefillProfiles(t.TempDir())
	l.prefillProfiles.Observe(prefillProfileDefaultScope, "test", llm.PrefillSample{InputTokens: 16000, TTFT: 30 * time.Second})
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: "Inspect the requested source before implementing the change."}}
	checks, results, peak := 0, 0, 0
	for _, message := range messages {
		if message.Role != llm.RoleAssistant && message.Role != llm.RoleTool {
			continue
		}
		if message.Role == llm.RoleAssistant {
			if len(message.ToolCalls) == 0 {
				continue
			}
			checks++
			estimate := l.EstimateNextRequestTokens()
			peak = max(peak, estimate)
			if estimate >= int(pruneTriggerFrac*128000) {
				t.Fatal("fixture unexpectedly reached hard context pressure")
			}
			if gain := l.maybePruneToolResults(context.Background(), nil); gain != 0 {
				t.Fatalf("active source-read trace lost evidence at request %d: %d reclaimed tokens", checks, gain)
			}
		} else {
			results++
		}
		l.Messages = append(l.Messages, message)
	}
	if budget, ok := l.PrefillBudget(); !ok || peak <= budget || results < 10 {
		t.Fatalf("trace did not exercise latency pressure: peak=%d budget=%d results=%d", peak, budget, results)
	}
	t.Logf("replayed %d source results across %d request boundaries; peak estimate %d; zero working results discarded", results, checks, peak)
}
