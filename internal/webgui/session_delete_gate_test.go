package webgui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/checkpoint"
)

// Another process (CLI/TUI, worker, AV scanner on Windows) may hold the native
// checkpoint store lock for a moment. Acquire makes one native attempt only,
// so deleting a conversation must retry instead of failing immediately.
func TestDeleteSessionRetriesExternallyBusyStoreGate(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "echo", "busy gate")
	if err != nil {
		t.Fatal(err)
	}
	oldAcquire, oldDelay := acquireStoreGate, storeGateRetryDelay
	t.Cleanup(func() { acquireStoreGate, storeGateRetryDelay = oldAcquire, oldDelay })
	storeGateRetryDelay = time.Millisecond
	attempts := 0
	acquireStoreGate = func(ctx context.Context, gate *checkpoint.StoreGate) (*checkpoint.StoreIO, error) {
		attempts++
		if attempts <= 2 {
			return nil, fmt.Errorf("%w: sharing violation", checkpoint.ErrStoreBusy)
		}
		return gate.Acquire(ctx)
	}
	if err := srv.eng.deleteSession(sess.ID); err != nil {
		if errors.Is(err, checkpoint.ErrStoreUnsupported) {
			t.Skip(err)
		}
		t.Fatalf("delete with briefly busy gate: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if _, err := store.Get(sess.ID); err == nil {
		t.Fatal("session still exists after delete")
	}
}

func TestSessionMutationErrorReportsBusyStoreAsConflict(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSessionMutationError(rec, errors.Join(fmt.Errorf("%w: locked", checkpoint.ErrStoreBusy), context.DeadlineExceeded))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}
