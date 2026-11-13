package p04

import "testing"

func TestFib(t *testing.T) {
	if Fib(0) != 0 || Fib(1) != 1 || Fib(10) != 55 {
		t.Fatal(Fib(10))
	}
}
