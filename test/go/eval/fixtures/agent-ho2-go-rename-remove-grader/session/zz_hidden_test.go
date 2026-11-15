package session

import (
	"testing"
	"time"
)

func TestHiddenRotateAndLogout(t *testing.T) {
	m := NewManager(4, time.Hour)
	m.Login("s1", []byte("a"), "t1")
	m.RotateCSRF("s1", "t2")
	if tok, ok := m.CSRF("s1"); !ok || tok != "t2" {
		t.Fatalf("CSRF = %q %v", tok, ok)
	}
	m.Login("s2", []byte("b"), "u")
	m.Logout("s1")
	if _, ok := m.Session("s2"); !ok {
		t.Fatal("s2 lost")
	}
	if _, ok := m.CSRF("s1"); ok {
		t.Fatal("csrf left")
	}
}
