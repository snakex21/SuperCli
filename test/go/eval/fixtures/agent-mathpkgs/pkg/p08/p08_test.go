package p08

import "testing"

func TestCountVowels(t *testing.T) {
	if CountVowels("queue") != 4 {
		t.Fatal(CountVowels("queue"))
	}
}
