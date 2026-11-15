package session

import (
	"testing"
	"time"
)

func TestLoginLogout(t *testing.T) {
	m := NewManager(4, time.Hour)
	m.Login("s1", []byte("alice"), "tok")
	if _, ok := m.Session("s1"); !ok {
		t.Fatal("no session")
	}
	m.Logout("s1")
	if _, ok := m.Session("s1"); ok {
		t.Fatal("still logged in")
	}
	if _, ok := m.CSRF("s1"); ok {
		t.Fatal("csrf left behind")
	}
}
