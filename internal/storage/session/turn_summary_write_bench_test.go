package session

import (
	"context"
	"testing"
)

// A normal (non-deferred) durable summary write. The identical benchmark can
// run against the pre-binding implementation without any new struct fields.
func BenchmarkOrdinaryTurnSummaryWrite(b *testing.B) {
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	sess, err := s.Create(b.TempDir(), "echo", "synthetic benchmark")
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	if err := s.AppendMessage(ctx, sess.ID, Encoded{Role: "assistant", Content: "synthetic"}); err != nil {
		b.Fatal(err)
	}
	turn := TurnSummary{SessionID: sess.ID, AssistantSeq: 1, Input: 17, Output: 5, DurationMS: 300}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.AppendTurnSummary(ctx, turn); err != nil {
			b.Fatal(err)
		}
	}
}
