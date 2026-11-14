package phonetunnel

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerDefaultIdle24Hours(t *testing.T) {
	m := New(t.TempDir())
	if m.IdleTimeout != 24*time.Hour {
		t.Fatal(m.IdleTimeout)
	}
	m.Installer = fixtureInstaller(t)
	m.Factory = func(string, string) (Process, error) {
		return &fakeProcess{reader: strings.NewReader("https://test-phone.trycloudflare.com\n"), done: make(chan struct{})}, nil
	}
	// Deterministic clock: on Windows two time.Now() calls can land in the
	// same tick, so Touch() could not be observed to move the expiry.
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var offset atomic.Int64
	m.now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	if err := m.Start(context.Background(), "http://127.0.0.1:1", false, nil); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("stop")
	st := waitState(t, m, "on")
	if !st.ExpiresAt.Equal(base.Add(24 * time.Hour)) {
		t.Fatal(st.ExpiresAt)
	}
	offset.Store(int64(time.Minute))
	m.Touch()
	if got := m.Status().ExpiresAt; !got.Equal(base.Add(time.Minute + 24*time.Hour)) {
		t.Fatalf("prompt did not renew expiry: %v", got)
	}
}
