package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

func mustCreateUsageSession(t *testing.T, s *Store, cwd string) Session {
	t.Helper()
	sess, err := s.Create(cwd, "test-model", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return sess
}

func TestOpenStore_MigratesSessionUsage(t *testing.T) {
	dir := t.TempDir()
	original, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore initial: %v", err)
	}
	sess := mustCreateUsageSession(t, original, "/migration")
	if _, err := original.db.Exec(`DROP TABLE session_usage`); err != nil {
		_ = original.Close()
		t.Fatalf("drop session_usage: %v", err)
	}
	if err := original.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	migrated, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore migrated: %v", err)
	}
	t.Cleanup(func() { _ = migrated.Close() })

	var tables int
	if err := migrated.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_usage'`).Scan(&tables); err != nil {
		t.Fatalf("query session_usage migration: %v", err)
	}
	if tables != 1 {
		t.Fatalf("session_usage table count = %d, want 1", tables)
	}

	ctx := context.Background()
	if err := migrated.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Input: 9, Output: 4}); err != nil {
		t.Fatalf("AppendUsage after migration: %v", err)
	}
	got, err := migrated.ReadUsage(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ReadUsage after migration: %v", err)
	}
	if len(got) != 1 || got[0].CallSeq != 1 || got[0].Input != 9 || got[0].Output != 4 {
		t.Fatalf("usage after migration = %+v, want one usable call", got)
	}
}

func TestStore_AppendReadUsage_CallSeqOrderAndRoundTrip(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/order")
	ctx := context.Background()
	base := time.Date(2030, time.January, 2, 3, 4, 5, 6, time.UTC)

	records := []UsageRecord{
		{SessionID: sess.ID, CallSeq: 2, Model: "second", Input: 20, Output: 2, CreatedAt: base.Add(2 * time.Second)},
		{
			SessionID: sess.ID, CallSeq: 1,
			Provider: "openai", ProviderType: "openai-compatible", EndpointHost: "api.example.test", Model: "first",
			Input: 10, Output: 1, CachedInput: 3, Reasoning: 1, HasCachedInput: true, HasReasoning: true,
			ContextWindow: 128000, ContextSystem: 2, ContextUser: 3, ContextAssistant: 4, ContextTool: 5, ContextOther: 6,
			Source: "provider", CreatedAt: base.Add(time.Second),
		},
		{SessionID: sess.ID, Model: "automatic", Input: 30, Output: 3, CreatedAt: base.Add(3 * time.Second)},
	}
	for i, record := range records {
		if err := s.AppendUsage(ctx, record); err != nil {
			t.Fatalf("AppendUsage record %d: %v", i, err)
		}
	}

	got, err := s.ReadUsage(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ReadUsage: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ReadUsage len = %d, want 3", len(got))
	}
	for i, wantSeq := range []int{1, 2, 3} {
		if got[i].CallSeq != wantSeq {
			t.Errorf("usage[%d].CallSeq = %d, want %d", i, got[i].CallSeq, wantSeq)
		}
	}

	first := got[0]
	if first.SessionID != sess.ID || first.Provider != "openai" || first.ProviderType != "openai-compatible" ||
		first.EndpointHost != "api.example.test" || first.Model != "first" {
		t.Errorf("identity round trip = %+v", first)
	}
	if first.Input != 10 || first.Output != 1 || first.CachedInput != 3 || first.Reasoning != 1 ||
		!first.HasCachedInput || !first.HasReasoning {
		t.Errorf("tokens round trip = %+v", first)
	}
	if first.ContextWindow != 128000 || first.ContextSystem != 2 || first.ContextUser != 3 ||
		first.ContextAssistant != 4 || first.ContextTool != 5 || first.ContextOther != 6 {
		t.Errorf("context round trip = %+v", first)
	}
	if first.Source != "provider" || !first.CreatedAt.Equal(base.Add(time.Second)) {
		t.Errorf("metadata round trip = %+v", first)
	}
	if got[2].Source != "model" {
		t.Errorf("automatic record Source = %q, want default model", got[2].Source)
	}
}

