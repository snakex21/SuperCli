package goal

import (
	"context"
	"testing"
)

func TestProgressTracksMutationsWithoutReadingOnRender(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Set(ctx, "Ship", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if p := svc.Progress(); p.Title != "Ship" || p.Total != 0 {
		t.Fatal(p)
	}
	if _, err := svc.AddTask(ctx, "", "Implement"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetTaskStatus(ctx, "", 1, TaskDone); err != nil {
		t.Fatal(err)
	}
	if p := svc.Progress(); p.Done != 1 || p.Total != 1 {
		t.Fatal(p)
	}
	if err := svc.Verify(ctx, "", true, "Test passed"); err != nil {
		t.Fatal(err)
	}
	if p := svc.Progress(); p.Verification != string(VerificationPassed) {
		t.Fatal(p)
	}
	// Removing the storage handle is safe for cached reads.
	storage := svc.storage
	svc.storage = nil
	if p := svc.Progress(); p.Done != 1 {
		t.Fatal(p)
	}
	svc.storage = storage
	if err := svc.SetStatus(ctx, "", StatusPaused); err != nil {
		t.Fatal(err)
	}
	if p := svc.Progress(); p.Title != "" {
		t.Fatal(p)
	}
}
