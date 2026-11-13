package p09

import "testing"

func TestFactorial(t *testing.T) {
	if Factorial(0) != 1 || Factorial(5) != 120 {
		t.Fatal(Factorial(5))
	}
}
