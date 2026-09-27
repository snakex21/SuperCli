package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMeteredMetadataDoesNotStartTTFT(t *testing.T) {
	for _, tc := range []struct {
		name   string
		deltas []Delta
	}{
		{"role", []Delta{{Role: RoleAssistant}}},
		{"usage", []Delta{{Usage: &Usage{Input: 12000}}}},
		{"finish", []Delta{{FinishReason: "stop"}}},
		{"error", []Delta{{Err: errors.New("no output")}}},
		{"empty", []Delta{{}}},
		{"notice", []Delta{{Notice: "queued"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &sinkCapture{}
			p := Metered(&meterStub{name: "m", delay: 20 * time.Millisecond, deltas: tc.deltas}, "test", PurposeMain, capture.sink())
			ch, err := p.Complete(context.Background(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			for range ch {
				count++
			}
			s := capture.all()[0]
			if s.TTFT != 0 || s.PrefillTokensPerSecond != 0 {
				t.Fatalf("metadata generated a token timing: ttft=%v throughput=%v", s.TTFT, s.PrefillTokensPerSecond)
			}
			if count != len(tc.deltas) {
				t.Fatalf("metadata lost: %d", count)
			}
		})
	}
}
