package r04

import "testing"

func TestVisible(t *testing.T) {
	if p, _ := ParsePort("8080"); p != 8080 {
		t.Fatal("port")
	}
}
