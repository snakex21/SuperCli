package meter

import "testing"

func TestHiddenModelNames(t *testing.T) {
	cases := map[string]int{
		"EM-6": 6, "em6": 6, " Em 6 ": 6, "EM.6": 6, "em-5": 5, "EM5": 5,
		"EMX": 7, "em_x": 7, "E M X": 7, "XR-200": 5, "EM-56": 5, "": 5,
	}
	for model, want := range cases {
		if got := Digits(model); got != want {
			t.Errorf("Digits(%q) = %d, want %d", model, got, want)
		}
	}
	if Modulus("em-6") != 1000000 || Modulus("EMX") != 10000000 || Modulus("nope") != 100000 {
		t.Fatal("Modulus")
	}
	if !Known("Em 6") || Known("EM-56") {
		t.Fatal("Known")
	}
}
