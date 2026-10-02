package llm

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type immediateErrorProvider struct {
	complete func(context.Context) (<-chan Delta, error)
}

func (p immediateErrorProvider) Name() string { return "immediate-error-test" }
func (p immediateErrorProvider) Complete(ctx context.Context, _ []Message, _ []ToolDef) (<-chan Delta, error) {
	return p.complete(ctx)
}

func TestMeteredImmediateProviderErrorPreservesActualCancellation(t *testing.T) {
	for _, background := range []bool{false, true} {
		for _, providerErr := range []error{errors.New("provider failure"), context.Canceled} {
			name := "foreground/" + providerErr.Error()
			if background {
				name = "background/" + providerErr.Error()
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if background {
					ctx = WithBackground(ctx)
				}
				wrapperStats := make(chan CallStat, 1)
				outerStats := make(chan CallStat, 1)
				ctx = WithCallSink(ctx, func(stat CallStat) { outerStats <- stat })
				provider := Metered(immediateErrorProvider{complete: func(callCtx context.Context) (<-chan Delta, error) {
					if callCtx.Err() != nil {
						t.Errorf("stub received canceled request before provider error: %v", callCtx.Err())
					}
					return nil, providerErr
				}}, "test", "main", func(stat CallStat) { wrapperStats <- stat })
				stream, err := provider.Complete(ctx, nil, nil)
				if !errors.Is(err, providerErr) || stream != nil {
					t.Fatalf("Complete returned stream=%v err=%v", stream, err)
				}
				if len(wrapperStats) != 1 || len(outerStats) != 1 {
					t.Fatalf("error metrics missing: wrapper=%d outer=%d", len(wrapperStats), len(outerStats))
				}
				wrapped, outer := <-wrapperStats, <-outerStats
				if !wrapped.Failed || wrapped.Canceled || wrapped.Background != background {
					t.Fatalf("ordinary error mislabeled as cancellation: %+v", wrapped)
				}
				if !reflect.DeepEqual(wrapped, outer) {
					t.Fatalf("context sink mismatch: wrapper=%+v outer=%+v", wrapped, outer)
				}
			})
		}
	}
}

func TestMeteredPreemptionBeforeStreamKeepsCancellationAndSinks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := make(chan struct{})
	wrapperStats := make(chan CallStat, 1)
	outerStats := make(chan CallStat, 1)
	background := Metered(immediateErrorProvider{complete: func(callCtx context.Context) (<-chan Delta, error) {
		close(started)
		<-callCtx.Done()
		return nil, callCtx.Err()
	}}, "test", "vision-index", func(stat CallStat) { wrapperStats <- stat })
	bgCtx := WithCallSink(WithBackground(ctx), func(stat CallStat) { outerStats <- stat })
	done := make(chan error, 1)
	go func() { _, err := background.Complete(bgCtx, nil, nil); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("background call did not start")
	}
	foregroundRelease := make(chan struct{})
	defer close(foregroundRelease)
	foreground := Metered(immediateErrorProvider{complete: func(callCtx context.Context) (<-chan Delta, error) {
		stream := make(chan Delta)
		go func() {
			defer close(stream)
			select {
			case <-foregroundRelease:
			case <-callCtx.Done():
			}
		}()
		return stream, nil
	}}, "test", "main", func(CallStat) {})
	stream, err := foreground.Complete(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range stream {
		}
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("preempted Complete error=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("foreground did not preempt blocked Complete")
	}
	wrapped, outer := <-wrapperStats, <-outerStats
	if !wrapped.Failed || !wrapped.Canceled || !wrapped.Background {
		t.Fatalf("real preemption lost cancellation: %+v", wrapped)
	}
	if !reflect.DeepEqual(wrapped, outer) {
		t.Fatalf("preemption context sink mismatch: wrapper=%+v outer=%+v", wrapped, outer)
	}
}
