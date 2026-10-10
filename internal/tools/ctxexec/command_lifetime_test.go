package ctxexec

import (
	"context"
	"testing"
	"time"
)

func TestCommandLifetimeHasNoDefaultDeadlineAndKeepsCallerCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	ctx, cancel := commandContext(parent, 0)
	defer cancel()
	if _, bounded := ctx.Deadline(); bounded {
		t.Fatal("omitted/zero timeout introduced an automatic deadline")
	}
	cancelParent()
	<-ctx.Done()
	if ctx.Err() != context.Canceled {
		t.Fatalf("caller cancel lost: %v", ctx.Err())
	}
}

func TestCommandLifetimeRespectsOptionalAndParentDeadlines(t *testing.T) {
	parent, stop := context.WithTimeout(context.Background(), time.Hour)
	defer stop()
	for _, timeout := range []int{0, 100, 1_800_000} {
		ctx, cancel := commandContext(parent, timeout)
		deadline, ok := ctx.Deadline()
		if !ok {
			cancel()
			t.Fatalf("caller/explicit deadline lost: %d", timeout)
		}
		parentDeadline, _ := parent.Deadline()
		if timeout == 0 && !deadline.Equal(parentDeadline) {
			t.Fatal("no-timeout request replaced the parent's deadline")
		}
		if timeout > 0 && (time.Until(deadline) <= 0 || time.Until(deadline) > time.Duration(timeout)*time.Millisecond) {
			t.Fatalf("explicit deadline changed: %d", timeout)
		}
		cancel()
	}
}

func TestCommandLifetimeRejectsDurationOverflowAndNegativeTimeout(t *testing.T) {
	for _, timeout := range []int{-1, int(^uint(0) >> 1)} {
		if timeout > 0 && int64(timeout) <= MaxTimeoutMS {
			continue // 32-bit int cannot represent a duration overflow in ms.
		}
		if err := (&Request{Command: []string{"unused"}, TimeoutMS: timeout}).Validate(); err == nil {
			t.Fatalf("invalid timeout accepted: %d", timeout)
		}
	}
}
