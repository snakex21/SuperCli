package webgui

import (
	"context"
	"reflect"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestStatsMessageCountsPreserveLegacyAndRecordedUsage(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	ctx := context.Background()
	rows := []session.Encoded{
		{Role: "user", Content: "request"},
		{Role: "assistant", PartsJSON: `[{"Type":"text","Text":"answer"}]`},
		{Role: "assistant", ToolCallsJSON: `[{"ID":"a","Name":"read","Arguments":"{}"}]`},
		{Role: "tool", ToolCallID: "a", Content: "output"},
		{Role: "assistant", PartsJSON: "["},
		{Role: "assistant", Content: "bad calls", ToolCallsJSON: `[{"Name":"read"}]`},
		{Role: "user", Content: ""},
		{Role: "user", Content: "\x00"},
	}
	for _, row := range rows {
		if err := store.AppendMessage(ctx, sess.ID, row); err != nil {
			t.Fatal(err)
		}
	}
	before, messages := fullTranscriptStats(t, store, sess.ID)
	legacy, err := eng.stats(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Provider identity is supplied separately from transcript summary.
	summary := legacy.Session
	summary.Provider = ""
	summary.ProviderType = ""
	if !reflect.DeepEqual(summary, before) {
		t.Fatalf("legacy counts: %+v want %+v", summary, before)
	}
	expectedContext := contextFromMessages(messages, eng.legacyUsageIdentity(sess.Model).ContextWindow)
	if legacy.Context.Breakdown != expectedContext.Breakdown || legacy.Context.EstimatedUsed != expectedContext.EstimatedUsed {
		t.Fatalf("legacy context changed: %+v want %+v", legacy.Context, expectedContext)
	}
	usage := session.UsageRecord{SessionID: sess.ID, Provider: "local-fixture", ProviderType: config.ProviderOpenAI, EndpointHost: "127.0.0.1", Model: "fixture-model", Input: 900, Output: 80, CachedInput: 300, Reasoning: 20, HasCachedInput: true, HasReasoning: true, ContextWindow: 32768, ContextSystem: 100, ContextUser: 200, ContextAssistant: 100, ContextTool: 500}
	if err := store.AppendUsage(ctx, usage); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		want, _ := fullTranscriptStats(t, store, sess.ID)
		got, err := eng.stats(ctx, sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		counts := got.Session
		counts.Provider = ""
		counts.ProviderType = ""
		counts.Model = want.Model
		if !reflect.DeepEqual(counts, want) {
			t.Fatalf("recorded counts: %+v want %+v", counts, want)
		}
		if got.Context.Breakdown != contextFromUsage(usage).Breakdown || got.Tokens.Input != 900 || got.Tokens.CachedInput != 300 || got.Tokens.Reasoning != 20 {
			t.Fatalf("usage changed: %+v", got)
		}
	}
	check()
	appendStatsMessage(t, store, sess.ID, llm.Message{Role: llm.RoleAssistant, Content: "new result"})
	check()
	if _, err := store.TruncateFrom(ctx, sess.ID, 5); err != nil {
		t.Fatal(err)
	}
	check()
}
