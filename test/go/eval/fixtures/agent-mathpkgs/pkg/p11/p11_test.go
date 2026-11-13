package p11

import "testing"

func TestTrimAll(t *testing.T) {
	if TrimAll("ababx", "ab") != "x" || TrimAll("x", "") != "x" {
		t.Fatal(TrimAll("ababx", "ab"))
	}
}
