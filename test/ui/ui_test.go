package ui_test

import (
	"context"
	"flag"
	"testing"
	"time"

	"supercli/internal/uitest"
)

var (
	uiFilter  = flag.String("ui-filter", "", "substring of native ESM UI test filenames")
	uiBrowser = flag.String("ui-browser", "", "dev-only Chrome/Chromium/Edge executable")
)

// This is deliberately part of go test ./..., not an opt-in integration test.
// A missing dev browser is an actionable failure, never an automatic skip.
func TestBrowserUI(t *testing.T) {
	deadline := time.Now().Add(7 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-15*time.Second).Before(deadline) {
		deadline = outer.Add(-15 * time.Second)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	result, err := uitest.Run(ctx, uitest.Options{Filter: *uiFilter, Browser: *uiBrowser})
	result.Log(t.Logf)
	if err != nil {
		t.Fatal(err)
	}
}
