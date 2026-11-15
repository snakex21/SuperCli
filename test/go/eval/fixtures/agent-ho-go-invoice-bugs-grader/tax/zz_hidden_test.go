package tax

import "testing"

func TestHiddenCountryCase(t *testing.T) {
	for _, c := range []string{"de", " pl ", "Nl", "AT"} {
		if _, err := RateFor(c); err != nil {
			t.Errorf("RateFor(%q): %v", c, err)
		}
	}
	if r, _ := RateFor(" pl "); r != 2300 {
		t.Errorf("pl = %d", r)
	}
	for _, c := range []string{"xx", "", "D E"} {
		if _, err := RateFor(c); err == nil {
			t.Errorf("RateFor(%q) should fail", c)
		}
	}
}
