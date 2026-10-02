package app

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
)

type memoryPreemptionProvider struct {
	complete func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error)
}

func (p memoryPreemptionProvider) Name() string { return "memory-test" }
func (p memoryPreemptionProvider) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	return p.complete(ctx, msgs, tools)
}

func TestIncrementalMemorySaveDiscardsPreemptedPartial(t *testing.T) {
	for _, mode := range []string{"foreground_preemption", "parent_cancellation", "error_delta", "before_stream"} {
		t.Run(mode, func(t *testing.T) {
			timeoutCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(timeoutCtx)
			defer cancel()
			saver, project, _ := newMemFixture(t)
			loop := loopWithMessages(llm.Message{Role: llm.RoleUser, Content: "Explain the project."}, llm.Message{Role: llm.RoleAssistant, Content: "A complete answer."})
			prog := &memProgress{}
			wrappedStats := make(chan llm.CallStat, 2)
			outerStats := make(chan llm.CallStat, 2)
			delivered := make(chan struct{})
			provider := llm.Metered(memoryPreemptionProvider{complete: func(callCtx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
				if mode == "before_stream" {
					close(delivered)
					<-callCtx.Done()
					return nil, callCtx.Err()
				}
				stream := make(chan llm.Delta)
				go func() {
					defer close(stream)
					select {
					case stream <- llm.Delta{Content: "An incomplete project note."}:
					case <-callCtx.Done():
						return
					}
					<-callCtx.Done() // Close silently, as actual transports may do.
					if mode == "error_delta" {
						stream <- llm.Delta{Err: callCtx.Err()}
					}
				}()
				return stream, nil
			}}, "test", "main", func(stat llm.CallStat) { wrappedStats <- stat })
			// Signal only after the actual consumer receives the partial delta.
			forwarded := memoryPreemptionProvider{complete: func(callCtx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
				input, err := provider.Complete(callCtx, msgs, tools)
				if err != nil {
					return nil, err
				}
				output := make(chan llm.Delta)
				go func() {
					defer close(output)
					first := true
					for delta := range input {
						output <- delta
						if first {
							close(delivered)
							first = false
						}
					}
				}()
				return output, nil
			}}
			ctx = llm.WithCallSink(ctx, func(stat llm.CallStat) { outerStats <- stat })
			done := make(chan struct{})
			go func() { incrementalMemorySave(ctx, saver, loop, prog, forwarded); close(done) }()
			select {
			case <-delivered:
			case <-timeoutCtx.Done():
				t.Fatal("summary did not deliver a partial delta")
			}
			if mode == "parent_cancellation" {
				cancel()
			} else {
				foreground := llm.Metered(memoryPreemptionProvider{complete: func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
					stream := make(chan llm.Delta)
					close(stream)
					return stream, nil
				}}, "test", "main", func(llm.CallStat) {})
				stream, err := foreground.Complete(timeoutCtx, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				for range stream {
				}
			}
			select {
			case <-done:
			case <-timeoutCtx.Done():
				t.Fatal("canceled summary did not finish")
			}
			if mode != "parent_cancellation" && ctx.Err() != nil {
				t.Fatal("foreground preemption canceled the parent")
			}
			if prog.covered != 0 {
				t.Errorf("partial summary advanced coverage to %d", prog.covered)
			}
			if logs, _ := project.Recent(memory.ScopeTaskLog, 5); len(logs) != 0 {
				t.Error("partial summary was stored")
			}
			wrapped, outer := <-wrappedStats, <-outerStats
			if !wrapped.Canceled || !wrapped.Background || wrapped.Purpose != llm.PurposeMemory || !reflect.DeepEqual(wrapped, outer) {
				t.Errorf("cancellation or existing sink lost: wrapper=%+v outer=%+v", wrapped, outer)
			}
			retry := &countingProvider{reply: "A complete project note."}
			incrementalMemorySave(timeoutCtx, saver, loop, prog, retry)
			if prog.covered != 2 || retry.calls.Load() != 1 {
				t.Errorf("backlog retry covered=%d calls=%d", prog.covered, retry.calls.Load())
			}
		})
	}
}

func TestMemoryRawPassDoesNotRetryOrdinaryCancellationErrors(t *testing.T) {
	for _, mode := range []string{"before_stream", "error_delta"} {
		for _, providerErr := range []error{context.Canceled, context.DeadlineExceeded} {
			t.Run(mode+"/"+providerErr.Error(), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				saver, project, _ := newMemFixture(t)
				for _, id := range []string{"raw-one", "raw-two"} {
					if err := project.Put(memory.Entry{ID: id, Scope: memory.ScopeRawLog, Content: "user: Keep the project history.", Source: memory.SourceAgent}); err != nil {
						t.Fatal(err)
					}
				}
				var calls atomic.Int32
				stats := make(chan llm.CallStat, 4)
				provider := llm.Metered(memoryPreemptionProvider{complete: func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
					calls.Add(1)
					if mode == "before_stream" {
						return nil, providerErr
					}
					stream := make(chan llm.Delta, 1)
					stream <- llm.Delta{Err: providerErr}
					close(stream)
					return stream, nil
				}}, "test", "main", func(stat llm.CallStat) { stats <- stat })
				if !saver.SummarizePendingRaw(ctx, providerSummarizer(provider)) || calls.Load() != 2 {
					t.Fatalf("ordinary failure was classified as interrupted: calls=%d ctx=%v", calls.Load(), ctx.Err())
				}
				for i := 0; i < 2; i++ {
					stat := <-stats
					if stat.Canceled || !stat.Failed {
						t.Errorf("ordinary failed call stat=%+v", stat)
					}
				}
				if raws, _ := project.Recent(memory.ScopeRawLog, 5); len(raws) != 2 {
					t.Fatal("ordinary failure lost raw history")
				}
				_, err := providerSummarizer(provider)(ctx, "fixture")
				if !errors.Is(err, providerErr) || errors.Is(err, memory.ErrSummaryInterrupted) {
					t.Errorf("ordinary provider error changed: %v", err)
				}
			})
		}
	}
}

func TestProviderSummarizerCanceledParentDoesNotWaitForStreamClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := memoryPreemptionProvider{complete: func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
		cancel()
		return make(chan llm.Delta), nil // Misbehaving provider never closes.
	}}
	done := make(chan error, 1)
	go func() {
		_, err := providerSummarizer(provider)(ctx, "fixture")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled parent error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("summary waited for an unclosed stream after cancellation")
	}
}
