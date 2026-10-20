package usagecost

import (
	"reflect"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestCallUsageProjectsRequestTraceAndExplicitZeroCache(t *testing.T) {
	trace := &llm.RequestTrace{Version: 1, First: llm.RequestAttemptTrace{State: "unavailable", Unavailable: "no_http_attempt"}}
	for _, tc := range []struct {
		reported bool
		cached   int
		want     bool
	}{{true, 0, true}, {false, 2, true}, {false, 0, false}} {
		stat := llm.CallStat{TokensIn: 10, TokensOut: 3, TokensCached: tc.cached, HasCachedInput: tc.reported, RequestTrace: trace}
		u := CallUsage(config.TomlConfig{}, stat, session.UsageRecord{})
		if u.HasCachedInput != tc.want || !reflect.DeepEqual(u.RequestTrace, trace) {
			t.Fatalf("projection: %+v", u)
		}
	}
	trace.First.Unavailable = "private"
	if u := CallUsage(config.TomlConfig{}, llm.CallStat{RequestTrace: trace}, session.UsageRecord{}); u.RequestTrace != nil {
		t.Fatal("unsafe trace projected")
	}
}
