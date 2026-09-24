package webgui

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func BenchmarkStatsLongSession(b *testing.B) {
	for _, turns := range []int{10, 200} {
		b.Run(fmt.Sprintf("%d_turns", turns), func(b *testing.B) {
			eng, sid := capsuleCostFixture(b, turns)
			ctx := context.Background()
			store, err := eng.sessionStore()
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < turns; i++ {
				if err := store.AppendUsage(ctx, session.UsageRecord{
					SessionID: sid, Provider: "fixture-local", ProviderType: config.ProviderOpenAI,
					EndpointHost: "127.0.0.1", Model: "fixture-model", Input: 1000, Output: 100,
					ContextWindow: 32768, ContextUser: 200, ContextAssistant: 100, ContextTool: 600, ContextSystem: 100,
				}); err != nil {
					b.Fatal(err)
				}
			}
			expected, err := eng.stats(ctx, sid)
			if err != nil {
				b.Fatal(err)
			}
			if expected.Session.UserMessages != turns || expected.Session.ToolMessages != 2*turns || expected.Session.ToolCalls != 2*turns || expected.Context.EstimatedUsed != 1000 {
				b.Fatalf("wrong fixture stats: %+v", expected)
			}
			b.ReportAllocs()
			for b.Loop() {
				got, err := eng.stats(ctx, sid)
				if err != nil || !reflect.DeepEqual(got, expected) {
					b.Fatalf("stats changed: %v", err)
				}
			}
		})
	}
}

func fullTranscriptStats(t testing.TB, store *session.Store, sid string) (statsSessionView, []llm.Message) {
	t.Helper()
	meta, err := store.Get(sid)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ReadMessages(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	var messages []llm.Message
	for _, row := range rows {
		if msg, err := row.ToMessage(); err == nil {
			messages = append(messages, msg)
		}
	}
	return summarizeSession(meta, messages), messages
}
