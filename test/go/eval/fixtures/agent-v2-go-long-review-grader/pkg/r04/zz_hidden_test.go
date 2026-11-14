package r04

import "testing"

func TestHiddenReview(t *testing.T) {
	for _, bad := range []string{"0", "65536", "70000", "-1", "x"} {
		if _, err := ParsePort(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if p, err := ParsePort("65535"); err != nil || p != 65535 {
		t.Fatal("65535")
	}
}
