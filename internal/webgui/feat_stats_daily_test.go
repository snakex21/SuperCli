package webgui

import (
	"context"
	"testing"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestDailyTokensDeduplicateTUIAccountingCopies(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	ctx := context.Background()
	ledger, err := eng.creditStorage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Hour)
	at := since.Add(time.Minute)
	usage := func(offset time.Duration, source string, in, out int64) {
		t.Helper()
		if err := store.AppendUsage(ctx, session.UsageRecord{SessionID: sess.ID, Source: source,
			Input: in, Output: out, CreatedAt: at.Add(offset)}); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(offset time.Duration, id string, source credits.Source, in, out int64) {
		t.Helper()
		if _, err := ledger.AppendLedger(ctx, credits.LedgerEntry{SessionID: id, Source: source,
			Input: in, Output: out, TS: at.Add(offset).UnixNano()}); err != nil {
			t.Fatal(err)
		}
	}
	// Exact duplicate observed in the user's databases, 2 ms apart.
	usage(0, llm.PurposeMain, 1104, 43)
	entry(2*time.Millisecond, sess.ID, credits.SourceLoop, 1104, 43)
	// An older ledger-only call in the same session still counts.
	entry(-time.Second, sess.ID, credits.SourceLoop, 3156, 223)
	// Repeated identical calls are each deduplicated once, not collapsed.
	usage(time.Second, llm.PurposeMain, 100, 10)
	entry(time.Second+time.Millisecond, sess.ID, credits.SourceLoop, 100, 10)
	usage(time.Second+20*time.Millisecond, llm.PurposeMain, 100, 10)
	entry(time.Second+21*time.Millisecond, sess.ID, credits.SourceLoop, 100, 10)
	// A canceled call with terminal metering but no credit write remains counted.
	usage(2*time.Second, llm.PurposeMain, 200, 10)
	usage(2*time.Second+20*time.Millisecond, llm.PurposeMain, 200, 10)
	entry(2*time.Second+21*time.Millisecond, sess.ID, credits.SourceLoop, 200, 10)
	// GUI and helper calls cannot be mistaken for mirrored TUI main calls.
	usage(3*time.Second, "model", 30, 3)
	entry(3*time.Second+time.Millisecond, sess.ID, credits.SourceLoop, 30, 3)
	usage(4*time.Second, llm.PurposeTitle, 40, 4)
	entry(4*time.Second+time.Millisecond, sess.ID, credits.SourceLoop, 40, 4)
	// Distinct session, reverse order and a long gap are never guessed away.
	usage(5*time.Second, llm.PurposeMain, 50, 5)
	entry(5*time.Second+time.Millisecond, "other-session", credits.SourceLoop, 50, 5)
	entry(6*time.Second-time.Millisecond, sess.ID, credits.SourceLoop, 60, 6)
	usage(6*time.Second, llm.PurposeMain, 60, 6)
	usage(7*time.Second, llm.PurposeMain, 70, 7)
	entry(10*time.Second, sess.ID, credits.SourceLoop, 70, 7)
	// Records preceding the local day boundary never enter either stream.
	usage(-2*time.Minute, llm.PurposeMain, 9999, 9999)
	entry(-2*time.Minute+time.Millisecond, sess.ID, credits.SourceLoop, 9999, 9999)
	want := int64(1147 + 3379 + 220 + 420 + 66 + 88 + 110 + 132 + 154)
	if got := dailyTokenTotal(ctx, store, ledger, since); got != want {
		t.Fatalf("daily tokens=%d want=%d", got, want)
	}
	// Neither budget accounting nor persisted session usage is rewritten.
	if got, err := ledger.TotalSince(ctx, since); err != nil || got != 5231 {
		t.Fatalf("ledger changed: total=%d err=%v", got, err)
	}
	if in, out, err := store.UsageSince(ctx, since); err != nil || in+out != 2062 {
		t.Fatalf("session usage changed: total=%d err=%v", in+out, err)
	}
}
