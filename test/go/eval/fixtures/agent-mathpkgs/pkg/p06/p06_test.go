package p06

import "testing"

func TestIndexOf(t *testing.T) {
	if IndexOf([]string{"a", "b"}, "b") != 1 || IndexOf(nil, "x") != -1 {
		t.Fatal("index")
	}
}
