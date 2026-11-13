package p03

import "testing"

func TestIsLeap(t *testing.T) {
	if !IsLeap(2000) || IsLeap(1900) || !IsLeap(2024) {
		t.Fatal("leap")
	}
}
