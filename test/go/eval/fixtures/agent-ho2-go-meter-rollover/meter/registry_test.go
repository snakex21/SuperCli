package meter

import "testing"

func TestDigits(t *testing.T) {
	cases := map[string]int{"EM-5": 5, "em5": 5, "EMX": 7, "emx": 7, "XR-200": 5}
	for model, want := range cases {
		if got := Digits(model); got != want {
			t.Errorf("Digits(%q) = %d, want %d", model, got, want)
		}
	}
}

func TestModulus(t *testing.T) {
	if got := Modulus("EM-5"); got != 100000 {
		t.Fatalf("Modulus = %d", got)
	}
}
