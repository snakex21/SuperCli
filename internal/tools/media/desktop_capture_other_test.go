//go:build !windows

package media

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDesktopOnlyUnsupportedPlatformAndCancellation(t *testing.T) {
	data, mime, _, err := captureDesktop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "only on Windows") || len(data) != 0 || mime != "" {
		t.Fatalf("capture=%d mime=%s error=%v", len(data), mime, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := captureDesktop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled=%v", err)
	}
}
