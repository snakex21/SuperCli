package p05

import "testing"

func TestCToF(t *testing.T) {
	if CToF(100) != 212 || CToF(0) != 32 {
		t.Fatal(CToF(100))
	}
}
