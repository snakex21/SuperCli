package app

import (
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

func TestCallStatsRetainsActualIdentityWithoutExportingConnectionKey(t *testing.T) {
	recorder := stats.NewMemory()
	stat := llm.CallStat{Provider: "openai", ProviderType: "openai", EndpointHost: "helper.example", ConnectionKey: "private-connection-digest", Model: "helper", Purpose: llm.PurposeTask, TokensIn: 100, TokensOut: 40, TokensCached: 30, TokensReasoning: 20}
	statsCallSink(recorder)(stat)
	calls := recorder.Calls()
	if len(calls) != 1 || calls[0].ProviderType != stat.ProviderType || calls[0].EndpointHost != stat.EndpointHost || calls[0].ConnectionKey != stat.ConnectionKey || calls[0].TokensReasoning != 20 {
		t.Fatalf("lost actual call identity: %+v", calls)
	}
	encoded, err := json.Marshal(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), stat.ConnectionKey) || strings.Contains(string(encoded), "connection_key") {
		t.Fatalf("ephemeral connection key was exported: %s", encoded)
	}
}
