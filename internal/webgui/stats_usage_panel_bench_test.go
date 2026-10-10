package webgui

import (
	"context"
	"fmt"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"testing"
)

var statsUsagePanelSink statsView

func BenchmarkStatsUsagePanel(b *testing.B) {
	for _, n := range []int{8, 1752, 10000} {
		b.Run(fmt.Sprintf("calls_%d", n), func(b *testing.B) {
			dir := b.TempDir()
			eng, err := NewEngine(echoConfig(), dir, dir)
			if err != nil {
				b.Fatal(err)
			}
			defer eng.Close()
			store, err := eng.sessionStore()
			if err != nil {
				b.Fatal(err)
			}
			sess, err := store.Create(dir, "fixture-unknown", "usage scaling")
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			for i := 0; i < n; i++ {
				u := session.UsageRecord{SessionID: sess.ID, CallSeq: i + 1, Provider: "fixture-cloud", ProviderType: config.ProviderOpenAI, EndpointHost: "fixture.invalid", Model: "fixture-unknown", Input: 1000, Output: 100, CachedInput: 100, Reasoning: 25, HasCachedInput: i%2 == 0, HasReasoning: i%3 == 0, ContextWindow: 65536, ContextSystem: 100, ContextUser: 1000, ContextAssistant: 100, ContextTool: 2000, ContextOther: 50, TTFTMS: 123, PrefillEvaluated: 900, PrefillBudget: 15000, PrefillBudgetSource: "fixture source", Source: "model"}
				if i%3 == 0 {
					u.EndpointHost = "127.0.0.1"
				}
				if i%3 == 1 {
					u.Model = "fixture-free"
				}
				if err := store.AppendUsage(ctx, u); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				v, err := eng.stats(ctx, sess.ID)
				if err != nil {
					b.Fatal(err)
				}
				statsUsagePanelSink = v
			}
		})
	}
}
