package http

import "testing"

func TestNew(t *testing.T) {
	if s := New(":0", nil); s.Addr != ":0" {
		t.Fatal(s.Addr)
	}
}