func TestStore_AppendUsage_ClampsTokenSubsets(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/clamp")
	ctx := context.Background()

	if err := s.AppendUsage(ctx, UsageRecord{
		SessionID: sess.ID, Input: 100, Output: 40,
		CachedInput: 150, Reasoning: 70, HasCachedInput: true, HasReasoning: true,
	}); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}
	got, err := s.ReadUsage(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ReadUsage: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ReadUsage len = %d, want 1", len(got))
	}
	if got[0].CachedInput != got[0].Input || got[0].CachedInput != 100 {
		t.Errorf("CachedInput = %d, Input = %d; want both 100", got[0].CachedInput, got[0].Input)
	}
	if got[0].Reasoning != got[0].Output || got[0].Reasoning != 40 {
		t.Errorf("Reasoning = %d, Output = %d; want both 40", got[0].Reasoning, got[0].Output)
	}
}

func TestStore_AppendUsage_AllocatesSequenceAcrossParallelCalls(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/parallel")
	const calls = 24
	var wg sync.WaitGroup
	errs := make(chan error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(input int64) {
			defer wg.Done()
			errs <- s.AppendUsage(context.Background(), UsageRecord{SessionID: sess.ID, Input: input})
		}(int64(i + 1))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("parallel AppendUsage: %v", err)
		}
	}
	got, err := s.ReadUsage(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != calls {
		t.Fatalf("parallel usage len = %d, want %d", len(got), calls)
	}
	for i, row := range got {
		if row.CallSeq != i+1 {
			t.Fatalf("parallel call_seq[%d] = %d", i, row.CallSeq)
		}
	}
}

func TestStore_UsageSince(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/since")
	ctx := context.Background()
	boundary := time.Date(2031, time.February, 3, 4, 5, 6, 0, time.UTC)

	for i, record := range []UsageRecord{
		{SessionID: sess.ID, Input: 7, Output: 3, CreatedAt: boundary.Add(-time.Minute)},
		{SessionID: sess.ID, Input: 11, Output: 5, CreatedAt: boundary},
		{SessionID: sess.ID, Input: 13, Output: 7, CreatedAt: boundary.Add(time.Minute)},
	} {
		if err := s.AppendUsage(ctx, record); err != nil {
			t.Fatalf("AppendUsage record %d: %v", i, err)
		}
	}

	input, output, err := s.UsageSince(ctx, boundary)
	if err != nil {
		t.Fatalf("UsageSince boundary: %v", err)
	}
	if input != 24 || output != 12 {
		t.Errorf("UsageSince(boundary) = (%d, %d), want (24, 12)", input, output)
	}

	input, output, err = s.UsageSince(ctx, boundary.Add(time.Nanosecond))
	if err != nil {
		t.Fatalf("UsageSince after boundary: %v", err)
	}
	if input != 13 || output != 7 {
		t.Errorf("UsageSince(boundary+1ns) = (%d, %d), want (13, 7)", input, output)
	}
}

func TestStore_UsageIsIsolatedBetweenSessions(t *testing.T) {
	s := openTestStore(t)
	firstSession := mustCreateUsageSession(t, s, "/first")
	secondSession := mustCreateUsageSession(t, s, "/second")
	ctx := context.Background()

	for i, record := range []UsageRecord{
		{SessionID: firstSession.ID, Model: "first-1", Input: 1},
		{SessionID: secondSession.ID, Model: "second-1", Input: 100},
		{SessionID: firstSession.ID, Model: "first-2", Input: 2},
	} {
		if err := s.AppendUsage(ctx, record); err != nil {
			t.Fatalf("AppendUsage record %d: %v", i, err)
		}
	}

	first, err := s.ReadUsage(ctx, firstSession.ID)
	if err != nil {
		t.Fatalf("ReadUsage first: %v", err)
	}
	if len(first) != 2 || first[0].SessionID != firstSession.ID || first[1].SessionID != firstSession.ID ||
		first[0].CallSeq != 1 || first[1].CallSeq != 2 || first[0].Input != 1 || first[1].Input != 2 {
		t.Errorf("first session usage = %+v", first)
	}

	second, err := s.ReadUsage(ctx, secondSession.ID)
	if err != nil {
		t.Fatalf("ReadUsage second: %v", err)
	}
	if len(second) != 1 || second[0].SessionID != secondSession.ID || second[0].CallSeq != 1 || second[0].Input != 100 {
		t.Errorf("second session usage = %+v", second)
	}

	missing, err := s.ReadUsage(ctx, "missing-session")
	if err != nil {
		t.Fatalf("ReadUsage missing: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing session usage = %+v, want empty", missing)
	}
}
