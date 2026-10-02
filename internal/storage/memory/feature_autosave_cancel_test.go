package memory

import (
	"context"
	"errors"
	"testing"
)

func TestStoreSummaryRejectsCanceledResult(t *testing.T) {
	for _, result := range []string{"", "NOTHING", "Incomplete project note.", "NOTHING\nUSER: The user likes astronomy."} {
		t.Run(result, func(t *testing.T) {
			saver, project, global := newAutoSaverForTest(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			consumed := saver.StoreSummary(ctx, "user: Discuss the project", func(context.Context, string) (string, error) {
				cancel()
				return result, nil // A canceled provider may close without an error delta.
			})
			if consumed {
				t.Error("canceled result consumed the transcript")
			}
			if logs, _ := project.Recent(ScopeTaskLog, 5); len(logs) != 0 {
				t.Error("canceled summary was persisted")
			}
			if prefs, _ := global.Recent(ScopePreference, 5); len(prefs) != 0 {
				t.Error("canceled user fact was persisted")
			}
		})
	}
}

func TestSummarizePendingRawPassStatusAndRetry(t *testing.T) {
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded, errors.New("provider unavailable"), errors.Join(ErrSummaryInterrupted, context.Canceled)} {
		t.Run(failure.Error(), func(t *testing.T) {
			saver, project, _ := newAutoSaverForTest(t)
			for _, id := range []string{"raw-one", "raw-two"} {
				if err := project.Put(Entry{ID: id, Scope: ScopeRawLog, Content: "user: Keep this history.", Source: SourceAgent}); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			completed := saver.SummarizePendingRaw(context.Background(), func(context.Context, string) (string, error) {
				calls++
				return "", failure
			})
			interrupted := errors.Is(failure, ErrSummaryInterrupted)
			wantCalls := 2
			if interrupted {
				wantCalls = 1
			}
			if completed == interrupted || calls != wantCalls {
				t.Errorf("completed=%v calls=%d; interrupted=%v want calls=%d", completed, calls, interrupted, wantCalls)
			}
			if raws, _ := project.Recent(ScopeRawLog, 5); len(raws) != 2 {
				t.Fatal("failed pass lost raw history")
			}
			if !saver.SummarizePendingRaw(context.Background(), func(context.Context, string) (string, error) {
				return "A complete project note.", nil
			}) {
				t.Fatal("complete retry reported interruption")
			}
			if raws, _ := project.Recent(ScopeRawLog, 5); len(raws) != 0 {
				t.Fatal("successful retry did not consume raw history")
			}
		})
	}
}

func TestStoreSummaryCanceledBeforeCallDoesNoWork(t *testing.T) {
	saver, _, _ := newAutoSaverForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	consumed := saver.StoreSummary(ctx, "user: Save this", func(context.Context, string) (string, error) {
		calls++
		return "A complete note.", nil
	})
	if consumed || calls != 0 {
		t.Fatalf("canceled call consumed=%v calls=%d", consumed, calls)
	}
}

func TestSummarizePendingRawStopsOnCancellation(t *testing.T) {
	saver, project, _ := newAutoSaverForTest(t)
	for _, id := range []string{"raw-one", "raw-two"} {
		if err := project.Put(Entry{ID: id, Scope: ScopeRawLog, Content: "user: Keep this project history.", Source: SourceAgent}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	saver.SummarizePendingRaw(ctx, func(context.Context, string) (string, error) {
		calls++
		cancel()
		return "An incomplete note.", nil
	})
	if calls != 1 {
		t.Errorf("attempts after cancellation=%d, want 1", calls)
	}
	if raws, _ := project.Recent(ScopeRawLog, 5); len(raws) != 2 {
		t.Errorf("raw entries remaining=%d, want 2", len(raws))
	}
	if logs, _ := project.Recent(ScopeTaskLog, 5); len(logs) != 0 {
		t.Error("partial raw summary persisted")
	}
}
