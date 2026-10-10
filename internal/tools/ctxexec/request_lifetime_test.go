package ctxexec

import (
	"context"
	"testing"
	"time"
)

func TestRequestLifetimeSharesAdmissionBudgetWithRunner(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	defer stop()
	for _, timeout := range []int{0, 30_000} {
		ctx, cancel, err := RequestLifetime(parent, timeout)
		if err != nil {
			t.Fatal(err)
		}
		if timeout == 0 && ctx != parent {
			t.Fatal("zero timeout allocated a replacement context")
		}
		runCtx, stopRun := commandContext(ctx, timeout)
		if runCtx != ctx {
			t.Fatal("runner restarted an already bounded request lifetime")
		}
		stopRun()
		if ctx.Err() != nil {
			t.Fatal("runner's no-op cleanup cancelled the admission owner")
		}
		cancel()
	}
	ctx, cancel, err := RequestLifetime(parent, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	stop()
	<-ctx.Done()
	if ctx.Err() != context.Canceled {
		t.Fatal("zero request lifetime lost caller cancellation")
	}
}

func TestRequestLifetimeKeepsEarlierCallerDeadlineAndRejectsInvalid(t *testing.T) {
	parent, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	ctx, cancel, err := RequestLifetime(parent, 3_600_000)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if ctx != parent {
		t.Fatal("later request limit replaced the earlier caller deadline")
	}
	for _, timeout := range []int{-1, int(^uint(0) >> 1)} {
		if timeout > 0 && int64(timeout) <= MaxTimeoutMS {
			continue
		}
		if ctx, cancel, err := RequestLifetime(parent, timeout); err == nil || ctx != nil || cancel != nil {
			t.Fatalf("invalid request timeout accepted: %d", timeout)
		}
	}
}
