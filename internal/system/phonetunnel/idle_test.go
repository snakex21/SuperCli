package phonetunnel

import (
	"context"
	"strings"
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
	before := time.Now()
	if err := m.Start(context.Background(), "http://127.0.0.1:1", false, nil); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("stop")
	st := waitState(t, m, "on")
	if st.ExpiresAt.Before(before.Add(24*time.Hour)) || st.ExpiresAt.After(time.Now().Add(24*time.Hour)) {
		t.Fatal(st.ExpiresAt)
	}
	m.Touch()
	if !m.Status().ExpiresAt.After(st.ExpiresAt) {
		t.Fatal("prompt did not renew expiry")
	}
}
