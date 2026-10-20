package agent

import (
	"reflect"
	"testing"

	"supercli/internal/llm"
)

// Test-only controls isolate the count pass from slice-growth savings. The
// known-capacity control uses an oracle computed outside timing; it is not a
// proposed production implementation. The upper-bound control needs no oracle
// but may allocate a larger size class when system messages are present.
func BenchmarkSharedWorkerSeedPrecount(b *testing.B) {
	a, _, _ := workerStartSeedDiagnosis(b, 1024, false)
	messages := a.ParentLoop.Messages
	want := referenceSharedWorkerSeed(messages, false)
	copyWithCapacity := func(capacity int) []llm.Message {
		out := make([]llm.Message, 0, capacity)
		for _, message := range messages {
			if message.Role != llm.RoleSystem {
				out = append(out, message)
			}
		}
		return out
	}
	for _, variant := range []struct {
		name string
		seed func() []llm.Message
	}{
		{"helper", func() []llm.Message { return sharedWorkerSeed(messages, false) }},
		{"known_capacity_no_count", func() []llm.Message { return copyWithCapacity(len(want)) }},
		{"upper_bound_no_count", func() []llm.Message { return copyWithCapacity(len(messages)) }},
	} {
		b.Run(variant.name, func(b *testing.B) {
			if got := variant.seed(); !reflect.DeepEqual(got, want) {
				b.Fatal("control differs from old seed")
			}
			b.ReportAllocs()
			for b.Loop() {
				if got := variant.seed(); len(got) != len(want) {
					b.Fatal("seed size changed")
				}
			}
		})
	}
}
